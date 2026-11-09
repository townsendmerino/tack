package core

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
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

// TestStuckSubscribersCostTheSessionLittle: a session's fan-out waits on a full queue only fanOutWait, so two stuck
// subscribers cost a turn and a Halt nothing like the send timeout each.
func TestStuckSubscribersCostTheSessionLittle(t *testing.T) {
	q, to := sendQueue, sendTimeout
	sendQueue, sendTimeout = 1, 10*time.Second
	t.Cleanup(func() { sendQueue, sendTimeout = q, to })
	f := newFixture(t, goinfertest.Says("one"))
	id := f.session(t)
	stuckSubscriber(t, f, id)
	stuckSubscriber(t, f, id)
	f.core.opt.Sweep = func(sandbox.Target, bool) (sandbox.Report, error) {
		return sandbox.Report{Refused: "1 process(es) carry mark …"}, nil
	}
	start := time.Now()
	done := make(chan error, 1)
	go func() {
		if err := f.client.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: id}, nil); err != nil {
			done <- err
			return
		}
		f.prompt(t, id, "hello")
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
	case <-time.After(sendTimeout + 10*time.Second):
		t.Fatal("stuck")
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("a turn and a Halt took %v beside two stuck subscribers; the fan-out waited on them", took)
	}
}

// unclosable hides a connection's Close: a transport core cannot close.
type unclosable struct{ net.Conn }

func (u unclosable) Read(p []byte) (int, error)  { return u.Conn.Read(p) }
func (u unclosable) Write(p []byte) (int, error) { return u.Conn.Write(p) }

// TestADroppedSubscriberLeavesOnATransportThatCannotClose: dropping a connection unsubscribes it even when core cannot
// close its transport, so a stuck subscriber does not stay until its Serve ends.
func TestADroppedSubscriberLeavesOnATransportThatCannotClose(t *testing.T) {
	q := sendQueue
	sendQueue = 1
	t.Cleanup(func() { sendQueue = q })
	f := newFixture(t, goinfertest.Says("one"))
	id := f.session(t)
	a, b := net.Pipe()
	t.Cleanup(func() { a.Close(); b.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go f.core.Serve(ctx, struct{ io.ReadWriter }{unclosable{b}})
	r := bufio.NewReader(a)
	for n, m := range []struct {
		method string
		params any
	}{{proto.MethodInitialize, proto.InitializeParams{Client: proto.ClientInfo{Name: "stuck", Version: "0"}, Protocol: proto.Version}},
		{proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: id}}} {
		p, _ := json.Marshal(m.params)
		line, _ := json.Marshal(proto.Request{JSONRPC: "2.0", ID: json.RawMessage(strconv.Itoa(n + 1)), Method: m.method, Params: p})
		a.Write(append(line, '\n'))
		for {
			l, err := r.ReadBytes('\n')
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(l, []byte(`"id":`+strconv.Itoa(n+1)+`,`)) {
				break
			}
		}
	}
	if err := f.client.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: id}, nil); err != nil {
		t.Fatal(err)
	}
	f.prompt(t, id, "hello")
	f.waitTurnEnd(t)
	s, _ := f.core.session(id)
	for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		s.mu.Lock()
		n := len(s.subs)
		s.mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d subscriptions, want 1: the dropped one stays on a transport core cannot close", n)
		}
	}
}

// eofThenPipe reads one request line and then EOF, and writes to a pipe nobody reads: a writer stuck in Encode when
// Serve ends.
type eofThenPipe struct {
	io.Reader
	net.Conn
}

func (e eofThenPipe) Read(p []byte) (int, error) { return e.Reader.Read(p) }

// TestFinishUnblocksAStuckWriter: when Serve's end cannot write what is queued within the send timeout, the transport is
// closed, so the writer stuck in Encode returns rather than staying blocked.
func TestFinishUnblocksAStuckWriter(t *testing.T) {
	to := sendTimeout
	sendTimeout = 100 * time.Millisecond
	t.Cleanup(func() { sendTimeout = to })
	f := newFixture(t)
	a, b := net.Pipe()
	t.Cleanup(func() { a.Close(); b.Close() })
	p, _ := json.Marshal(proto.InitializeParams{Client: proto.ClientInfo{Name: "t", Version: "0"}, Protocol: proto.Version})
	line, _ := json.Marshal(proto.Request{JSONRPC: "2.0", ID: json.RawMessage("1"), Method: proto.MethodInitialize, Params: p})
	served := make(chan struct{})
	go func() {
		f.core.Serve(context.Background(), eofThenPipe{Reader: bytes.NewReader(append(line, '\n')), Conn: b})
		close(served)
	}()
	select {
	case <-served:
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not end")
	}
	// The answer to initialize was never read. With the transport closed, a write from the far end fails at once;
	// left open, it blocks until its deadline.
	a.SetWriteDeadline(time.Now().Add(time.Second))
	if _, err := a.Write([]byte("x")); err == nil || errors.Is(err, os.ErrDeadlineExceeded) {
		t.Errorf("the far end's write: %v; want the transport closed", err)
	}
}
