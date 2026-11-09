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
	"strings"
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

// TestADroppedConnectionIsNotSubscribed: a connection dropped before its replay registers it stays unsubscribed; its
// unsubscribe may already have run, and nothing else would remove it.
func TestADroppedConnectionIsNotSubscribed(t *testing.T) {
	f := newFixture(t)
	id := f.session(t)
	cn := newConn(io.Discard, nil)
	cn.drop()
	// From past the log's end: nothing to send, so only the check before registering can keep it out.
	p, _ := json.Marshal(proto.SessionSubscribeParams{Session: id, FromSeq: 1000})
	f.core.replay(cn, proto.Request{JSONRPC: "2.0", ID: json.RawMessage("1"), Method: proto.MethodSessionSubscribe, Params: p})
	s, _ := f.core.session(id)
	s.mu.Lock()
	defer s.mu.Unlock()
	for sub := range s.subs {
		if sub.conn == cn {
			t.Fatal("a dropped connection was subscribed")
		}
	}
}

// TestALongReplayHoldsUpNoTurn: a client that stops reading during its replay of a long log holds up no other client's
// turn: the replay goes out without the session's lock.
func TestALongReplayHoldsUpNoTurn(t *testing.T) {
	q, to := sendQueue, sendTimeout
	sendQueue, sendTimeout = 1, 3*time.Second
	t.Cleanup(func() { sendQueue, sendTimeout = q, to })
	f := newFixture(t, goinfertest.Says("one"), goinfertest.Says("two"))
	id := f.session(t)
	if err := f.client.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: id}, nil); err != nil {
		t.Fatal(err)
	}
	f.prompt(t, id, "first")
	f.waitTurnEnd(t)          // a log of several events
	stuckSubscriber(t, f, id) // its replay stalls on a full queue of 1
	done := make(chan error, 1)
	go func() {
		done <- f.client.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: id, Content: []proto.ContentBlock{proto.Text("second")}}, nil)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("a prompt waited more than 1 s beside a stuck replay: the replay held the session")
	}
	f.waitTurnEnd(t)
}

// TestAClientCatchingUpIsNotDropped: a client still reading a long replay, slowly, is not dropped by the first live
// events for the queue the replay filled, and hears the next turn.
func TestAClientCatchingUpIsNotDropped(t *testing.T) {
	q, w := sendQueue, fanOutWait
	sendQueue, fanOutWait = 2, time.Millisecond
	t.Cleanup(func() { sendQueue, fanOutWait = q, w })
	f := newFixture(t, goinfertest.Says("one"), goinfertest.Says("two"))
	id := f.session(t)
	f.prompt(t, id, "first") // unsubscribed: the turn's events reach the slow client only by replay
	st := f.core.opt.Store
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		evs, _ := st.Events(id, 0)
		if len(evs) > 0 && evs[len(evs)-1].Type == proto.EvTurnEnd {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the first turn never ended")
		}
	}
	a, b := net.Pipe()
	t.Cleanup(func() { a.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go f.core.Serve(ctx, b)
	ends := make(chan struct{}, 4)
	go func() { // a slow reader: every line takes it 20 ms
		r := bufio.NewReader(a)
		for {
			l, err := r.ReadBytes('\n')
			if err != nil {
				return
			}
			if bytes.Contains(l, []byte(`"type":"turn.end"`)) {
				ends <- struct{}{}
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
	for n, m := range []struct {
		method string
		params any
	}{{proto.MethodInitialize, proto.InitializeParams{Client: proto.ClientInfo{Name: "slow", Version: "0"}, Protocol: proto.Version}},
		{proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: id}}} {
		p, _ := json.Marshal(m.params)
		line, _ := json.Marshal(proto.Request{JSONRPC: "2.0", ID: json.RawMessage(strconv.Itoa(n + 1)), Method: m.method, Params: p})
		a.Write(append(line, '\n'))
	}
	f.prompt(t, id, "second") // its live events meet a queue the replay is still draining
	for i := range 2 {
		select {
		case <-ends:
		case <-time.After(10 * time.Second):
			t.Fatalf("the slow client heard %d of the two turns' ends: it was dropped while catching up", i)
		}
	}
}

// TestAClientThatStopsWhileCatchingUpCostsOneWait: a client that stops reading partway through its replay holds a live
// turn up for one catchUpWait, not the send timeout.
func TestAClientThatStopsWhileCatchingUpCostsOneWait(t *testing.T) {
	q, w, c, to := sendQueue, fanOutWait, catchUpWait, sendTimeout
	sendQueue, fanOutWait, catchUpWait, sendTimeout = 2, time.Millisecond, 100*time.Millisecond, 10*time.Second
	t.Cleanup(func() { sendQueue, fanOutWait, catchUpWait, sendTimeout = q, w, c, to })
	f := newFixture(t, goinfertest.Says("one"), goinfertest.Says("two"))
	id := f.session(t)
	f.prompt(t, id, "first")
	st := f.core.opt.Store
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		evs, _ := st.Events(id, 0)
		if len(evs) > 0 && evs[len(evs)-1].Type == proto.EvTurnEnd {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the first turn never ended")
		}
	}
	a, b := net.Pipe()
	t.Cleanup(func() { a.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go f.core.Serve(ctx, b)
	logged, _ := st.Events(id, 0)
	read := len(logged) // its two answers and all but the last two replayed events, which stay queued
	if read < 4 {
		t.Fatalf("the log has %d events, too few to leave the queue of 2 full", read)
	}
	registered := make(chan struct{})
	go func() { // reads slowly, then stops with the replay's last two still queued: it is catching up when it stops
		defer close(registered)
		r := bufio.NewReader(a)
		for range read {
			if _, err := r.ReadBytes('\n'); err != nil {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
	for n, m := range []struct {
		method string
		params any
	}{{proto.MethodInitialize, proto.InitializeParams{Client: proto.ClientInfo{Name: "stops", Version: "0"}, Protocol: proto.Version}},
		{proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: id}}} {
		p, _ := json.Marshal(m.params)
		line, _ := json.Marshal(proto.Request{JSONRPC: "2.0", ID: json.RawMessage(strconv.Itoa(n + 1)), Method: m.method, Params: p})
		a.Write(append(line, '\n'))
	}
	if err := f.client.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: id}, nil); err != nil {
		t.Fatal(err)
	}
	f.waitTurnEnd(t) // the replay of the first turn
	<-registered
	time.Sleep(50 * time.Millisecond) // its replay registered: it is now catching up, and reads no more
	start := time.Now()
	f.prompt(t, id, "second")
	f.waitTurnEnd(t)
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("a turn took %v beside a client that stopped while catching up: more than one catchUpWait", took)
	}
}

// TestASlowReaderCannotPaceTheSession: a client that keeps reading, slowly, while live events keep coming, stops
// catching up once its replay is written, so the turn falls back to fanOutWait for it rather than its pace.
func TestASlowReaderCannotPaceTheSession(t *testing.T) {
	q, w, c, to := sendQueue, fanOutWait, catchUpWait, sendTimeout
	sendQueue, fanOutWait, catchUpWait, sendTimeout = 2, time.Millisecond, 400*time.Millisecond, 600*time.Millisecond
	t.Cleanup(func() { sendQueue, fanOutWait, catchUpWait, sendTimeout = q, w, c, to })
	f := newFixture(t, goinfertest.Says("one"), goinfertest.Says("two"))
	id := f.session(t)
	f.prompt(t, id, "first")
	st := f.core.opt.Store
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		evs, _ := st.Events(id, 0)
		if len(evs) > 0 && evs[len(evs)-1].Type == proto.EvTurnEnd {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the first turn never ended")
		}
	}
	a, b := net.Pipe()
	t.Cleanup(func() { a.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go f.core.Serve(ctx, b)
	go func() { // reads a line every 300 ms, for as long as it can
		r := bufio.NewReader(a)
		for {
			if _, err := r.ReadBytes('\n'); err != nil {
				return
			}
			time.Sleep(300 * time.Millisecond)
		}
	}()
	for n, m := range []struct {
		method string
		params any
	}{{proto.MethodInitialize, proto.InitializeParams{Client: proto.ClientInfo{Name: "slow", Version: "0"}, Protocol: proto.Version}},
		{proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: id}}} {
		p, _ := json.Marshal(m.params)
		line, _ := json.Marshal(proto.Request{JSONRPC: "2.0", ID: json.RawMessage(strconv.Itoa(n + 1)), Method: m.method, Params: p})
		a.Write(append(line, '\n'))
	}
	if err := f.client.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: id}, nil); err != nil {
		t.Fatal(err)
	}
	f.waitTurnEnd(t) // the first turn, replayed
	s, _ := f.core.session(id)
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(5 * time.Millisecond) { // the slow client registered
		s.mu.Lock()
		n := len(s.subs)
		s.mu.Unlock()
		if n == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the slow client never finished its replay")
		}
	}
	start := time.Now()
	f.prompt(t, id, "second")
	f.waitTurnEnd(t)
	if took := time.Since(start); took > 1500*time.Millisecond {
		t.Errorf("a turn took %v beside a slow reader: it set the session's pace past sendTimeout", took)
	}
}

// blockingWriter takes each write only when released.
type blockingWriter struct{ release chan struct{} }

func (b blockingWriter) Write(p []byte) (int, error) { <-b.release; return len(p), nil }

// TestCatchingUpEndsAtTheReplaysLastMessage: catching up lasts until the replay's last message is written, not until
// the queue is first empty: a replay's second phase may still be sending into it.
func TestCatchingUpEndsAtTheReplaysLastMessage(t *testing.T) {
	bw := blockingWriter{release: make(chan struct{})}
	cn := newConn(bw, nil)
	t.Cleanup(func() { close(bw.release) })
	cn.send("one")
	cn.catchUp(2) // "one", then two more the replay is about to send
	bw.release <- struct{}{}
	for deadline := time.Now().Add(2 * time.Second); cn.writes.Load() < 1; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the first message was never written")
		}
	}
	if !cn.catchingUp.Load() {
		t.Error("catching up ended when the queue was first empty, with two of the replay to come")
	}
	cn.send("two")
	cn.send("three")
	bw.release <- struct{}{}
	bw.release <- struct{}{}
	for deadline := time.Now().Add(2 * time.Second); cn.catchingUp.Load(); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("catching up did not end at the replay's last message")
		}
	}
}

// TestALargeMessageIsProgress: a message that takes longer than catchUpWait to write, to a client that is reading it,
// does not get the client dropped: bytes going out are progress.
func TestALargeMessageIsProgress(t *testing.T) {
	q, c := sendQueue, catchUpWait
	sendQueue, catchUpWait = 1, 200*time.Millisecond
	t.Cleanup(func() { sendQueue, catchUpWait = q, c })
	a, b := net.Pipe()
	t.Cleanup(func() { a.Close(); b.Close() })
	go func() { // reads 64 KB every 30 ms: the 1 MB message takes about half a second
		buf := make([]byte, 64<<10)
		for {
			if _, err := io.ReadFull(a, buf); err != nil {
				return
			}
			time.Sleep(30 * time.Millisecond)
		}
	}()
	cn := newConn(b, nil)
	cn.send(strings.Repeat("x", 1<<20)) // the writer takes it, and the queue is empty
	for deadline := time.Now().Add(2 * time.Second); cn.progress.Load() == 0; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the large message never started")
		}
	}
	cn.send("filler") // the queue of 1 is full behind it
	cn.catchUp(0)
	if err := cn.fanOut("live"); err != nil {
		t.Fatalf("a live event to a client reading a large message: %v", err)
	}
}

// TestCatchingUpHasADeadline: catching up lasts sendTimeout at most: past it, a live event waits only fanOutWait, even
// with the replay unwritten.
func TestCatchingUpHasADeadline(t *testing.T) {
	q, w, c, to := sendQueue, fanOutWait, catchUpWait, sendTimeout
	sendQueue, fanOutWait, catchUpWait, sendTimeout = 1, 10*time.Millisecond, 2*time.Second, 100*time.Millisecond
	t.Cleanup(func() { sendQueue, fanOutWait, catchUpWait, sendTimeout = q, w, c, to })
	bw := blockingWriter{release: make(chan struct{})}
	cn := newConn(bw, nil)
	t.Cleanup(func() { close(bw.release) })
	cn.send("one") // the writer takes it and blocks
	for deadline := time.Now().Add(2 * time.Second); len(cn.out) > 0; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the writer never took the first message")
		}
	}
	cn.send("two") // the queue of 1 is full
	cn.catchUp(100)
	time.Sleep(150 * time.Millisecond) // past its deadline
	start := time.Now()
	cn.fanOut("live")
	if took := time.Since(start); took > 500*time.Millisecond {
		t.Errorf("a live event waited %v on a connection past its catch-up deadline; want about fanOutWait", took)
	}
}

// TestAFlushMarkCountsAsWritten: the writer counts a flush mark as it counts a message, since catchUp's target counts
// everything queued; a mark left uncounted would hold catching up to its deadline.
func TestAFlushMarkCountsAsWritten(t *testing.T) {
	cn := newConn(io.Discard, nil)
	cn.send("a")
	cn.flush()
	if n := cn.writes.Load(); n != 2 {
		t.Errorf("writes %d after a message and a flush, want 2", n)
	}
}

// TestACatchUpWaitEndsAtTheDeadline: a live event to a client that is reading, slowly, a large message waits no longer
// than catching up has left, though catchUpWait is longer.
func TestACatchUpWaitEndsAtTheDeadline(t *testing.T) {
	q, c, to := sendQueue, catchUpWait, sendTimeout
	sendQueue, catchUpWait, sendTimeout = 1, 3*time.Second, 300*time.Millisecond
	t.Cleanup(func() { sendQueue, catchUpWait, sendTimeout = q, c, to })
	a, b := net.Pipe()
	t.Cleanup(func() { a.Close(); b.Close() })
	go func() { // reads 64 KB every 50 ms: a 4 MB message takes about 3 s
		buf := make([]byte, 64<<10)
		for {
			if _, err := io.ReadFull(a, buf); err != nil {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	}()
	cn := newConn(b, nil)
	cn.send(strings.Repeat("x", 4<<20))
	for deadline := time.Now().Add(2 * time.Second); cn.progress.Load() == 0; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the large message never started")
		}
	}
	cn.send("filler")
	cn.catchUp(0)
	start := time.Now()
	cn.fanOut("live")
	if took := time.Since(start); took > time.Second {
		t.Errorf("a live event waited %v; catching up had 300 ms left", took)
	}
}
