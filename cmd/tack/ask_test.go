package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/core"
	"github.com/townsendmerino/tack/internal/daemon"
	"github.com/townsendmerino/tack/internal/proto"
)

// TestAskEndsWhenTheDaemonGoes: a daemon that takes the prompt and then goes away mid-turn (a crash, a second signal)
// ends ask with an error saying so, rather than leaving it waiting for a turn.end that will not come.
func TestAskEndsWhenTheDaemonGoes(t *testing.T) {
	a, b := net.Pipe()
	t.Cleanup(func() { a.Close() })
	go func() {
		defer b.Close()
		sc, enc := bufio.NewScanner(b), json.NewEncoder(b)
		for sc.Scan() {
			var req proto.Request
			if json.Unmarshal(sc.Bytes(), &req) != nil {
				continue
			}
			var result any = struct{}{}
			switch req.Method {
			case proto.MethodSessionNew:
				result = proto.SessionNewResult{Session: "s_1"}
			case proto.MethodSessionPrompt:
				result = proto.SessionPromptResult{Seq: 2}
			}
			r, _ := json.Marshal(result)
			enc.Encode(proto.Response{JSONRPC: "2.0", ID: req.ID, Result: r})
			if req.Method == proto.MethodSessionPrompt {
				return // gone mid-turn
			}
		}
	}()
	var stdout, stderr bytes.Buffer
	code := make(chan int, 1)
	go func() {
		code <- ask(context.Background(), core.NewClient(a), "/w", "q", proto.ModePlan, false, true, &stdout, &stderr, nil)
	}()
	select {
	case c := <-code:
		if c != exitError || !strings.Contains(stderr.String(), "connection") {
			t.Fatalf("ask = %d, stderr %q; want an error naming the lost connection", c, stderr.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ask still waits for the turn.end of a daemon that has gone")
	}
}

// TestTheDaemonIsGivenAnAbsoluteWorkspace: a relative --workspace is this process's, so it is made absolute here; the
// daemon runs in another directory.
func TestTheDaemonIsGivenAnAbsoluteWorkspace(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	got, err := clientWorkspace("sub")
	if err != nil || got != filepath.Join(wd, "sub") {
		t.Fatalf("clientWorkspace(%q) = %q, %v; want %q", "sub", got, err, filepath.Join(wd, "sub"))
	}
	if got, err := clientWorkspace(""); err != nil || !filepath.IsAbs(got) {
		t.Fatalf("clientWorkspace(\"\") = %q, %v; want the git root, absolute", got, err)
	}
}

// TestASpawnWaitsOutADrain: a client that starts a daemon while the last one drains gets the new socket only once the
// old daemon lets go of the lock, after its grace window; so the wait for the socket covers the configured grace, as
// tack daemon stop's wait does.
func TestASpawnWaitsOutADrain(t *testing.T) {
	for _, grace := range []time.Duration{10 * time.Second, time.Minute} {
		if w := drainWait(grace); w < grace+5*time.Second {
			t.Errorf("drainWait(%s) = %s; want the grace and the drain's 5s to record cancelled turns, at least", grace, w)
		}
	}
}

// TestStopWhenTheDaemonGoesMidway: a daemon that finishes shutting down while stop is talking to it closes the
// connection, and lets go of its lock a moment later; stop waits for the lock as for any stopping daemon, and reports
// it stopped, not an error.
func TestStopWhenTheDaemonGoesMidway(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "tack-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	l, err := daemon.Acquire(dir, "1") // the daemon's lock, and its record naming this process
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	ln, err := net.Listen("unix", daemon.SocketPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() { // a daemon that initializes the connection, then is gone: the connection, then the lock
		nc, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() {
			nc.Close()
			time.Sleep(300 * time.Millisecond)
			l.Release()
		}()
		sc, enc := bufio.NewScanner(nc), json.NewEncoder(nc)
		for sc.Scan() {
			var req proto.Request
			if json.Unmarshal(sc.Bytes(), &req) != nil || req.Method != proto.MethodInitialize {
				return
			}
			r, _ := json.Marshal(proto.InitializeResult{Protocol: proto.Version})
			enc.Encode(proto.Response{JSONRPC: "2.0", ID: req.ID, Result: r})
		}
	}()
	var out, errb bytes.Buffer
	began := time.Now()
	if code := daemonStop(dir, time.Second, &out, &errb); code != exitOK || !strings.Contains(out.String(), "stopped") || time.Since(began) < 250*time.Millisecond {
		t.Fatalf("stop on a daemon gone midway: exit %d after %s, %q %q; want 0 and stopped, once the lock was let go", code, time.Since(began), out.String(), errb.String())
	}
}

// TestClientWaitsFollowTheStoppingRecord: a client waits on a stopping daemon as long as its record says it needs
// (Until), not only the grace this client's config gives; and a client that starts a daemon waits longer still, for the
// new one to open its store, recover and bind its socket once the old one lets go.
func TestClientWaitsFollowTheStoppingRecord(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "tack-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	l, err := daemon.Acquire(dir, "1")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	l.SetShutdown(2 * time.Minute) // its grace is longer than this client's config says
	ln, err := l.Listen()
	if err != nil {
		t.Fatal(err)
	}
	ln.Close() // stopping, until two minutes from now
	if w := stopWaitFor(dir, 10*time.Second); w < 2*time.Minute-time.Second {
		t.Errorf("stop's wait on a daemon stopping for two minutes: %s; want two minutes at least", w)
	}
	if w := spawnWaitFor(dir, 10*time.Second); w < 2*time.Minute+5*time.Second {
		t.Errorf("a spawn's wait behind a daemon stopping for two minutes: %s; want two minutes and room to start", w)
	}
}

// TestAskLeavesTheDaemonsTurnOnAnInterrupt: an interrupt ends tack run --daemon but not the daemon's turn, and says
// which session is left running.
func TestAskLeavesTheDaemonsTurnOnAnInterrupt(t *testing.T) {
	cl := fakeAsk(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	var stdout, stderr bytes.Buffer
	code := make(chan int, 1)
	go func() { code <- ask(ctx, cl, "/w", "q", proto.ModePlan, false, true, &stdout, &stderr, nil) }()
	time.Sleep(100 * time.Millisecond) // prompted, the turn under way
	cancel()
	select {
	case c := <-code:
		if c != exitError || !strings.Contains(stderr.String(), "left session s_1 running in the daemon") {
			t.Fatalf("ask after an interrupt: %d, %q; want 1 and the session named as left running", c, stderr.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ask did not return after the interrupt")
	}
}

// TestAskOnAHaltedDaemonExits5: a halted daemon refuses the prompt (CodeHalted), and tack run --daemon exits 5, as an
// in-process run does.
func TestAskOnAHaltedDaemonExits5(t *testing.T) {
	cl := fakeAsk(t, &proto.Error{Code: proto.CodeHalted, Message: "tack is halted; run tack resume"})
	var stdout, stderr bytes.Buffer
	if c := ask(context.Background(), cl, "/w", "q", proto.ModePlan, false, true, &stdout, &stderr, nil); c != exitHalted {
		t.Fatalf("ask on a halted daemon: %d, %q; want %d", c, stderr.String(), exitHalted)
	}
}

// fakeAsk is a client of a daemon that makes session s_1 and answers its prompt with refuse, or takes it and never
// ends the turn when refuse is nil.
func fakeAsk(t *testing.T, refuse *proto.Error) *core.Client {
	t.Helper()
	a, b := net.Pipe()
	t.Cleanup(func() { a.Close(); b.Close() })
	go func() {
		sc, enc := bufio.NewScanner(b), json.NewEncoder(b)
		for sc.Scan() {
			var req proto.Request
			if json.Unmarshal(sc.Bytes(), &req) != nil {
				continue
			}
			resp := proto.Response{JSONRPC: "2.0", ID: req.ID}
			var result any = struct{}{}
			switch req.Method {
			case proto.MethodSessionNew:
				result = proto.SessionNewResult{Session: "s_1"}
			case proto.MethodSessionPrompt:
				result = proto.SessionPromptResult{Seq: 2}
				resp.Error = refuse
			}
			if resp.Error == nil {
				resp.Result, _ = json.Marshal(result)
			}
			enc.Encode(resp)
		}
	}()
	return core.NewClient(a)
}

// fakeDaemon answers ask's calls on one end of a pipe. Before answering a subscribe it sends the events from the
// subscribe's from_seq on (events[seq-1] is seq); after answering the prompt it sends live events up to upTo and, when
// drop is set, closes the connection there, mid-turn, and right after answering a re-subscribe.
func fakeDaemon(t *testing.T, events []proto.Event, upTo int64, drop bool, from *int64) *core.Client {
	t.Helper()
	a, b := net.Pipe()
	t.Cleanup(func() { a.Close() })
	notify := func(enc *json.Encoder, ev proto.Event) {
		p, _ := json.Marshal(proto.SessionEventParams{Session: "s_1", Event: ev})
		enc.Encode(proto.Request{JSONRPC: "2.0", Method: proto.MethodSessionEvent, Params: p})
	}
	go func() {
		defer b.Close()
		sc, enc := bufio.NewScanner(b), json.NewEncoder(b)
		for sc.Scan() {
			var req proto.Request
			if json.Unmarshal(sc.Bytes(), &req) != nil {
				continue
			}
			var result any = struct{}{}
			switch req.Method {
			case proto.MethodSessionNew:
				result = proto.SessionNewResult{Session: "s_1"}
			case proto.MethodSessionSubscribe:
				var p proto.SessionSubscribeParams
				json.Unmarshal(req.Params, &p)
				if from != nil {
					*from = p.FromSeq
				}
				for _, ev := range events {
					if p.FromSeq > 0 && ev.Seq >= p.FromSeq {
						notify(enc, ev)
					}
				}
			case proto.MethodSessionPrompt:
				result = proto.SessionPromptResult{Seq: 1}
			}
			r, _ := json.Marshal(result)
			enc.Encode(proto.Response{JSONRPC: "2.0", ID: req.ID, Result: r})
			var sp proto.SessionSubscribeParams
			if drop && req.Method == proto.MethodSessionSubscribe && json.Unmarshal(req.Params, &sp) == nil && sp.FromSeq > 0 {
				return // a re-subscribe, dropped at once
			}
			if req.Method == proto.MethodSessionPrompt {
				for _, ev := range events {
					if ev.Seq <= upTo {
						notify(enc, ev)
					}
				}
				if drop {
					return
				}
			}
		}
	}()
	return core.NewClient(a)
}

// TestAskPicksUpAfterADrop: a connection the daemon closes mid-turn (a client that stopped reading) is replaced, the
// session subscribed again from the event after the last one heard, and ask ends with the turn's own stop reason.
func TestAskPicksUpAfterADrop(t *testing.T) {
	var evs []proto.Event
	for i, p := range []struct {
		typ     proto.EventType
		payload any
	}{
		{proto.EvUserMessage, proto.UserMessage{Content: []proto.ContentBlock{proto.Text("q")}}},
		{proto.EvAssistantMessage, proto.AssistantMessage{Request: 1, Content: []proto.ModelBlock{{Type: "text", Text: "a"}}}},
		{proto.EvTurnEnd, proto.TurnEnd{StopReason: proto.StopEndTurn}},
	} {
		ev, err := proto.NewEvent(int64(i+1), time.Unix(0, 0), p.typ, 0, p.payload)
		if err != nil {
			t.Fatal(err)
		}
		evs = append(evs, ev)
	}
	first := fakeDaemon(t, evs, 2, true, nil) // hears seq 1 and 2, then the daemon drops it
	var from int64
	redials := 0
	redial := func() (*core.Client, error) {
		redials++
		return fakeDaemon(t, evs, 0, false, &from), nil
	}
	var stdout, stderr bytes.Buffer
	code := make(chan int, 1)
	go func() {
		code <- ask(context.Background(), first, "/w", "q", proto.ModePlan, false, true, &stdout, &stderr, redial)
	}()
	select {
	case c := <-code:
		if c != exitOK || redials != 1 || from != 3 || !strings.Contains(stderr.String(), "connected to the daemon again") {
			t.Fatalf("ask = %d after %d redials, subscribed again from seq %d; stderr %q", c, redials, from, stderr.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ask did not pick the turn up after the drop")
	}
}

// TestAskGivesUpAfterItsRedials: a daemon that keeps dropping the connection ends ask with an error after askRedials.
func TestAskGivesUpAfterItsRedials(t *testing.T) {
	ev, _ := proto.NewEvent(1, time.Unix(0, 0), proto.EvUserMessage, 0, proto.UserMessage{Content: []proto.ContentBlock{proto.Text("q")}})
	evs := []proto.Event{ev}
	redials := 0
	redial := func() (*core.Client, error) {
		redials++
		return fakeDaemon(t, evs, 0, true, nil), nil // and it drops this one too
	}
	var stdout, stderr bytes.Buffer
	c := ask(context.Background(), fakeDaemon(t, evs, 1, true, nil), "/w", "q", proto.ModePlan, false, true, &stdout, &stderr, redial)
	if c != exitError || redials != askRedials {
		t.Fatalf("ask = %d after %d redials; stderr %q", c, redials, stderr.String())
	}
}

// TestAskPrintsWhatTheGapMissed: text streamed while the connection was gone is never replayed (assistant.delta is not
// persisted), so the assistant.message that follows prints the part of its text not yet printed: the answer is whole.
func TestAskPrintsWhatTheGapMissed(t *testing.T) {
	var evs []proto.Event
	for _, p := range []struct {
		seq     int64
		typ     proto.EventType
		payload any
	}{
		{1, proto.EvUserMessage, proto.UserMessage{Content: []proto.ContentBlock{proto.Text("q")}}},
		{0, proto.EvAssistantDelta, proto.AssistantDelta{Kind: "text", Text: "Hel"}}, // heard before the drop
		{2, proto.EvAssistantMessage, proto.AssistantMessage{Request: 1, Content: []proto.ModelBlock{{Type: "text", Text: "Hello, world"}}}},
		{3, proto.EvTurnEnd, proto.TurnEnd{StopReason: proto.StopEndTurn}},
	} {
		ev, err := proto.NewEvent(p.seq, time.Unix(0, 0), p.typ, 0, p.payload)
		if err != nil {
			t.Fatal(err)
		}
		evs = append(evs, ev)
	}
	first := fakeDaemon(t, evs, 1, true, nil) // hears seq 1 and the first delta, then is dropped
	redial := func() (*core.Client, error) { return fakeDaemon(t, evs, 0, false, nil), nil }
	var stdout, stderr bytes.Buffer
	if c := ask(context.Background(), first, "/w", "q", proto.ModePlan, false, true, &stdout, &stderr, redial); c != exitOK {
		t.Fatalf("ask = %d; stderr %q", c, stderr.String())
	}
	if stdout.String() != "Hello, world\n" {
		t.Errorf("stdout %q, want the whole answer once", stdout.String())
	}
}
