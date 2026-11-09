package core

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/loop"
	"github.com/townsendmerino/tack/internal/models"
	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
	"github.com/townsendmerino/tack/internal/store"
	"github.com/townsendmerino/tack/internal/window"
)

// daemonOn is one daemon's Core over st, its model the scripted server at url, with one initialized client whose
// events arrive on the returned channel. A second daemonOn over the same store is the next daemon.
func daemonOn(t *testing.T, st *store.Store, url string, set ...func(*Options)) (*Core, *Client, chan proto.Event) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	opt := Options{Store: st, Version: "test",
		Budget: loop.Budget{MaxRequests: 4, MaxToolCalls: 4, MaxWall: time.Minute, MaxTokens: 64, TurnTokens: 1 << 20,
			MaxErrors: 8, SessionTokens: 1 << 20, SessionToolCalls: 100},
		Source: func(string) (models.Source, error) { return models.NewRemote("local", url, "", "", nil), nil }}
	for _, f := range set {
		f(&opt)
	}
	c := New(ctx, opt)
	t.Cleanup(func() { c.Close() })
	a, b := net.Pipe()
	t.Cleanup(func() { a.Close() })
	go c.Serve(ctx, b)
	cl := NewClient(a)
	events := make(chan proto.Event, 256)
	cl.OnEvent = func(p proto.SessionEventParams) { events <- p.Event }
	if err := cl.Call(proto.MethodInitialize, proto.InitializeParams{Client: proto.ClientInfo{Name: "t", Version: "0"}, Protocol: proto.Version}, nil); err != nil {
		t.Fatal(err)
	}
	return c, cl, events
}

func repo(t *testing.T) string {
	t.Helper()
	d := filepath.Join(t.TempDir(), "repo")
	os.MkdirAll(d, 0o755)
	os.WriteFile(filepath.Join(d, "a.go"), []byte("package a\n"), 0o644)
	r, _ := filepath.EvalSymlinks(d)
	return r
}

// untilTurnEnd collects events until a turn.end.
func untilTurnEnd(t *testing.T, events chan proto.Event) []proto.Event {
	t.Helper()
	var got []proto.Event
	for {
		select {
		case ev := <-events:
			got = append(got, ev)
			if ev.Type == proto.EvTurnEnd {
				return got
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("no turn.end; got %v", types(got))
		}
	}
}

// TestASessionOutlivesItsDaemon: a session made by one daemon is subscribed to and prompted on the next one, over the
// same store. The replay begins with session.created, and the next turn's request carries the first turn's exchange,
// so the model continues the conversation (and goinfer its cached prefix). The instructions joined the first prompt,
// which the log holds, so the next daemon does not add them to the next one.
func TestASessionOutlivesItsDaemon(t *testing.T) {
	whole := func(o *Options) { o.WholeInstructions = true }
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	script := goinfertest.NewScripted("m", 16384, goinfertest.Says("the first answer"), goinfertest.Says("the second answer"))
	ts := httptest.NewServer(script)
	t.Cleanup(ts.Close)

	first, cl, events := daemonOn(t, st, ts.URL, whole)
	ws := repo(t)
	os.WriteFile(filepath.Join(ws, "CLAUDE.md"), []byte("Rule: stage explicit paths.\n"), 0o644)
	var sess proto.SessionNewResult
	if err := cl.Call(proto.MethodSessionNew, proto.SessionNewParams{Workspace: ws, Model: "main", Mode: proto.ModePlan}, &sess); err != nil {
		t.Fatal(err)
	}
	if err := cl.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: sess.Session}, nil); err != nil {
		t.Fatal(err)
	}
	if err := cl.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: sess.Session, Content: []proto.ContentBlock{proto.Text("one")}}, nil); err != nil {
		t.Fatal(err)
	}
	untilTurnEnd(t, events)
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	_, cl2, events2 := daemonOn(t, st, ts.URL, whole)
	var sub proto.SessionSubscribeResult
	if err := cl2.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: sess.Session}, &sub); err != nil {
		t.Fatalf("subscribing on the next daemon: %v", err)
	}
	replay := untilTurnEnd(t, events2)
	if int64(len(replay)) != sub.Head || replay[0].Type != proto.EvSessionCreated {
		t.Fatalf("replay %v with head %d; want the whole log, from session.created", types(replay), sub.Head)
	}
	if v, _ := replay[0].Decode(); v.(proto.SessionCreated) != (proto.SessionCreated{Workspace: ws, Model: "main", Mode: proto.ModePlan}) {
		t.Fatalf("session.created = %+v", v)
	}
	var pr proto.SessionPromptResult
	if err := cl2.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: sess.Session, Content: []proto.ContentBlock{proto.Text("two")}}, &pr); err != nil {
		t.Fatalf("prompting on the next daemon: %v", err)
	}
	if pr.Seq != sub.Head+1 {
		t.Fatalf("the next prompt is seq %d, want %d: the log continues", pr.Seq, sub.Head+1)
	}
	second := untilTurnEnd(t, events2)
	if v, _ := second[0].Decode(); v.(proto.UserMessage).Content[0].Text != "two" {
		t.Fatalf("the next daemon's first prompt was sent as %+v; want just \"two\", the instructions already in the log", v)
	}
	if !strings.Contains(string(replay[1].Payload), "stage explicit paths") {
		t.Fatalf("the first prompt lacks the instructions, so this test cannot see them repeated: %s", replay[1].Payload)
	}
	if n := len(script.Requests); n != 2 || !strings.Contains(string(script.Requests[1]), "the first answer") {
		t.Fatalf("%d requests; the second daemon's should carry the first turn's answer", n)
	}
}

// crashedLog writes a session's log as a crash during a tool would leave it: the model called glob twice, the first
// call started, and nothing more was written. With causes, the events carry them as the live loop writes them (a
// request answers its user.message, a response its request, a tool.call its response, tool.started its tool.call);
// without, none, as a log from before causes. It returns the session's id.
func crashedLog(t *testing.T, st *store.Store, ws string, causes bool) proto.SessionID {
	t.Helper()
	id := store.NewSessionID(time.Now())
	log, err := st.Create(id)
	if err != nil {
		t.Fatal(err)
	}
	c := func(seq int64) int64 {
		if causes {
			return seq
		}
		return 0
	}
	input := json.RawMessage(`{"pattern":"*.go"}`)
	for _, e := range []struct {
		typ     proto.EventType
		cause   int64
		payload any
	}{
		{proto.EvSessionCreated, 0, proto.SessionCreated{Workspace: ws, Model: "main", Mode: proto.ModePlan}},                         // 1
		{proto.EvUserMessage, 0, proto.UserMessage{Content: []proto.ContentBlock{proto.Text(loop.UserText(true, ws, "list twice"))}}}, // 2
		{proto.EvRequestSent, c(2), proto.RequestSent{Request: 1, Server: "goinfer@local", Model: "m"}},                               // 3
		{proto.EvAssistantMessage, c(3), proto.AssistantMessage{Request: 1, StopReason: "tool_use", Content: []proto.ModelBlock{ // 4
			{Type: "tool_use", ID: "t1", Name: "glob", Input: input}, {Type: "tool_use", ID: "t2", Name: "glob", Input: input}}}},
		{proto.EvToolCall, c(4), proto.ToolCall{ID: "t1", Name: "glob", Input: input}}, // 5
		{proto.EvToolCall, c(4), proto.ToolCall{ID: "t2", Name: "glob", Input: input}}, // 6
		{proto.EvToolStarted, c(5), proto.ToolStarted{ID: "t1"}},                       // 7
	} {
		if _, err := log.Append(e.typ, e.cause, e.payload); err != nil {
			t.Fatal(err)
		}
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	return id
}

// TestRecoverClosesAnInterruptedTurn: at start, a session a crash left inside a turn gets a result for each call
// without one (outcome unknown for the call that had started, not run for the other) and a turn.end saying it was
// interrupted (DESIGN §6.1). The next prompt sends the model those results and runs neither call again.
func TestRecoverClosesAnInterruptedTurn(t *testing.T) {
	t.Run("with causes, as the loop writes them", func(t *testing.T) { recoverClosesAnInterruptedTurn(t, true) })
	t.Run("without causes, as a log from before them", func(t *testing.T) { recoverClosesAnInterruptedTurn(t, false) })
}

func recoverClosesAnInterruptedTurn(t *testing.T, causes bool) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	script := goinfertest.NewScripted("m", 16384, goinfertest.Says("both were cut off"))
	ts := httptest.NewServer(script)
	t.Cleanup(ts.Close)
	ws := repo(t)
	id := crashedLog(t, st, ws, causes)

	c, cl, events := daemonOn(t, st, ts.URL)
	got, err := c.Recover()
	if err != nil || !slices.Equal(got, []proto.SessionID{id}) {
		t.Fatalf("Recover = %v, %v; want [%s]", got, err, id)
	}
	evs, _ := st.Events(id, 0)
	tail := evs[len(evs)-3:]
	want := []proto.ToolFinished{{ID: "t1", Outcome: proto.OutcomeUnknown, Result: unknownResult},
		{ID: "t2", Outcome: proto.OutcomeCancelled, Result: notRunResult}}
	for i, w := range want {
		v, _ := tail[i].Decode()
		if fin, ok := v.(proto.ToolFinished); !ok || fin.ID != w.ID || fin.Outcome != w.Outcome || fin.Result != w.Result || tail[i].Cause != int64(5+i) {
			t.Errorf("recovery event %d: %+v (cause %d); want %+v answering seq %d", i, v, tail[i].Cause, w, 5+i)
		}
	}
	if v, _ := tail[2].Decode(); v != (proto.TurnEnd{StopReason: proto.StopInterrupted}) || tail[2].Cause != 2 {
		t.Errorf("recovery's last event: %+v (cause %d); want turn.end interrupted answering the user message", v, tail[2].Cause)
	}
	if again, err := c.Recover(); err != nil || len(again) != 0 {
		t.Errorf("a second Recover = %v, %v; want nothing to do", again, err)
	}

	if err := cl.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: id, FromSeq: int64(len(evs) + 1)}, nil); err != nil {
		t.Fatal(err)
	}
	if err := cl.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: id, Content: []proto.ContentBlock{proto.Text("what happened?")}}, nil); err != nil {
		t.Fatal(err)
	}
	for _, ev := range untilTurnEnd(t, events) {
		if ev.Type == proto.EvToolStarted || ev.Type == proto.EvToolCall {
			t.Errorf("after recovery the turn ran a tool: %s", ev.Payload)
		}
	}
	req := string(script.Requests[0])
	for _, s := range []string{`"tool_use_id":"t1"`, "outcome unknown", `"tool_use_id":"t2"`, "not run", "what happened?"} {
		if !strings.Contains(req, s) {
			t.Errorf("the request after recovery lacks %q: %s", s, req)
		}
	}
}

// TestRecoverLeavesALiveSessionAlone: a session whose log another process has open is mid-turn, not interrupted.
// Recover leaves it as it is, and the daemon refuses to reopen it.
func TestRecoverLeavesALiveSessionAlone(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ws := repo(t)
	id := store.NewSessionID(time.Now())
	live, err := st.Create(id) // as a running `tack run` holds it
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	live.Append(proto.EvSessionCreated, 0, proto.SessionCreated{Workspace: ws, Model: "main", Mode: proto.ModePlan})
	live.Append(proto.EvUserMessage, 0, proto.UserMessage{Content: []proto.ContentBlock{proto.Text("busy")}})

	c, cl, _ := daemonOn(t, st, "http://127.0.0.1:1")
	if got, err := c.Recover(); err != nil || len(got) != 0 {
		t.Fatalf("Recover = %v, %v; want the live session left alone", got, err)
	}
	if evs, _ := st.Events(id, 0); len(evs) != 2 {
		t.Fatalf("the live session's log has %d events, want its 2", len(evs))
	}
	var pe *proto.Error
	if err := cl.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: id}, nil); !errors.As(err, &pe) || pe.Code != proto.CodeBusy {
		t.Fatalf("subscribing to a session open elsewhere: %v, want code %d", err, proto.CodeBusy)
	}
}

// TestReopenRefuses: what the daemon cannot reopen is refused with a reason: an id that is not one (it never becomes
// a path), and a log from before session.created.
func TestReopenRefuses(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	old := store.NewSessionID(time.Now())
	log, _ := st.Create(old)
	log.Append(proto.EvUserMessage, 0, proto.UserMessage{Content: []proto.ContentBlock{proto.Text("from P0")}})
	log.Close()
	_, cl, _ := daemonOn(t, st, "http://127.0.0.1:1")
	var pe *proto.Error
	for id, code := range map[proto.SessionID]int{"s_../../etc": proto.CodeUnknownSession, "s_NOSUCH": proto.CodeUnknownSession,
		old: proto.CodeInvalidParams} {
		err := cl.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: id}, nil)
		if !errors.As(err, &pe) || pe.Code != code {
			t.Errorf("subscribe %q: %v, want code %d", id, err, code)
		}
	}
	if err := cl.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: old}, nil); err == nil || !strings.Contains(err.Error(), "session.created") {
		t.Errorf("subscribing to a P0 log: %v, want the reason", err)
	}
}

// TestRecoverAnswersAReusedIDOnce: a server may reuse a tool-call id across a turn's steps. The call that finished in
// the first step keeps its result; only the second, cut off by the crash, gets one, answering its own tool.call.
func TestRecoverAnswersAReusedIDOnce(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ws := repo(t)
	id := store.NewSessionID(time.Now())
	log, _ := st.Create(id)
	in := json.RawMessage(`{"pattern":"*.go"}`)
	use := []proto.ModelBlock{{Type: "tool_use", ID: "t1", Name: "glob", Input: in}}
	var secondCall int64
	for i, e := range []struct {
		typ     proto.EventType
		cause   int64
		payload any
	}{
		{proto.EvSessionCreated, 0, proto.SessionCreated{Workspace: ws, Model: "main", Mode: proto.ModePlan}},
		{proto.EvUserMessage, 0, proto.UserMessage{Content: []proto.ContentBlock{proto.Text("twice")}}},
		{proto.EvAssistantMessage, 0, proto.AssistantMessage{Request: 1, StopReason: "tool_use", Content: use}},
		{proto.EvToolCall, 0, proto.ToolCall{ID: "t1", Name: "glob", Input: in}}, // seq 4
		{proto.EvToolStarted, 4, proto.ToolStarted{ID: "t1"}},                    // seq 5
		{proto.EvToolFinished, 4, proto.ToolFinished{ID: "t1", Outcome: proto.OutcomeOK, Result: "a.go"}},
		{proto.EvAssistantMessage, 0, proto.AssistantMessage{Request: 2, StopReason: "tool_use", Content: use}},
		{proto.EvToolCall, 0, proto.ToolCall{ID: "t1", Name: "glob", Input: in}}, // seq 8
		{proto.EvToolStarted, 8, proto.ToolStarted{ID: "t1"}},
	} {
		ev, err := log.Append(e.typ, e.cause, e.payload)
		if err != nil {
			t.Fatal(err)
		}
		if i == 7 {
			secondCall = ev.Seq
		}
	}
	log.Close()
	c, _, _ := daemonOn(t, st, "http://127.0.0.1:1")
	if _, err := c.Recover(); err != nil {
		t.Fatal(err)
	}
	evs, _ := st.Events(id, 0)
	var fins []proto.Event
	for _, ev := range evs[9:] {
		if ev.Type == proto.EvToolFinished {
			fins = append(fins, ev)
		}
	}
	if len(fins) != 1 || fins[0].Cause != secondCall {
		t.Fatalf("recovery answered %d calls (%v); want one, the second t1 (seq %d)", len(fins), fins, secondCall)
	}
}

// TestAFailedSessionLeavesNoLog: a session/new that fails after its log was made (here: auto mode with no sandbox)
// leaves no session behind in the store.
func TestAFailedSessionLeavesNoLog(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, cl, _ := daemonOn(t, st, "http://127.0.0.1:1")
	if err := cl.Call(proto.MethodSessionNew, proto.SessionNewParams{Workspace: repo(t), Model: "main", Mode: proto.ModeAuto}, nil); err == nil {
		t.Fatal("auto mode without a sandbox made a session")
	}
	if ids, err := st.Sessions(); err != nil || len(ids) != 0 {
		t.Fatalf("the store holds %v (%v) after the failed session/new; want nothing", ids, err)
	}
}

// TestRecoverRecordsCallsTheCrashCutOffBeforeTheirToolCall: the model's response and its tool.call events are separate
// appends, so a crash can land between them. Recovery records the missing tool.call from the response's tool_use
// block, then answers it as not run, so the turn closes whole and the session reopens.
func TestRecoverRecordsCallsTheCrashCutOffBeforeTheirToolCall(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ws := repo(t)
	id := store.NewSessionID(time.Now())
	log, _ := st.Create(id)
	in := json.RawMessage(`{"pattern":"*.go"}`)
	log.Append(proto.EvSessionCreated, 0, proto.SessionCreated{Workspace: ws, Model: "main", Mode: proto.ModePlan})
	log.Append(proto.EvUserMessage, 0, proto.UserMessage{Content: []proto.ContentBlock{proto.Text(loop.UserText(true, ws, "two"))}})
	msg, _ := log.Append(proto.EvAssistantMessage, 0, proto.AssistantMessage{Request: 1, StopReason: "tool_use", Content: []proto.ModelBlock{
		{Type: "tool_use", ID: "t1", Name: "glob", Input: in}, {Type: "tool_use", ID: "t2", Name: "glob", Input: in}}})
	log.Append(proto.EvToolCall, msg.Seq, proto.ToolCall{ID: "t1", Name: "glob", Input: in}) // the crash: t2's never written
	log.Close()
	script := goinfertest.NewScripted("m", 16384, goinfertest.Says("recovered"))
	ts := httptest.NewServer(script)
	t.Cleanup(ts.Close)
	c, cl, events := daemonOn(t, st, ts.URL)
	if _, err := c.Recover(); err != nil {
		t.Fatal(err)
	}
	evs, _ := st.Events(id, 0)
	var calls, fins []string
	for _, ev := range evs {
		switch v, _ := ev.Decode(); p := v.(type) {
		case proto.ToolCall:
			calls = append(calls, p.ID)
		case proto.ToolFinished:
			fins = append(fins, p.ID+" "+p.Outcome)
		}
	}
	if strings.Join(calls, ",") != "t1,t2" || strings.Join(fins, ",") != "t1 cancelled,t2 cancelled" {
		t.Fatalf("after recovery: calls %v, results %v; want t2's call recorded and both answered not run", calls, fins)
	}
	if err := cl.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: id, FromSeq: int64(len(evs) + 1)}, nil); err != nil {
		t.Fatalf("reopening the recovered session: %v", err)
	}
	if err := cl.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: id, Content: []proto.ContentBlock{proto.Text("go on")}}, nil); err != nil {
		t.Fatal(err)
	}
	untilTurnEnd(t, events)
}

// TestRecoverLeavesALogFromBeforeTheDaemonAlone: a log without session.created, which reopen refuses, is not
// written to either.
func TestRecoverLeavesALogFromBeforeTheDaemonAlone(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id := store.NewSessionID(time.Now())
	log, _ := st.Create(id)
	log.Append(proto.EvUserMessage, 0, proto.UserMessage{Content: []proto.ContentBlock{proto.Text("from P0, cut off")}})
	log.Close()
	c, _, _ := daemonOn(t, st, "http://127.0.0.1:1")
	if got, err := c.Recover(); err != nil || len(got) != 0 {
		t.Fatalf("Recover = %v, %v; want the P0 log left alone", got, err)
	}
	if evs, _ := st.Events(id, 0); len(evs) != 1 {
		t.Fatalf("the P0 log has %d events, want its 1", len(evs))
	}
}

// TestCloseIsFinal: Close closes each session's log and gives up its hold, a running turn's too; a closed Core
// reopens nothing, so neither can be taken again behind it; closing it again is a no-op.
func TestCloseIsFinal(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ws := repo(t)
	ts := httptest.NewServer(stuckModel())
	t.Cleanup(ts.Close)
	c, cl, _ := daemonOn(t, st, ts.URL)
	var sess proto.SessionNewResult
	if err := cl.Call(proto.MethodSessionNew, proto.SessionNewParams{Workspace: ws, Model: "main", Mode: proto.ModePlan}, &sess); err != nil {
		t.Fatal(err)
	}
	// A running turn holds the log's lock, so the check below sees Close's own work, not a log idle already.
	if err := cl.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: sess.Session, Content: []proto.ContentBlock{proto.Text("x")}}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := st.OpenLog(sess.Session); !errors.Is(err, store.ErrLogBusy) {
		t.Fatalf("the log while its turn runs: %v, want ErrLogBusy", err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("a second Close: %v", err)
	}
	var pe *proto.Error
	if err := cl.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: sess.Session}, nil); !errors.As(err, &pe) || pe.Code != proto.CodeShuttingDown {
		t.Fatalf("subscribe on a closed Core: %v, want code %d", err, proto.CodeShuttingDown)
	}
	if err := cl.Call(proto.MethodSessionNew, proto.SessionNewParams{Workspace: ws, Model: "main", Mode: proto.ModePlan}, nil); !errors.As(err, &pe) || pe.Code != proto.CodeShuttingDown {
		t.Fatalf("session/new on a closed Core: %v, want code %d", err, proto.CodeShuttingDown)
	}
	l, err := st.OpenLog(sess.Session)
	if err != nil {
		t.Fatalf("the session's log after Close: %v, want it free", err)
	}
	l.Close()
	h, err := st.Hold(sess.Session)
	if err != nil {
		t.Fatalf("the session's hold after Close: %v, want it free", err)
	}
	h.Release()
}

// TestAnIdleSessionsLogIsFree: between turns the daemon holds no lock on a session's log, so tack rewind, another
// process, can write it (here it appends its rewind event); the next turn takes the lock back and continues the log
// past what rewind wrote.
func TestAnIdleSessionsLogIsFree(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	script := goinfertest.NewScripted("m", 16384, goinfertest.Says("one"), goinfertest.Says("two"))
	ts := httptest.NewServer(script)
	t.Cleanup(ts.Close)
	_, cl, events := daemonOn(t, st, ts.URL)
	ws := repo(t)
	var sess proto.SessionNewResult
	if err := cl.Call(proto.MethodSessionNew, proto.SessionNewParams{Workspace: ws, Model: "main", Mode: proto.ModePlan}, &sess); err != nil {
		t.Fatal(err)
	}
	if err := cl.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: sess.Session}, nil); err != nil {
		t.Fatal(err)
	}
	if err := cl.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: sess.Session, Content: []proto.ContentBlock{proto.Text("one")}}, nil); err != nil {
		t.Fatal(err)
	}
	untilTurnEnd(t, events)
	// The client hears turn.end as the loop records it; the core lets the log go as the loop returns, just after.
	var rw *store.Log
	deadline := time.Now().Add(2 * time.Second)
	for rw, err = st.OpenLog(sess.Session); errors.Is(err, store.ErrLogBusy) && time.Now().Before(deadline); rw, err = st.OpenLog(sess.Session) {
		time.Sleep(5 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("the idle session's log: %v, want it free for tack rewind", err)
	}
	ev, err := rw.Append(proto.EvRewind, 1, proto.Rewind{To: 1, Workspace: ws})
	if err != nil {
		t.Fatal(err)
	}
	rw.Close()
	var pr proto.SessionPromptResult
	if err := cl.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: sess.Session, Content: []proto.ContentBlock{proto.Text("two")}}, &pr); err != nil {
		t.Fatal(err)
	}
	if pr.Seq != ev.Seq+1 {
		t.Fatalf("the next prompt is seq %d; want %d, after rewind's", pr.Seq, ev.Seq+1)
	}
	// The subscriber hears what rewind wrote, in order, before the turn's own events.
	var order []proto.EventType
	for _, e := range untilTurnEnd(t, events) {
		order = append(order, e.Type)
	}
	if len(order) < 2 || order[0] != proto.EvRewind || order[1] != proto.EvUserMessage {
		t.Fatalf("the subscriber heard %v; want rewind's event, then the next prompt's", order)
	}
	if _, err := st.Events(sess.Session, 0); err != nil {
		t.Fatalf("the log after both writers: %v", err)
	}
}

// closingSource counts how often it is closed.
type closingSource struct {
	*models.Remote
	closes *atomic.Int32
}

func (s closingSource) Close() error { s.closes.Add(1); return nil }

// TestCloseWaitsForASessionBeingMade: a Close that comes while session/new is making a session waits for it, so the
// source, ken and log the new session opened are closed with the rest, not left open behind a closed Core.
func TestCloseWaitsForASessionBeingMade(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var opened, closes atomic.Int32
	c, cl, _ := daemonOn(t, st, "http://127.0.0.1:1", func(o *Options) {
		o.Source = func(string) (models.Source, error) {
			opened.Add(1)
			return closingSource{models.NewRemote("local", "http://127.0.0.1:1", "", "", nil), &closes}, nil
		}
	})
	closed := make(chan error, 1)
	madeHook = func() { // session/new has passed its closed check; Close comes now
		go func() { closed <- c.Close() }()
		select {
		case <-closed:
			closed <- nil // Close did not wait
		case <-time.After(200 * time.Millisecond):
		}
	}
	defer func() { madeHook = nil }()
	var sess proto.SessionNewResult
	if err := cl.Call(proto.MethodSessionNew, proto.SessionNewParams{Workspace: repo(t), Model: "main", Mode: proto.ModePlan}, &sess); err != nil {
		t.Fatalf("session/new, past its closed check when Close came: %v", err)
	}
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	if o, cs := opened.Load(), closes.Load(); o != 1 || cs != 1 {
		t.Fatalf("%d sources opened and %d closed; want the new session's one, closed by Close", o, cs)
	}
	h, err := st.Hold(sess.Session)
	if err != nil {
		t.Fatalf("the new session's hold after Close: %v, want it given up with the rest", err)
	}
	h.Release()
}

// TestRecoverKeepsADuplicateToolUse: a response may carry two tool_use blocks with one id; recording the first's
// tool.call accounts for that block alone, so the second, cut off by the crash, is still recorded and answered.
func TestRecoverKeepsADuplicateToolUse(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ws := repo(t)
	id := store.NewSessionID(time.Now())
	log, _ := st.Create(id)
	in := json.RawMessage(`{"pattern":"*.go"}`)
	log.Append(proto.EvSessionCreated, 0, proto.SessionCreated{Workspace: ws, Model: "main", Mode: proto.ModePlan})
	log.Append(proto.EvUserMessage, 0, proto.UserMessage{Content: []proto.ContentBlock{proto.Text("twice")}})
	msg, _ := log.Append(proto.EvAssistantMessage, 0, proto.AssistantMessage{Request: 1, StopReason: "tool_use", Content: []proto.ModelBlock{
		{Type: "tool_use", ID: "t1", Name: "glob", Input: in}, {Type: "tool_use", ID: "t1", Name: "glob", Input: in}}})
	log.Append(proto.EvToolCall, msg.Seq, proto.ToolCall{ID: "t1", Name: "glob", Input: in})
	log.Close()
	c, _, _ := daemonOn(t, st, "http://127.0.0.1:1")
	if _, err := c.Recover(); err != nil {
		t.Fatal(err)
	}
	evs, _ := st.Events(id, 0)
	calls, fins := 0, 0
	for _, ev := range evs {
		switch ev.Type {
		case proto.EvToolCall:
			calls++
		case proto.EvToolFinished:
			fins++
		}
	}
	if calls != 2 || fins != 2 {
		t.Fatalf("after recovery: %d tool.calls and %d results; want both blocks recorded and answered", calls, fins)
	}
}

// TestARewoundSessionKeepsItsFirstTurn: rewind (P1 step 3) restores files and records a rewind event; it leaves the
// conversation whole, so a session reopened after it still holds its first message, instructions and all, and is
// rightly prompted: the next turn is a later one, and nothing is added to it again.
func TestARewoundSessionKeepsItsFirstTurn(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	script := goinfertest.NewScripted("m", 16384, goinfertest.Says("one"), goinfertest.Says("two"))
	ts := httptest.NewServer(script)
	t.Cleanup(ts.Close)
	whole := func(o *Options) { o.WholeInstructions = true }
	first, cl, events := daemonOn(t, st, ts.URL, whole)
	ws := repo(t)
	os.WriteFile(filepath.Join(ws, "CLAUDE.md"), []byte("Rule: stage explicit paths.\n"), 0o644)
	var sess proto.SessionNewResult
	cl.Call(proto.MethodSessionNew, proto.SessionNewParams{Workspace: ws, Model: "main", Mode: proto.ModePlan}, &sess)
	cl.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: sess.Session}, nil)
	cl.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: sess.Session, Content: []proto.ContentBlock{proto.Text("one")}}, nil)
	untilTurnEnd(t, events)
	first.Close()
	rw, err := st.OpenLog(sess.Session)
	if err != nil {
		t.Fatal(err)
	}
	rw.Append(proto.EvRewind, 2, proto.Rewind{To: 2, Workspace: ws}) // back to before the first turn's changes
	rw.Close()
	_, cl2, events2 := daemonOn(t, st, ts.URL, whole)
	cl2.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: sess.Session}, nil)
	untilTurnEnd(t, events2) // the replay
	cl2.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: sess.Session, Content: []proto.ContentBlock{proto.Text("two")}}, nil)
	var prompt string
	for _, ev := range untilTurnEnd(t, events2) { // the replayed rewind event comes first
		if ev.Type == proto.EvUserMessage {
			v, _ := ev.Decode()
			prompt = v.(proto.UserMessage).Content[0].Text
		}
	}
	if prompt != "two" {
		t.Fatalf("the next prompt was sent as %q; want the bare text of a later turn", prompt)
	}
	if !strings.Contains(string(script.Requests[1]), "stage explicit paths") {
		t.Fatal("after the rewind the conversation lacks its first message")
	}
}

// TestAReopenedSessionAddsNoFirstPromptContext: the first prompt's injection and repository map are not made again for
// a reopened session that was prompted already: its next prompt runs, though building either would fail.
func TestAReopenedSessionAddsNoFirstPromptContext(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	script := goinfertest.NewScripted("m", 16384, goinfertest.Says("one"), goinfertest.Says("two"))
	ts := httptest.NewServer(script)
	t.Cleanup(ts.Close)
	first, cl, events := daemonOn(t, st, ts.URL)
	var sess proto.SessionNewResult
	cl.Call(proto.MethodSessionNew, proto.SessionNewParams{Workspace: repo(t), Model: "main", Mode: proto.ModePlan}, &sess)
	cl.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: sess.Session}, nil)
	cl.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: sess.Session, Content: []proto.ContentBlock{proto.Text("one")}}, nil)
	untilTurnEnd(t, events)
	first.Close()
	refuse := errors.New("not again")
	c2, cl2, events2 := daemonOn(t, st, ts.URL, func(o *Options) {
		o.InjectContext, o.RepoMap = true, true
		o.OpenInject = func(context.Context, string) (window.InjectIndex, error) { return nil, refuse }
		o.OpenRepoMap = func(context.Context, string) (window.MapIndex, error) { return nil, refuse }
	})
	if err := cl2.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: sess.Session}, nil); err != nil {
		t.Fatal(err)
	}
	untilTurnEnd(t, events2) // the replayed first turn
	// The reopen sets neither up (open's guard), and the next prompt builds neither (prompt's): each is checked.
	c2.mu.Lock()
	s := c2.sessions[sess.Session]
	c2.mu.Unlock()
	if s.injectRoot != "" || s.mapRoot != "" {
		t.Fatalf("a reopened, prompted session would add injection %q and map %q to its next prompt", s.injectRoot, s.mapRoot)
	}
	if err := cl2.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: sess.Session, Content: []proto.ContentBlock{proto.Text("two")}}, nil); err != nil {
		t.Fatalf("the reopened session's next prompt: %v; it built the first prompt's injection or map again", err)
	}
	untilTurnEnd(t, events2)
}

// TestCrashedLogWritesCausesWhenAsked: the helper the recovery tests run both ways does write causes when asked, so
// the two ways are two paths.
func TestCrashedLogWritesCausesWhenAsked(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ws := repo(t)
	for _, causes := range []bool{true, false} {
		evs, _ := st.Events(crashedLog(t, st, ws, causes), 0)
		if got := evs[len(evs)-1].Cause; (got == 5) != causes {
			t.Errorf("causes %v: tool.started's cause is %d", causes, got)
		}
	}
}

// connect is another initialized client of c, with its own event channel.
func connect(t *testing.T, c *Core) (*Client, chan proto.Event) {
	t.Helper()
	a, b := net.Pipe()
	t.Cleanup(func() { a.Close() })
	go c.Serve(c.ctx, b)
	cl := NewClient(a)
	events := make(chan proto.Event, 256)
	cl.OnEvent = func(p proto.SessionEventParams) { events <- p.Event }
	if err := cl.Call(proto.MethodInitialize, proto.InitializeParams{Client: proto.ClientInfo{Name: "t", Version: "0"}, Protocol: proto.Version}, nil); err != nil {
		t.Fatal(err)
	}
	return cl, events
}

// rewindWhileIdle appends a rewind event to the session's idle log as tack rewind does, once the core has let it go.
func rewindWhileIdle(t *testing.T, st *store.Store, id proto.SessionID, ws string) proto.Event {
	t.Helper()
	var rw *store.Log
	var err error
	deadline := time.Now().Add(2 * time.Second)
	for rw, err = st.OpenLog(id); errors.Is(err, store.ErrLogBusy) && time.Now().Before(deadline); rw, err = st.OpenLog(id) {
		time.Sleep(5 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("the idle session's log: %v", err)
	}
	defer rw.Close()
	ev, err := rw.Append(proto.EvRewind, 1, proto.Rewind{To: 1, Workspace: ws})
	if err != nil {
		t.Fatal(err)
	}
	return ev
}

// TestAnEventWrittenWhileIdleReachesEachSubscriberOnce: what tack rewind wrote reaches a subscriber from before the
// write live, one from after it by replay alone, and one subscribed from past it not at all.
func TestAnEventWrittenWhileIdleReachesEachSubscriberOnce(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	script := goinfertest.NewScripted("m", 16384, goinfertest.Says("one"), goinfertest.Says("two"))
	ts := httptest.NewServer(script)
	t.Cleanup(ts.Close)
	c, before, beforeEvs := daemonOn(t, st, ts.URL)
	ws := repo(t)
	var sess proto.SessionNewResult
	if err := before.Call(proto.MethodSessionNew, proto.SessionNewParams{Workspace: ws, Model: "main", Mode: proto.ModePlan}, &sess); err != nil {
		t.Fatal(err)
	}
	before.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: sess.Session}, nil)
	before.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: sess.Session, Content: []proto.ContentBlock{proto.Text("one")}}, nil)
	untilTurnEnd(t, beforeEvs)
	rw := rewindWhileIdle(t, st, sess.Session, ws)
	after, afterEvs := connect(t, c)
	if err := after.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: sess.Session}, nil); err != nil {
		t.Fatal(err)
	}
	past, pastEvs := connect(t, c)
	if err := past.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: sess.Session, FromSeq: rw.Seq + 1}, nil); err != nil {
		t.Fatal(err)
	}
	if err := before.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: sess.Session, Content: []proto.ContentBlock{proto.Text("two")}}, nil); err != nil {
		t.Fatal(err)
	}
	rewinds := func(evs []proto.Event) int {
		n := 0
		for _, e := range evs {
			if e.Type == proto.EvRewind {
				n++
			}
		}
		return n
	}
	afterGot := untilTurnEnd(t, afterEvs) // the replayed first turn
	afterGot = append(afterGot, untilTurnEnd(t, afterEvs)...)
	for _, w := range []struct {
		name string
		got  []proto.Event
		want int
	}{{"subscribed before the write", untilTurnEnd(t, beforeEvs), 1}, {"subscribed after it", afterGot, 1},
		{"subscribed from past it", untilTurnEnd(t, pastEvs), 0}} {
		if n := rewinds(w.got); n != w.want {
			t.Errorf("%s: heard rewind's event %d times, want %d (%v)", w.name, n, w.want, types(w.got))
		}
	}
}

// TestAFirstPromptRefusedKeepsItsAdditions: a first prompt refused because another process is writing the log adds
// nothing to it, so the one after still carries the first prompt's additions (here the project instructions).
func TestAFirstPromptRefusedKeepsItsAdditions(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	script := goinfertest.NewScripted("m", 16384, goinfertest.Says("one"))
	ts := httptest.NewServer(script)
	t.Cleanup(ts.Close)
	_, cl, events := daemonOn(t, st, ts.URL, func(o *Options) { o.WholeInstructions = true })
	ws := repo(t)
	if err := os.WriteFile(filepath.Join(ws, "AGENTS.md"), []byte("Answer in French."), 0o644); err != nil {
		t.Fatal(err)
	}
	var sess proto.SessionNewResult
	if err := cl.Call(proto.MethodSessionNew, proto.SessionNewParams{Workspace: ws, Model: "main", Mode: proto.ModePlan}, &sess); err != nil {
		t.Fatal(err)
	}
	cl.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: sess.Session}, nil)
	rw, err := st.OpenLog(sess.Session) // tack rewind, writing between turns
	if err != nil {
		t.Fatal(err)
	}
	var pe *proto.Error
	if err := cl.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: sess.Session, Content: []proto.ContentBlock{proto.Text("one")}}, nil); !errors.As(err, &pe) || pe.Code != proto.CodeBusy {
		t.Fatalf("a prompt while another process writes the log: %v, want code %d", err, proto.CodeBusy)
	}
	rw.Close()
	if err := cl.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: sess.Session, Content: []proto.ContentBlock{proto.Text("one")}}, nil); err != nil {
		t.Fatal(err)
	}
	for _, ev := range untilTurnEnd(t, events) {
		if ev.Type == proto.EvUserMessage && !strings.Contains(string(ev.Payload), "Answer in French.") {
			t.Fatalf("the first prompt that ran lost the project instructions: %s", ev.Payload)
		}
	}
}

// TestASessionAnotherCoreHoldsIsBusy: a session one Core has in memory is not reopened by another, between turns too,
// when the log itself is free; recovery passes over it; once the first Core closes, the second may reopen it.
func TestASessionAnotherCoreHoldsIsBusy(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, cl, _ := daemonOn(t, st, "http://127.0.0.1:1")
	var sess proto.SessionNewResult
	if err := cl.Call(proto.MethodSessionNew, proto.SessionNewParams{Workspace: repo(t), Model: "main", Mode: proto.ModePlan}, &sess); err != nil {
		t.Fatal(err)
	}
	// The log ends inside a turn while its Core lives (a turn.end that failed to append): the live Core's to finish,
	// not another's to close.
	rw, err := st.OpenLog(sess.Session)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rw.Append(proto.EvUserMessage, 0, proto.UserMessage{Content: []proto.ContentBlock{proto.Text("x")}}); err != nil {
		t.Fatal(err)
	}
	rw.Close()
	second, cl2, _ := daemonOn(t, st, "http://127.0.0.1:1")
	var pe *proto.Error
	if err := cl2.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: sess.Session}, nil); !errors.As(err, &pe) || pe.Code != proto.CodeBusy {
		t.Fatalf("reopening a session another Core holds: %v, want code %d", err, proto.CodeBusy)
	}
	if ids, err := second.Recover(); err != nil || len(ids) != 0 {
		t.Fatalf("recovery beside a live Core: %v, %v; want it to leave the held session alone", ids, err)
	}
	first.Close()
	if err := cl2.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: sess.Session}, nil); err != nil {
		t.Fatalf("reopening it once the first Core closed: %v", err)
	}
}

// TestRecoverOnAFreshStoreFindsNothing: a daemon's first start, before any session exists, recovers nothing and does
// not fail.
func TestRecoverOnAFreshStoreFindsNothing(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	c, _, _ := daemonOn(t, st, "http://127.0.0.1:1")
	if ids, err := c.Recover(); err != nil || len(ids) != 0 {
		t.Fatalf("Recover on a fresh store: %v, %v; want nothing", ids, err)
	}
}

// TestRecoverLocksOnlyWhatItCloses: recovery takes the hold and the log's lock only of a session whose last turn is
// open, so tack rewind, or a core reopening a finished session, is not refused while recovery reads it.
func TestRecoverLocksOnlyWhatItCloses(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ws := repo(t)
	maker, cl, _ := daemonOn(t, st, "http://127.0.0.1:1")
	var done proto.SessionNewResult
	if err := cl.Call(proto.MethodSessionNew, proto.SessionNewParams{Workspace: ws, Model: "main", Mode: proto.ModePlan}, &done); err != nil {
		t.Fatal(err)
	}
	maker.Close()
	crashed := crashedLog(t, st, ws, true)
	var locked []proto.SessionID
	recoverHook = func(id proto.SessionID) { locked = append(locked, id) }
	t.Cleanup(func() { recoverHook = nil })
	c, _, _ := daemonOn(t, st, "http://127.0.0.1:1")
	ids, err := c.Recover()
	if err != nil || !slices.Equal(ids, []proto.SessionID{crashed}) {
		t.Fatalf("Recover: %v, %v; want only %s", ids, err, crashed)
	}
	if !slices.Equal(locked, []proto.SessionID{crashed}) {
		t.Fatalf("Recover locked %v; want only %s, the session it closes (%s has no open turn)", locked, crashed, done.Session)
	}
}

// TestRecoverMatchesARepeatedIDInOrder: without causes, a tool.started or tool.finished for an id a response repeats
// answers the earliest call with that id still waiting for it, since calls run in the order they were logged; so the
// call a crash cut off mid-run is the unknown one, and the one after it was not run.
func TestRecoverMatchesARepeatedIDInOrder(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id := store.NewSessionID(time.Now())
	log, _ := st.Create(id)
	in := json.RawMessage(`{"pattern":"*.go"}`)
	log.Append(proto.EvSessionCreated, 0, proto.SessionCreated{Workspace: repo(t), Model: "main", Mode: proto.ModePlan})
	log.Append(proto.EvUserMessage, 0, proto.UserMessage{Content: []proto.ContentBlock{proto.Text("twice")}})
	msg, _ := log.Append(proto.EvAssistantMessage, 0, proto.AssistantMessage{Request: 1, StopReason: "tool_use", Content: []proto.ModelBlock{
		{Type: "tool_use", ID: "t1", Name: "glob", Input: in}, {Type: "tool_use", ID: "t1", Name: "glob", Input: in}}})
	first, _ := log.Append(proto.EvToolCall, msg.Seq, proto.ToolCall{ID: "t1", Name: "glob", Input: in})
	second, _ := log.Append(proto.EvToolCall, msg.Seq, proto.ToolCall{ID: "t1", Name: "glob", Input: in})
	log.Append(proto.EvToolStarted, 0, proto.ToolStarted{ID: "t1"}) // the first call's; the crash comes as it runs
	log.Close()
	c, _, _ := daemonOn(t, st, "http://127.0.0.1:1")
	if _, err := c.Recover(); err != nil {
		t.Fatal(err)
	}
	evs, _ := st.Events(id, 0)
	outcome := map[int64]string{}
	for _, ev := range evs {
		if ev.Type == proto.EvToolFinished {
			v, _ := ev.Decode()
			outcome[ev.Cause] = v.(proto.ToolFinished).Outcome
		}
	}
	if outcome[first.Seq] != proto.OutcomeUnknown || outcome[second.Seq] != proto.OutcomeCancelled {
		t.Fatalf("results by call: %v; want the first (seq %d) unknown and the second (seq %d) not run", outcome, first.Seq, second.Seq)
	}
}
