package core

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/sandbox"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
)

// stuckSubscriber connects to f's core, subscribes to id, reads up to the subscribe's answer and then never reads
// again: its socket stops draining. It returns the client end.
func stuckSubscriber(t *testing.T, f *fixture, id proto.SessionID) net.Conn {
	t.Helper()
	a, b := net.Pipe()
	t.Cleanup(func() { a.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go f.core.Serve(ctx, b)
	r := bufio.NewReader(a)
	call := func(n int, method string, params any) {
		p, _ := json.Marshal(params)
		line, _ := json.Marshal(proto.Request{JSONRPC: "2.0", ID: json.RawMessage(strconv.Itoa(n)), Method: method, Params: p})
		if _, err := a.Write(append(line, '\n')); err != nil {
			t.Fatal(err)
		}
		for { // the replay's events come before the answer
			l, err := r.ReadBytes('\n')
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(l, []byte(`"id":`+strconv.Itoa(n)+`,`)) {
				return
			}
		}
	}
	call(1, proto.MethodInitialize, proto.InitializeParams{Client: proto.ClientInfo{Name: "stuck", Version: "0"}, Protocol: proto.Version})
	call(2, proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: id})
	return a
}

// TestAStuckSubscriberStallsNothing: a subscriber whose socket stops draining holds up neither another client's turn
// nor a Halt for long.
func TestAStuckSubscriberStallsNothing(t *testing.T) {
	f := newFixture(t, goinfertest.Says("one"))
	id := f.session(t)
	stuckSubscriber(t, f, id)
	f.core.opt.Sweep = func(sandbox.Target, bool) (sandbox.Report, error) {
		return sandbox.Report{Refused: "1 process(es) carry mark …"}, nil
	}
	done := make(chan error, 1)
	go func() {
		if err := f.client.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: id}, nil); err != nil {
			done <- err
			return
		}
		if err := f.client.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: id, Content: []proto.ContentBlock{proto.Text("hello")}}, nil); err != nil {
			done <- err
			return
		}
		for ev := range f.events {
			if ev.Type == proto.EvTurnEnd {
				break
			}
		}
		f.core.Halt(proto.DaemonHalted{Trigger: proto.HaltByCommand, By: "test"})
		done <- nil
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(sendTimeout + 5*time.Second):
		t.Fatal("a turn and a Halt are stuck behind a subscriber that does not read")
	}
}

// TestAStuckSubscriberIsDropped: a subscriber that leaves no room for sendTimeout is cut off: its connection is closed
// and its subscription gone, and the session goes on for everyone else.
func TestAStuckSubscriberIsDropped(t *testing.T) {
	q, to := sendQueue, sendTimeout
	sendQueue, sendTimeout = 1, 100*time.Millisecond
	t.Cleanup(func() { sendQueue, sendTimeout = q, to })
	f := newFixture(t, goinfertest.Says("one"))
	id := f.session(t)
	stuck := stuckSubscriber(t, f, id)
	if err := f.client.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: id}, nil); err != nil {
		t.Fatal(err)
	}
	f.prompt(t, id, "hello")
	f.waitTurnEnd(t)
	// The stuck client's end reads what was queued before the drop, then finds the connection closed.
	stuck.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 1<<16)
	var err error
	for err == nil {
		_, err = stuck.Read(buf)
	}
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatalf("the stuck subscriber's connection is still open: %v", err)
	}
	s, perr := f.core.session(id)
	if perr != nil {
		t.Fatal(perr)
	}
	for deadline := time.Now().Add(2 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		s.mu.Lock()
		n := len(s.subs)
		s.mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d subscriptions, want 1: the dropped one is still subscribed", n)
		}
	}
}

// TestAReplayLongerThanTheQueueArrives: a subscribe replays a log longer than sendQueue to a client that reads, and the
// client hears all of it: a send waits for room rather than dropping a client that is reading.
func TestAReplayLongerThanTheQueueArrives(t *testing.T) {
	q := sendQueue
	sendQueue = 1
	t.Cleanup(func() { sendQueue = q })
	f := newFixture(t, goinfertest.Says("one"))
	id := f.session(t)
	f.prompt(t, id, "hello") // no subscription yet: the turn's events reach the client only by replay
	st := f.core.opt.Store
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		evs, _ := st.Events(id, 0)
		if len(evs) > 0 && evs[len(evs)-1].Type == proto.EvTurnEnd {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the turn never ended")
		}
	}
	evs, _ := st.Events(id, 0)
	if len(evs) < 4 {
		t.Fatalf("the log has %d events, too few to pass a queue of 1", len(evs))
	}
	if err := f.client.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: id}, nil); err != nil {
		t.Fatal(err)
	}
	if got := f.waitTurnEnd(t); len(got) != len(evs) {
		t.Errorf("heard %d of the log's %d events", len(got), len(evs))
	}
}
