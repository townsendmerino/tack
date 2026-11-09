package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/loop"
	"github.com/townsendmerino/tack/internal/models"
	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
	"github.com/townsendmerino/tack/internal/store"
)

// tap records every line crossing a connection as a fixture transcript line.
type tap struct {
	mu    sync.Mutex
	lines []string
}

func (t *tap) add(dir string, line []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	b, _ := json.Marshal(proto.TranscriptLine{Dir: dir, Msg: append(json.RawMessage(nil), line...)})
	t.lines = append(t.lines, string(b))
}

// tapConn wraps the client's end: writes are c2d, reads are d2c.
type tapConn struct {
	net.Conn
	t    *tap
	rbuf []byte
}

func (c *tapConn) Write(p []byte) (int, error) {
	for l := range strings.SplitSeq(strings.TrimRight(string(p), "\n"), "\n") {
		c.t.add("c2d", []byte(l))
	}
	return c.Conn.Write(p)
}

func (c *tapConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.rbuf = append(c.rbuf, p[:n]...)
	for {
		i := strings.IndexByte(string(c.rbuf), '\n')
		if i < 0 {
			break
		}
		c.t.add("d2c", c.rbuf[:i])
		c.rbuf = c.rbuf[i+1:]
	}
	return n, err
}

type fixture struct {
	core   *Core
	client *Client
	tap    *tap
	events chan proto.Event
	script *goinfertest.Scripted

	// timing: each fsync the store made, and when each event reached the client, to tell a slow disk from a stall.
	mu       sync.Mutex
	syncs    []string
	arrivals []time.Time
}

func newFixture(t *testing.T, replies ...goinfertest.Reply) *fixture {
	t.Helper()
	script := goinfertest.NewScripted("m", 16384, replies...)
	f := newFixtureOn(t, script)
	f.script = script
	return f
}

// newFixtureOn is newFixture with h as the model server.
func newFixtureOn(t *testing.T, h http.Handler) *fixture {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{tap: &tap{}, events: make(chan proto.Event, 256)}
	st.OnSync = func(typ proto.EventType, took time.Duration) {
		f.mu.Lock()
		f.syncs = append(f.syncs, fmt.Sprintf("%s %s", typ, took.Round(time.Microsecond)))
		f.mu.Unlock()
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c := New(ctx, Options{Store: st, Version: "test", Servers: []string{"goinfer@local"},
		Budget: loop.Budget{MaxRequests: 4, MaxToolCalls: 4, MaxWall: time.Minute, MaxTokens: 64, TurnTokens: 1 << 20,
			MaxErrors: 8, SessionTokens: 1 << 20, SessionToolCalls: 100},
		Source: func(string) (models.Source, error) { return models.NewRemote("local", ts.URL, "", "", nil), nil }})
	a, b := net.Pipe()
	t.Cleanup(func() { a.Close() })
	go c.Serve(ctx, b)
	f.core = c
	f.client = NewClient(&tapConn{Conn: a, t: f.tap})
	f.client.OnEvent = func(p proto.SessionEventParams) {
		f.mu.Lock()
		f.arrivals = append(f.arrivals, time.Now())
		f.mu.Unlock()
		f.events <- p.Event
	}
	if err := f.client.Call(proto.MethodInitialize, proto.InitializeParams{Client: proto.ClientInfo{Name: "t", Version: "0"}, Protocol: proto.Version}, nil); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fixture) session(t *testing.T) proto.SessionID {
	t.Helper()
	var r proto.SessionNewResult
	if err := f.client.Call(proto.MethodSessionNew, proto.SessionNewParams{Workspace: f.workspace(t), Model: "main", Mode: proto.ModePlan}, &r); err != nil {
		t.Fatal(err)
	}
	return r.Session
}

func (f *fixture) workspace(t *testing.T) string {
	d := filepath.Join(t.TempDir(), "repo")
	os.MkdirAll(d, 0o755)
	os.WriteFile(filepath.Join(d, "a.go"), []byte("package a\n"), 0o644)
	return d
}

func (f *fixture) waitTurnEnd(t *testing.T) []proto.Event {
	t.Helper()
	var got []proto.Event
	for {
		select {
		case ev := <-f.events:
			got = append(got, ev)
			if ev.Type == proto.EvTurnEnd {
				return got
			}
		case <-time.After(5 * time.Second):
			// A turn against the scripted server takes milliseconds, and its fsyncs measured 5–36 ms on macOS CI, so 5 s
			// between events is a stall. Every goroutine's stack shows where. A macOS CI run once got only the first
			// event: no fsync happens between it and request.sent, only Source.Ensure's first HTTP request.
			buf := make([]byte, 1<<20)
			t.Fatalf("no event within 5s of the last; got %d events: %v\n%s", len(got), types(got), buf[:runtime.Stack(buf, true)])
		}
	}
}

// TestTrafficConformsToTheProtocol runs a real turn through the core and holds every line that crossed the pipe to
// the same checks as the golden fixtures (proto.CheckTranscript).
func TestTrafficConformsToTheProtocol(t *testing.T) {
	f := newFixture(t, goinfertest.Calls("", "t1", "glob", `{"pattern":"*.go"}`), goinfertest.Says("a.go"))
	id := f.session(t)
	if err := f.client.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: id}, nil); err != nil {
		t.Fatal(err)
	}
	var pr proto.SessionPromptResult
	if err := f.client.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: id, Content: []proto.ContentBlock{proto.Text("files?")}}, &pr); err != nil {
		t.Fatal(err)
	}
	evs := f.waitTurnEnd(t)
	f.logTiming(t)
	if pr.Seq != 2 || evs[0].Type != proto.EvSessionCreated || evs[1].Type != proto.EvUserMessage {
		t.Fatalf("prompt seq %d, events %v; want session.created, then the prompt's user.message at seq 2", pr.Seq, types(evs))
	}
	time.Sleep(20 * time.Millisecond) // let the tap see the last line
	f.tap.mu.Lock()
	n := len(f.tap.lines)
	transcript := strings.Join(f.tap.lines, "\n")
	f.tap.mu.Unlock()
	if n < 15 {
		t.Fatalf("the tap saw %d lines; a turn with a tool call crosses more than 15", n)
	}
	for _, e := range proto.CheckTranscript([]byte(transcript), nil) {
		t.Error(e)
	}
}

// TestSubscribeReplaysOnceThenStreams subscribes after a turn has run: the persisted events arrive once, in order,
// and live events of the next turn follow without gaps or repeats.
func TestSubscribeReplaysOnceThenStreams(t *testing.T) {
	f := newFixture(t, goinfertest.Says("one"), goinfertest.Says("two"))
	id := f.session(t)
	if err := f.client.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: id, Content: []proto.ContentBlock{proto.Text("1")}}, nil); err != nil {
		t.Fatal(err)
	}
	// Not subscribed yet: wait for the turn to finish.
	deadline := time.Now().Add(5 * time.Second)
	for {
		f.core.mu.Lock()
		s := f.core.sessions[id]
		f.core.mu.Unlock()
		s.mu.Lock()
		running := s.running
		s.mu.Unlock()
		if !running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first turn did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	var sub proto.SessionSubscribeResult
	if err := f.client.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: id, FromSeq: 0}, &sub); err != nil {
		t.Fatal(err)
	}
	first := f.waitTurnEnd(t)
	if int64(len(first)) != sub.Head || first[0].Seq != 1 {
		t.Fatalf("replayed %d events, head %d", len(first), sub.Head)
	}
	if err := f.client.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: id, Content: []proto.ContentBlock{proto.Text("2")}}, nil); err != nil {
		t.Fatal(err)
	}
	second := f.waitTurnEnd(t)
	last := sub.Head
	for _, ev := range second {
		if ev.Seq == 0 {
			continue
		}
		if ev.Seq != last+1 {
			t.Fatalf("live seq %d after %d", ev.Seq, last)
		}
		last = ev.Seq
	}
}

func TestErrors(t *testing.T) {
	f := newFixture(t)
	var pe *proto.Error
	err := f.client.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: "s_nope", Content: []proto.ContentBlock{proto.Text("x")}}, nil)
	if !errors.As(err, &pe) || pe.Code != proto.CodeUnknownSession {
		t.Fatalf("unknown session: %v", err)
	}
	err = f.client.Call("session/fork", map[string]any{}, nil)
	if !errors.As(err, &pe) || pe.Code != proto.CodeMethodNotFound {
		t.Fatalf("unknown method: %v", err)
	}
	err = f.client.Call(proto.MethodSessionNew, map[string]any{"workspace": "/", "model": "main", "mode": "ask", "colour": 1}, nil)
	if !errors.As(err, &pe) || pe.Code != proto.CodeInvalidParams {
		t.Fatalf("unknown param field: %v", err)
	}
	err = f.client.Call(proto.MethodInitialize, proto.InitializeParams{Protocol: proto.Version + 2}, nil)
	if !errors.As(err, &pe) || pe.Code != proto.CodeInvalidParams {
		t.Fatalf("future protocol: %v", err)
	}
}

func TestSecondPromptWhileRunningIsBusy(t *testing.T) {
	f := newFixture(t, goinfertest.Says("done"))
	id := f.session(t)
	f.core.mu.Lock()
	s := f.core.sessions[id]
	f.core.mu.Unlock()
	s.mu.Lock()
	s.running = true // as if a turn were running
	s.mu.Unlock()
	var pe *proto.Error
	err := f.client.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: id, Content: []proto.ContentBlock{proto.Text("x")}}, nil)
	if !errors.As(err, &pe) || pe.Code != proto.CodeBusy {
		t.Fatalf("second prompt: %v", err)
	}
}

func types(evs []proto.Event) []proto.EventType {
	out := make([]proto.EventType, len(evs))
	for i, ev := range evs {
		out[i] = ev.Type
	}
	return out
}

// logTiming reports each fsync's duration and the gap between the turn's first two events, on stderr so it streams
// under -v and is visible in CI even when the test passes (the macOS job runs this test with -v).
func (f *fixture) logTiming(t *testing.T) {
	f.mu.Lock()
	defer f.mu.Unlock()
	gap := time.Duration(0)
	if len(f.arrivals) >= 2 {
		gap = f.arrivals[1].Sub(f.arrivals[0])
	}
	fmt.Fprintf(os.Stderr, "%s: %d events; first-to-second event gap %s; fsyncs: %s\n", t.Name(), len(f.arrivals),
		gap.Round(time.Microsecond), strings.Join(f.syncs, ", "))
}
