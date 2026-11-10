package core

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/loop"
	"github.com/townsendmerino/tack/internal/perms"
	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/sandbox"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
)

// privileged is a connection to f's core that presented the client token, with its own event channel.
func (f *fixture) privileged(t *testing.T) (*Client, chan proto.Event, net.Conn) {
	t.Helper()
	f.core.opt.ClientToken = "tok"
	a, b := net.Pipe()
	t.Cleanup(func() { a.Close() })
	go f.core.Serve(f.core.ctx, b)
	cl := NewClient(a)
	events := make(chan proto.Event, 256)
	cl.OnEvent = func(p proto.SessionEventParams) { events <- p.Event }
	if err := cl.Call(proto.MethodInitialize, proto.InitializeParams{Client: proto.ClientInfo{Name: "tui", Version: "0"},
		Protocol: proto.Version, Token: "tok"}, nil); err != nil {
		t.Fatal(err)
	}
	return cl, events, a
}

// askingSession is an auto-mode session in a git workspace whose model writes AGENTS.md (an instruction file, which
// asks even in auto mode) and then says done, watched by a privileged client.
func askingSession(t *testing.T) (*fixture, *Client, chan proto.Event, proto.SessionID, string, net.Conn) {
	t.Helper()
	f := newFixture(t, goinfertest.Calls("", "w1", "write", `{"path":"AGENTS.md","content":"Be brief.\n"}`), goinfertest.Says("done"))
	f.core.opt.Edit = func(root string, _ sandbox.Target) (EditKit, error) {
		return EditKit{Shell: noShell{}, Engine: &perms.Engine{Root: root, Sandboxed: true}}, nil
	}
	ws := gitWorkspace(t, "")
	sid, err := f.sessionIn(t, ws, proto.ModeAuto)
	if err != nil {
		t.Fatal(err)
	}
	tui, events, pipe := f.privileged(t)
	if err := tui.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: sid}, nil); err != nil {
		t.Fatal(err)
	}
	return f, tui, events, sid, ws, pipe
}

// await returns the first event of type typ on events, failing after 5 s.
func await(t *testing.T, events chan proto.Event, typ proto.EventType) proto.Event {
	t.Helper()
	for {
		select {
		case ev := <-events:
			if ev.Type == typ {
				return ev
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("no %s within 5s", typ)
		}
	}
}

// TestAWatchingClientApproves: with a privileged client watching, a question waits for its answer. An allow runs the
// call, and the log says the user allowed it; a second answer to the same question is not_pending.
func TestAWatchingClientApproves(t *testing.T) {
	_, tui, events, sid, ws, _ := askingSession(t)
	if err := tui.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: sid, Content: []proto.ContentBlock{proto.Text("add notes")}}, nil); err != nil {
		t.Fatal(err)
	}
	qev := await(t, events, proto.EvPermissionRequested)
	v, _ := qev.Decode()
	q := v.(proto.PermissionRequested)
	if err := tui.Call(proto.MethodPermissionRespond, proto.PermissionRespondParams{Session: sid, Seq: qev.Seq, ID: q.ID, Decision: proto.DecisionAllow, Scope: "once"}, nil); err != nil {
		t.Fatalf("permission/respond: %v", err)
	}
	v, _ = await(t, events, proto.EvPermissionResolved).Decode()
	if r := v.(proto.PermissionResolved); r.Decision != proto.DecisionAllow || r.By != proto.DecidedByUser || r.Scope != "once" {
		t.Fatalf("resolved %+v; want allowed by the user, once", r)
	}
	await(t, events, proto.EvTurnEnd)
	if b, _ := os.ReadFile(filepath.Join(ws, "AGENTS.md")); string(b) != "Be brief.\n" {
		t.Fatalf("AGENTS.md is %q; want the approved write", b)
	}
	err := tui.Call(proto.MethodPermissionRespond, proto.PermissionRespondParams{Session: sid, Seq: qev.Seq, ID: q.ID, Decision: proto.DecisionAllow}, nil)
	var pe *proto.Error
	if !errors.As(err, &pe) || pe.Code != proto.CodeNotPending {
		t.Fatalf("a second answer: %v; want not_pending", err)
	}
}

// TestADenyWithANoteReachesTheModel: a deny's note is in the refused result the model reads, and in the log.
func TestADenyWithANoteReachesTheModel(t *testing.T) {
	f, tui, events, sid, ws, _ := askingSession(t)
	tui.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: sid, Content: []proto.ContentBlock{proto.Text("add notes")}}, nil)
	qev := await(t, events, proto.EvPermissionRequested)
	v, _ := qev.Decode()
	q := v.(proto.PermissionRequested)
	if err := tui.Call(proto.MethodPermissionRespond, proto.PermissionRespondParams{Session: sid, Seq: qev.Seq, ID: q.ID, Decision: proto.DecisionDeny,
		Note: "put it in README instead"}, nil); err != nil {
		t.Fatal(err)
	}
	v, _ = await(t, events, proto.EvPermissionResolved).Decode()
	if r := v.(proto.PermissionResolved); r.Decision != proto.DecisionDeny || r.By != proto.DecidedByUser || r.Note != "put it in README instead" {
		t.Fatalf("resolved %+v; want denied by the user, with the note", r)
	}
	await(t, events, proto.EvTurnEnd)
	if _, err := os.Stat(filepath.Join(ws, "AGENTS.md")); err == nil {
		t.Fatal("a denied write ran")
	}
	if n := len(f.script.Requests); n < 2 || !strings.Contains(string(f.script.Requests[1]), "put it in README instead") {
		t.Fatal("the model's next request does not carry the note")
	}
}

// TestRespondChecksWhatItIsGiven: permission/respond needs the client token, a known decision and scope, and refuses
// the reserved rule field until it is built.
func TestRespondChecksWhatItIsGiven(t *testing.T) {
	f, tui, _, sid, _, _ := askingSession(t)
	code := func(cl *Client, p proto.PermissionRespondParams) int {
		var pe *proto.Error
		if err := cl.Call(proto.MethodPermissionRespond, p, nil); errors.As(err, &pe) {
			return pe.Code
		}
		return 0
	}
	if c := code(f.client, proto.PermissionRespondParams{Session: sid, Seq: 6, ID: "w1", Decision: proto.DecisionAllow}); c != proto.CodeUnauthorized {
		t.Errorf("without the token: code %d; want unauthorized", c)
	}
	for name, p := range map[string]proto.PermissionRespondParams{
		"a decision of maybe": {Session: sid, ID: "w1", Decision: "maybe"},
		"a scope of forever":  {Session: sid, ID: "w1", Decision: proto.DecisionAllow, Scope: "forever"},
		"an edited rule":      {Session: sid, ID: "w1", Decision: proto.DecisionAllow, Rule: "git *"},
	} {
		if c := code(tui, p); c != proto.CodeInvalidParams {
			t.Errorf("%s: code %d; want invalid params", name, c)
		}
	}
	if c := code(tui, proto.PermissionRespondParams{Session: sid, Seq: 6, ID: "nothing-asked", Decision: proto.DecisionAllow}); c != proto.CodeNotPending {
		t.Errorf("an answer to no question: code %d; want not_pending", c)
	}
}

// TestNoWatcherMeansNoOneToAsk: with no privileged client subscribed, a question is denied at once as no_user, as
// before; the turn never waits on nobody.
func TestNoWatcherMeansNoOneToAsk(t *testing.T) {
	f := newFixture(t, goinfertest.Calls("", "w1", "write", `{"path":"AGENTS.md","content":"x\n"}`), goinfertest.Says("done"))
	f.core.opt.Edit = func(root string, _ sandbox.Target) (EditKit, error) {
		return EditKit{Shell: noShell{}, Engine: &perms.Engine{Root: root, Sandboxed: true}}, nil
	}
	sid, err := f.sessionIn(t, gitWorkspace(t, ""), proto.ModeAuto)
	if err != nil {
		t.Fatal(err)
	}
	f.client.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: sid}, nil) // unprivileged
	f.prompt(t, sid, "add notes")
	for _, ev := range f.waitTurnEnd(t) {
		if v, _ := ev.Decode(); ev.Type == proto.EvPermissionResolved && v.(proto.PermissionResolved).By != proto.DecidedByNoUser {
			t.Fatalf("resolved %+v; want no_user", v)
		}
	}
}

// TestTheLastWatcherLeavingEndsTheQuestion: a question waits while a privileged client watches; when it goes, the
// question is denied as no_user and the turn goes on, rather than wait for an answer no one can give.
func TestTheLastWatcherLeavingEndsTheQuestion(t *testing.T) {
	f, tui, events, sid, _, pipe := askingSession(t)
	f.client.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: sid}, nil) // the unprivileged one sees the end
	tui.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: sid, Content: []proto.ContentBlock{proto.Text("add notes")}}, nil)
	await(t, events, proto.EvPermissionRequested)
	pipe.Close()
	evs := f.waitTurnEnd(t)
	found := false
	for _, ev := range evs {
		if v, _ := ev.Decode(); ev.Type == proto.EvPermissionResolved {
			found = v.(proto.PermissionResolved).By == proto.DecidedByNoUser
		}
	}
	if !found {
		t.Fatal("the question did not end as no_user when its last watcher left")
	}
}

// TestAnAnswerSettlesOnlyItsOwnQuestion: the loop asks again under the same tool call id when the reasons change after
// a yes (permit's snapshot re-ask), so a question is named by its permission.requested seq. A second client's late
// allow, meant for the first question, must not settle the re-ask, whose reasons nobody who answered has seen.
func TestAnAnswerSettlesOnlyItsOwnQuestion(t *testing.T) {
	f, _, _, sid, _, _ := askingSession(t)
	other, _, _ := f.privileged(t) // a second TUI, a step behind
	s, perr := f.core.session(sid)
	if perr != nil {
		t.Fatal(perr)
	}
	ask := func(seq int64, reasons ...string) (proto.PermissionRequested, chan loop.Approval) {
		req := proto.PermissionRequested{ID: "b1", Tool: "bash", Mode: proto.ModeAuto, Command: "make", Reasons: reasons}
		ev, err := proto.NewEvent(seq, time.Now(), proto.EvPermissionRequested, 5, req)
		if err != nil {
			t.Fatal(err)
		}
		s.emit(ev)
		got := make(chan loop.Approval, 1)
		go func() { a, _ := (approver{s}).Approve(context.Background(), req); got <- a }()
		return req, got
	}
	respond := func(cl *Client, seq int64, decision string) error {
		return cl.Call(proto.MethodPermissionRespond, proto.PermissionRespondParams{Session: sid, Seq: seq, ID: "b1", Decision: decision}, nil)
	}
	_, first := ask(6, "reason A")
	tui, _, _ := f.privileged(t)
	if err := respond(tui, 6, proto.DecisionAllow); err != nil {
		t.Fatal(err)
	}
	if a := <-first; !a.Allow {
		t.Fatal("the first question was not allowed")
	}
	_, second := ask(9, "reason A", "reason B") // the re-ask, under the same id
	var pe *proto.Error
	if err := respond(other, 6, proto.DecisionAllow); !errors.As(err, &pe) || pe.Code != proto.CodeNotPending {
		t.Fatalf("a late allow for the first question, given to the re-ask: %v; want not_pending", err)
	}
	if err := respond(tui, 9, proto.DecisionDeny); err != nil {
		t.Fatal(err)
	}
	if a := <-second; a.Allow {
		t.Fatal("the re-ask was allowed; it was denied")
	}
}

// TestAnIdReusedInALaterTurnIsAnotherQuestion: model-chosen ids repeat across turns ("w1" again), and an answer naming
// the first turn's question by its seq does not settle the second's.
func TestAnIdReusedInALaterTurnIsAnotherQuestion(t *testing.T) {
	f := newFixture(t,
		goinfertest.Calls("", "w1", "write", `{"path":"AGENTS.md","content":"one\n"}`), goinfertest.Says("first"),
		goinfertest.Calls("", "w1", "write", `{"path":"AGENTS.md","content":"two\n"}`), goinfertest.Says("second"))
	f.core.opt.Edit = func(root string, _ sandbox.Target) (EditKit, error) {
		return EditKit{Shell: noShell{}, Engine: &perms.Engine{Root: root, Sandboxed: true}}, nil
	}
	ws := gitWorkspace(t, "")
	sid, err := f.sessionIn(t, ws, proto.ModeAuto)
	if err != nil {
		t.Fatal(err)
	}
	tui, events, _ := f.privileged(t)
	tui.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: sid}, nil)
	prompt := func(text string) proto.Event {
		tui.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: sid, Content: []proto.ContentBlock{proto.Text(text)}}, nil)
		return await(t, events, proto.EvPermissionRequested)
	}
	q1 := prompt("one")
	tui.Call(proto.MethodPermissionRespond, proto.PermissionRespondParams{Session: sid, Seq: q1.Seq, ID: "w1", Decision: proto.DecisionDeny}, nil)
	await(t, events, proto.EvTurnEnd)
	q2 := prompt("two")
	var pe *proto.Error
	err = tui.Call(proto.MethodPermissionRespond, proto.PermissionRespondParams{Session: sid, Seq: q1.Seq, ID: "w1", Decision: proto.DecisionAllow}, nil)
	if !errors.As(err, &pe) || pe.Code != proto.CodeNotPending {
		t.Fatalf("an allow naming the first turn's question, in the second: %v; want not_pending", err)
	}
	if err := tui.Call(proto.MethodPermissionRespond, proto.PermissionRespondParams{Session: sid, Seq: q2.Seq, ID: "w1", Decision: proto.DecisionDeny}, nil); err != nil {
		t.Fatal(err)
	}
	await(t, events, proto.EvTurnEnd)
	if _, err := os.Stat(filepath.Join(ws, "AGENTS.md")); err == nil {
		t.Fatal("a write no one allowed ran")
	}
}

// TestTwoAnswersAtOnceOneWins: two clients answering one question at the same moment: one answer settles it, and the
// other is not_pending.
func TestTwoAnswersAtOnceOneWins(t *testing.T) {
	f, tui, events, sid, _, _ := askingSession(t)
	other, _, _ := f.privileged(t)
	tui.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: sid, Content: []proto.ContentBlock{proto.Text("add notes")}}, nil)
	qev := await(t, events, proto.EvPermissionRequested)
	errs := make(chan error, 2)
	for _, cl := range []*Client{tui, other} {
		go func() {
			errs <- cl.Call(proto.MethodPermissionRespond, proto.PermissionRespondParams{Session: sid, Seq: qev.Seq, ID: "w1", Decision: proto.DecisionDeny}, nil)
		}()
	}
	ok, notPending := 0, 0
	for range 2 {
		var pe *proto.Error
		switch err := <-errs; {
		case err == nil:
			ok++
		case errors.As(err, &pe) && pe.Code == proto.CodeNotPending:
			notPending++
		default:
			t.Fatal(err)
		}
	}
	if ok != 1 || notPending != 1 {
		t.Fatalf("%d answers settled it and %d were not_pending; want 1 and 1", ok, notPending)
	}
}

// TestAHaltEndsAWaitingQuestionAsHalted: a halt while a question waits ends it, and the log says a halt ended it, not
// that no one was there to answer.
func TestAHaltEndsAWaitingQuestionAsHalted(t *testing.T) {
	f, tui, events, sid, _, _ := askingSession(t)
	tui.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: sid, Content: []proto.ContentBlock{proto.Text("add notes")}}, nil)
	await(t, events, proto.EvPermissionRequested)
	f.core.Halt(proto.DaemonHalted{Trigger: proto.HaltByCommand, By: "test"})
	v, _ := await(t, events, proto.EvPermissionResolved).Decode()
	if r := v.(proto.PermissionResolved); r.By != proto.DecidedByHalted || r.Decision != proto.DecisionDeny {
		t.Fatalf("resolved %+v; want denied, by a halt", r)
	}
}
