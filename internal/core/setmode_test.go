package core

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/sandbox"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
	"github.com/townsendmerino/tack/internal/store"
	"github.com/townsendmerino/tack/internal/trust"
)

// modeChanges are the session's mode.changed events in its log, in order.
func modeChanges(t *testing.T, f *fixture, id proto.SessionID) []proto.ModeChanged {
	t.Helper()
	evs, err := f.core.opt.Store.Events(id, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []proto.ModeChanged
	for _, ev := range evs {
		if v, _ := ev.Decode(); v != nil {
			if m, ok := v.(proto.ModeChanged); ok {
				out = append(out, m)
			}
		}
	}
	return out
}

func setMode(cl *Client, id proto.SessionID, m proto.Mode) (proto.SessionSetModeResult, error) {
	var r proto.SessionSetModeResult
	err := cl.Call(proto.MethodSessionSetMode, proto.SessionSetModeParams{Session: id, Mode: m, Confirmed: m == proto.ModeAuto}, &r) // auto is the confirmed move; see the test below for the unconfirmed one
	return r, err
}

func code(err error) int {
	var pe *proto.Error
	if errors.As(err, &pe) {
		return pe.Code
	}
	return 0
}

// turn prompts the session over cl and waits, on events, for the turn to end.
func turn(t *testing.T, cl *Client, events chan proto.Event, id proto.SessionID, text string) []proto.Event {
	t.Helper()
	if err := cl.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: id, Content: []proto.ContentBlock{proto.Text(text)}}, nil); err != nil {
		t.Fatal(err)
	}
	return untilTurnEnd(t, events)
}

// hasWriteTool reports whether the request's tool list, not its history, offers write.
func hasWriteTool(body []byte) bool {
	var req struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if json.Unmarshal(body, &req) != nil {
		return false
	}
	for _, t := range req.Tools {
		if t.Name == "write" {
			return true
		}
	}
	return false
}

// freshSession is a session in mode over a new workspace with the edit kit, watched by a privileged client, which it returns with
// its event channel, the fixture, the session and the workspace.
func freshSession(t *testing.T, mode proto.Mode, replies ...goinfertest.Reply) (*fixture, *Client, chan proto.Event, proto.SessionID, string) {
	t.Helper()
	f := newFixture(t, replies...)
	withKit(f)
	cl, events, _ := f.privileged(t)
	ws := gitWorkspace(t, "")
	id, err := f.sessionIn(t, ws, mode)
	if err != nil {
		t.Fatal(err)
	}
	if err := cl.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: id}, nil); err != nil {
		t.Fatal(err)
	}
	return f, cl, events, id, ws
}

func writeCallReply(id string) goinfertest.Reply {
	return goinfertest.Calls("", id, "write", `{"path":"b.go","content":"package a\n"}`)
}

// TestSetModeAmongAskEditsAutoOnlyChangesHowItDecides: from ask to edits the same session, with the same tools in its requests,
// stops asking for a write between two turns; the change is logged as mode.changed and comes back as the session's mode.
func TestSetModeAmongAskEditsAutoOnlyChangesHowItDecides(t *testing.T) {
	f, cl, events, id, ws := freshSession(t, proto.ModeAsk, writeCallReply("w1"), goinfertest.Says("one"), writeCallReply("w2"), goinfertest.Says("two"))
	// A question with a privileged watcher waits for it, so the first turn is answered denied through the protocol.
	if err := cl.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: id, Content: []proto.ContentBlock{proto.Text("write")}}, nil); err != nil {
		t.Fatal(err)
	}
	var q proto.Event
	for q.Type != proto.EvPermissionRequested {
		select {
		case q = <-events:
		case <-time.After(5 * time.Second):
			t.Fatal("no question")
		}
	}
	if err := cl.Call(proto.MethodPermissionRespond, proto.PermissionRespondParams{Session: id, ID: "w1", Seq: q.Seq, Decision: proto.DecisionDeny}, nil); err != nil {
		t.Fatal(err)
	}
	untilTurnEnd(t, events)
	if _, err := os.Stat(filepath.Join(ws, "b.go")); err == nil {
		t.Fatal("written in ask mode")
	}
	f.waitTurnFreed(t, id)
	r, err := setMode(cl, id, proto.ModeEdits)
	if err != nil || r.Mode != proto.ModeEdits {
		t.Fatalf("setMode: %+v, %v", r, err)
	}
	if got := modeChanges(t, f, id); len(got) != 1 || got[0] != (proto.ModeChanged{Mode: proto.ModeEdits, By: "user"}) {
		t.Fatalf("mode.changed %+v", got)
	}
	second := turn(t, cl, events, id, "again")
	if permission(second) != nil {
		t.Error("edits mode asked")
	}
	if _, err := os.Stat(filepath.Join(ws, "b.go")); err != nil {
		t.Error("not written in edits mode")
	}
	for i, body := range f.script.Requests {
		if !hasWriteTool(body) {
			t.Errorf("request %d lacks the write tool", i)
		}
	}
}

// TestSetModeIsRefusedWhileATurnRuns: a question waiting for the user keeps the turn running; setMode then is busy, changes
// nothing and logs nothing, and works once the turn has ended.
func TestSetModeIsRefusedWhileATurnRuns(t *testing.T) {
	f, cl, events, id, _ := freshSession(t, proto.ModeAsk, writeCallReply("w1"), goinfertest.Says("done"))
	if err := cl.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: id, Content: []proto.ContentBlock{proto.Text("write")}}, nil); err != nil {
		t.Fatal(err)
	}
	var q proto.Event
	for q.Type != proto.EvPermissionRequested {
		select {
		case q = <-events:
		case <-time.After(5 * time.Second):
			t.Fatal("no question")
		}
	}
	if _, err := setMode(cl, id, proto.ModeAuto); code(err) != proto.CodeBusy {
		t.Fatalf("while a turn runs: %v", err)
	}
	if got := modeChanges(t, f, id); len(got) != 0 {
		t.Errorf("a refused change was logged: %+v", got)
	}
	cl.Call(proto.MethodPermissionRespond, proto.PermissionRespondParams{Session: id, ID: "w1", Seq: q.Seq, Decision: proto.DecisionDeny}, nil)
	untilTurnEnd(t, events)
	f.waitTurnFreed(t, id)
	if r, err := setMode(cl, id, proto.ModeAuto); err != nil || r.Mode != proto.ModeAuto {
		t.Errorf("after the turn: %+v, %v", r, err)
	}
}

// TestSetModeToAndFromPlanRebuildsTheSession: leaving plan gives the session the write tool, going back takes it away, a watcher
// attached all along hears every turn and each mode.changed, and the conversation carries across.
func TestSetModeToAndFromPlanRebuildsTheSession(t *testing.T) {
	f, cl, events, id, ws := freshSession(t, proto.ModePlan,
		goinfertest.Says("plan answer"),
		writeCallReply("w1"), goinfertest.Says("wrote"),
		goinfertest.Calls("", "w2", "write", `{"path":"c.go","content":"package a\n"}`), goinfertest.Says("no write"))
	turn(t, cl, events, id, "think")
	f.waitTurnFreed(t, id)
	if hasWriteTool(f.script.Requests[0]) {
		t.Fatal("plan offered the write tool")
	}
	if _, err := setMode(cl, id, proto.ModeEdits); err != nil {
		t.Fatal(err)
	}
	var sawChange bool
	second := turn(t, cl, events, id, "do it")
	for _, ev := range second {
		sawChange = sawChange || ev.Type == proto.EvModeChanged
	}
	if _, err := os.Stat(filepath.Join(ws, "b.go")); err != nil {
		t.Error("the rebuilt session did not write")
	}
	if !hasWriteTool(f.script.Requests[1]) || !strings.Contains(string(f.script.Requests[1]), "plan answer") {
		t.Errorf("the rebuilt session's request lacks the write tool or the first turn's answer")
	}
	f.waitTurnFreed(t, id)
	if _, err := setMode(cl, id, proto.ModePlan); err != nil {
		t.Fatal(err)
	}
	turn(t, cl, events, id, "back to thinking")
	if _, err := os.Stat(filepath.Join(ws, "c.go")); err == nil {
		t.Error("plan wrote a file")
	}
	if hasWriteTool(f.script.Requests[len(f.script.Requests)-1]) {
		t.Error("plan offered the write tool again")
	}
	got := modeChanges(t, f, id)
	if len(got) != 2 || got[0].Mode != proto.ModeEdits || got[1].Mode != proto.ModePlan {
		t.Errorf("mode.changed %+v", got)
	}
	_ = sawChange
}

// TestSetModeIsLoweredToATrustedCeiling: a trusted repository's max_mode caps what a session can be moved to; asking for more
// than it allows gives the ceiling, and asking for what it already has changes nothing and logs nothing. An untrusted file
// caps nothing.
func TestSetModeIsLoweredToATrustedCeiling(t *testing.T) {
	for _, trusted := range []bool{true, false} {
		f := newFixture(t)
		withKit(f)
		st := trust.Open(t.TempDir())
		f.core.opt.Trust = st
		cl, _, _ := f.privileged(t)
		ws := gitWorkspace(t, "")
		os.MkdirAll(filepath.Join(ws, ".tack"), 0o755)
		os.WriteFile(filepath.Join(ws, trust.ConfigPath), []byte("[session]\nmax_mode = \"ask\"\n"), 0o644)
		if trusted {
			grantNow(ws, st)
		}
		id, err := f.sessionIn(t, ws, proto.ModePlan)
		if err != nil {
			t.Fatal(err)
		}
		r, err := setMode(cl, id, proto.ModeAuto)
		want := proto.ModeAsk
		if !trusted {
			want = proto.ModeAuto
		}
		if err != nil || r.Mode != want {
			t.Errorf("trusted=%v: setMode auto gave %+v, %v; want %s", trusted, r, err, want)
		}
		if got := modeChanges(t, f, id); len(got) != 1 || got[0].Mode != want {
			t.Errorf("trusted=%v: mode.changed %+v", trusted, got)
		}
		if r, err := setMode(cl, id, want); err != nil || r.Mode != want {
			t.Errorf("trusted=%v: setMode to the current mode: %+v, %v", trusted, r, err)
		}
		if got := modeChanges(t, f, id); len(got) != 1 {
			t.Errorf("trusted=%v: a no-op was logged: %+v", trusted, got)
		}
	}
}

// TestSetModeRefusals: no token, a value that is not a mode and an unknown session are refused and log nothing.
func TestSetModeRefusals(t *testing.T) {
	f := newFixture(t)
	withKit(f)
	cl, _, _ := f.privileged(t)
	id, err := f.sessionIn(t, gitWorkspace(t, ""), proto.ModeAsk)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := setMode(f.client, id, proto.ModeAuto); code(err) != proto.CodeUnauthorized {
		t.Errorf("unprivileged: %v", err)
	}
	for _, bad := range []proto.Mode{"", "yolo", "AUTO"} {
		if _, err := setMode(cl, id, bad); code(err) != proto.CodeInvalidParams {
			t.Errorf("mode %q: %v", bad, err)
		}
	}
	if _, err := setMode(cl, "s_nope", proto.ModeAsk); code(err) != proto.CodeUnknownSession {
		t.Errorf("unknown session: %v", err)
	}
	if got := modeChanges(t, f, id); len(got) != 0 {
		t.Errorf("logged %+v", got)
	}
}

// TestASetModeThatCannotBeAppliedIsUndone: leaving plan needs the edit kit; when it fails, the session stays in plan, the log says
// so with a compensating mode.changed by "system", and the error is reported.
func TestASetModeThatCannotBeAppliedIsUndone(t *testing.T) {
	f := newFixture(t, goinfertest.Says("still planning"))
	f.core.opt.Edit = func(string, sandbox.Target) (EditKit, error) { return EditKit{}, errors.New("no sandbox today") }
	cl, events, _ := f.privileged(t)
	id, err := f.sessionIn(t, gitWorkspace(t, ""), proto.ModePlan)
	if err != nil {
		t.Fatal(err)
	}
	cl.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: id}, nil)
	if _, err := setMode(cl, id, proto.ModeEdits); err == nil || !strings.Contains(err.Error(), "no sandbox today") {
		t.Fatalf("setMode: %v", err)
	}
	got := modeChanges(t, f, id)
	if len(got) != 2 || got[0].Mode != proto.ModeEdits || got[1] != (proto.ModeChanged{Mode: proto.ModePlan, By: "system"}) {
		t.Errorf("mode.changed %+v", got)
	}
	turn(t, cl, events, id, "think")
	if hasWriteTool(f.script.Requests[0]) {
		t.Error("the session left plan after all")
	}
}

// TestAReopenedSessionTakesItsLastModeChange: a log's last mode.changed, not session.created, is the session's mode when it is
// reopened, and so for a fork of it; a mode.changed that is not a mode refuses the reopen.
func TestAReopenedSessionTakesItsLastModeChange(t *testing.T) {
	for _, c := range []struct {
		created proto.Mode
		changes []proto.Mode
		write   bool // the reopened session can write, without asking
		asks    bool
		refused bool
	}{
		{proto.ModeAsk, []proto.Mode{proto.ModeEdits}, true, false, false},
		{proto.ModeEdits, []proto.Mode{proto.ModePlan}, false, false, false},
		{proto.ModePlan, []proto.Mode{proto.ModeAuto, proto.ModeAsk}, false, true, false}, // ask asks, and with no one to answer it is denied
		{proto.ModeEdits, []proto.Mode{"yolo"}, false, false, true},
	} {
		for _, forked := range []bool{false, true} {
			f := newFixture(t, writeCallReply("w1"), goinfertest.Says("done"))
			withKit(f)
			id, ws := loggedSession(t, f, c.created)
			log, err := f.core.opt.Store.OpenLog(id)
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range c.changes {
				log.Append(proto.EvModeChanged, 0, proto.ModeChanged{Mode: m, By: "user"})
			}
			log.Close()
			if forked {
				child := store.NewSessionID(time.Now().Add(time.Second))
				all, _ := f.core.opt.Store.Events(id, 0)
				if err := f.core.opt.Store.Fork(id, child, int64(len(all))); err != nil {
					t.Fatal(err)
				}
				id = child
			}
			err = f.client.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: id}, nil)
			if c.refused {
				if err == nil {
					t.Errorf("%v: reopened a log whose mode.changed is not a mode", c.changes)
				}
				continue
			}
			if err != nil {
				t.Fatalf("%v: %v", c.changes, err)
			}
			f.waitTurnEnd(t)
			f.prompt(t, id, "write")
			evs := f.waitTurnEnd(t)
			_, werr := os.Stat(filepath.Join(ws, "b.go"))
			if (werr == nil) != c.write || (permission(evs) != nil) != c.asks {
				t.Errorf("created %s, changes %v, forked=%v: written %v, asked %v; want written %v, asked %v",
					c.created, c.changes, forked, werr == nil, permission(evs) != nil, c.write, c.asks)
			}
		}
	}
}

// TestMovingToAutoNeedsTheOwnersConfirmation: setMode to auto without `confirmed` is refused as invalid, changes nothing and logs
// nothing, even where a ceiling would have lowered it anyway; with it the move is made. The other modes need no such word.
func TestMovingToAutoNeedsTheOwnersConfirmation(t *testing.T) {
	f := newFixture(t)
	withKit(f)
	cl, _, _ := f.privileged(t)
	id, err := f.sessionIn(t, gitWorkspace(t, ""), proto.ModeAsk)
	if err != nil {
		t.Fatal(err)
	}
	raw := func(m proto.Mode, confirmed bool) error {
		return cl.Call(proto.MethodSessionSetMode, proto.SessionSetModeParams{Session: id, Mode: m, Confirmed: confirmed}, nil)
	}
	if err := raw(proto.ModeAuto, false); code(err) != proto.CodeInvalidParams || !strings.Contains(err.Error(), "confirmation") {
		t.Fatalf("unconfirmed auto: %v", err)
	}
	if got := modeChanges(t, f, id); len(got) != 0 {
		t.Errorf("a refused move was logged: %+v", got)
	}
	for _, m := range []proto.Mode{proto.ModePlan, proto.ModeEdits, proto.ModeAsk} {
		if err := raw(m, false); err != nil {
			t.Errorf("%s without confirmation: %v", m, err)
		}
	}
	if err := raw(proto.ModeAuto, true); err != nil {
		t.Errorf("confirmed auto: %v", err)
	}
	if got := modeChanges(t, f, id); len(got) == 0 || got[len(got)-1].Mode != proto.ModeAuto {
		t.Errorf("mode.changed %+v", got)
	}
}
