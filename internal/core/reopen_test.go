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
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/loop"
	"github.com/townsendmerino/tack/internal/models"
	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
	"github.com/townsendmerino/tack/internal/store"
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
// call started, and nothing more was written. It returns the session's id and the two calls' tool.call seqs.
func crashedLog(t *testing.T, st *store.Store, ws string) proto.SessionID {
	t.Helper()
	id := store.NewSessionID(time.Now())
	log, err := st.Create(id)
	if err != nil {
		t.Fatal(err)
	}
	input := json.RawMessage(`{"pattern":"*.go"}`)
	for _, e := range []struct {
		typ     proto.EventType
		payload any
	}{
		{proto.EvSessionCreated, proto.SessionCreated{Workspace: ws, Model: "main", Mode: proto.ModePlan}},
		{proto.EvUserMessage, proto.UserMessage{Content: []proto.ContentBlock{proto.Text(loop.UserText(true, ws, "list twice"))}}},
		{proto.EvRequestSent, proto.RequestSent{Request: 1, Server: "goinfer@local", Model: "m"}},
		{proto.EvAssistantMessage, proto.AssistantMessage{Request: 1, StopReason: "tool_use", Content: []proto.ModelBlock{
			{Type: "tool_use", ID: "t1", Name: "glob", Input: input}, {Type: "tool_use", ID: "t2", Name: "glob", Input: input}}}},
		{proto.EvToolCall, proto.ToolCall{ID: "t1", Name: "glob", Input: input}},
		{proto.EvToolCall, proto.ToolCall{ID: "t2", Name: "glob", Input: input}},
		{proto.EvToolStarted, proto.ToolStarted{ID: "t1"}},
	} {
		if _, err := log.Append(e.typ, 0, e.payload); err != nil {
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
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	script := goinfertest.NewScripted("m", 16384, goinfertest.Says("both were cut off"))
	ts := httptest.NewServer(script)
	t.Cleanup(ts.Close)
	ws := repo(t)
	id := crashedLog(t, st, ws)

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
