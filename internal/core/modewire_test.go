package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/perms"
	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/sandbox"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
	"github.com/townsendmerino/tack/internal/store"
	"github.com/townsendmerino/tack/internal/trust"
)

// writeInMode runs a turn in which the model writes b.go, in a session started in mode with an edit kit, and returns whether the
// file exists afterwards, the events, and the session's request bodies.
func writeInMode(t *testing.T, mode proto.Mode) (written bool, evs []proto.Event, requests []string) {
	t.Helper()
	f := newFixture(t, goinfertest.Calls("", "w1", "write", `{"path":"b.go","content":"package a\n"}`), goinfertest.Says("done"))
	f.core.opt.Edit = func(root string, _ sandbox.Target) (EditKit, error) {
		return EditKit{Shell: noShell{}, Engine: &perms.Engine{Root: root, Sandboxed: true}}, nil
	}
	ws := gitWorkspace(t, "")
	sid, err := f.sessionIn(t, ws, mode)
	if err != nil {
		t.Fatal(err)
	}
	f.client.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: sid}, nil)
	f.prompt(t, sid, "write b.go")
	evs = f.waitTurnEnd(t)
	_, err = os.Stat(filepath.Join(ws, "b.go"))
	for _, b := range f.script.Requests {
		requests = append(requests, string(b))
	}
	return err == nil, evs, requests
}

func permission(evs []proto.Event) *proto.PermissionRequested {
	for _, ev := range evs {
		if v, _ := ev.Decode(); v != nil {
			if p, ok := v.(proto.PermissionRequested); ok {
				return &p
			}
		}
	}
	return nil
}

// TestEveryModeButPlanGetsTheEditToolsAndTheEngine: a session in edits mode writes a file without asking, one in ask mode asks
// (and with no privileged client to answer, the question is denied and nothing is written), one in auto writes, and one in plan has
// no write tool at all. Before, ask and edits got the read-only tools and no engine, so they were plan.
func TestEveryModeButPlanGetsTheEditToolsAndTheEngine(t *testing.T) {
	for _, c := range []struct {
		mode    proto.Mode
		written bool
		asks    bool
	}{
		{proto.ModeAuto, true, false},
		{proto.ModeEdits, true, false},
		{proto.ModeAsk, false, true},
		{proto.ModePlan, false, false},
	} {
		written, evs, reqs := writeInMode(t, c.mode)
		q := permission(evs)
		if written != c.written || (q != nil) != c.asks {
			t.Errorf("%s: written %v, asked %v; want written %v, asked %v", c.mode, written, q != nil, c.written, c.asks)
		}
		if q != nil && (q.Tool != "write" || q.Mode != proto.ModeAsk) {
			t.Errorf("%s: question %+v", c.mode, q)
		}
		hasWrite := len(reqs) > 0 && strings.Contains(reqs[0], `"name":"write"`)
		if hasWrite != (c.mode != proto.ModePlan) {
			t.Errorf("%s: write tool offered %v", c.mode, hasWrite)
		}
	}
}

// TestAskAndEditsNeedTheSandboxToo: with no edit kit, asking for ask or edits is refused as auto is, rather than starting a session
// that silently cannot change anything.
func TestAskAndEditsNeedTheSandboxToo(t *testing.T) {
	for _, m := range []proto.Mode{proto.ModeAsk, proto.ModeEdits} {
		f := newFixture(t)
		if _, err := f.sessionIn(t, gitWorkspace(t, ""), m); err == nil {
			t.Errorf("%s without an edit kit started", m)
		}
	}
}

// loggedSession writes a session's log holding only session.created in mode, then a finished turn, as an earlier daemon would
// have left it, and returns its id and workspace.
func loggedSession(t *testing.T, f *fixture, mode proto.Mode) (proto.SessionID, string) {
	t.Helper()
	ws := gitWorkspace(t, "")
	id := store.NewSessionID(time.Now())
	log, err := f.core.opt.Store.Create(id)
	if err != nil {
		t.Fatal(err)
	}
	log.Append(proto.EvSessionCreated, 0, proto.SessionCreated{Workspace: ws, Model: "main", Mode: mode})
	log.Append(proto.EvUserMessage, 0, proto.UserMessage{Content: []proto.ContentBlock{proto.Text("earlier")}})
	log.Append(proto.EvTurnEnd, 0, proto.TurnEnd{StopReason: "end_turn"})
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	return id, ws
}

func withKit(f *fixture) {
	f.core.opt.Edit = func(root string, _ sandbox.Target) (EditKit, error) {
		return EditKit{Shell: noShell{}, Engine: &perms.Engine{Root: root, Sandboxed: true}}, nil
	}
}

// TestASessionLogWithAModeThatIsNotOneIsNotReopened: an empty or unknown mode in session.created is refused when the session is
// reopened, with no write tools and no turn; it is not read as a mode that lets the session change files.
func TestASessionLogWithAModeThatIsNotOneIsNotReopened(t *testing.T) {
	for _, bad := range []proto.Mode{"", "yolo", "AUTO"} {
		f := newFixture(t, goinfertest.Calls("", "w1", "write", `{"path":"b.go","content":"package a\n"}`), goinfertest.Says("done"))
		withKit(f)
		id, ws := loggedSession(t, f, bad)
		if err := f.client.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: id}, nil); err == nil {
			t.Errorf("mode %q: the session reopened", bad)
		}
		if err := f.client.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: id, Content: []proto.ContentBlock{proto.Text("x")}}, nil); err == nil {
			t.Errorf("mode %q: the session took a prompt", bad)
		}
		if _, err := os.Stat(filepath.Join(ws, "b.go")); err == nil {
			t.Errorf("mode %q: a file was written", bad)
		}
		if len(f.script.Requests) != 0 {
			t.Errorf("mode %q: %d requests went out", bad, len(f.script.Requests))
		}
	}
}

// TestAReopenedSessionKeepsItsMode: a session whose log says ask reopens in ask (its write asks), one that says plan reopens in
// plan (no write tool), as do their forks, which copy the log and so the mode.
func TestAReopenedSessionKeepsItsMode(t *testing.T) {
	for _, mode := range []proto.Mode{proto.ModeAsk, proto.ModePlan} {
		for _, forked := range []bool{false, true} {
			f := newFixture(t, goinfertest.Calls("", "w1", "write", `{"path":"b.go","content":"package a\n"}`), goinfertest.Says("done"))
			withKit(f)
			id, ws := loggedSession(t, f, mode)
			if forked {
				child := store.NewSessionID(time.Now().Add(time.Second))
				if err := f.core.opt.Store.Fork(id, child, 3); err != nil {
					t.Fatal(err)
				}
				id = child
			}
			if err := f.client.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: id}, nil); err != nil {
				t.Fatalf("%s forked=%v: %v", mode, forked, err)
			}
			f.waitTurnEnd(t) // the replay of the earlier turn, which ends the log so far
			f.prompt(t, id, "write b.go")
			evs := f.waitTurnEnd(t)
			_, werr := os.Stat(filepath.Join(ws, "b.go"))
			q := permission(evs)
			hasWrite := len(f.script.Requests) > 0 && strings.Contains(string(f.script.Requests[0]), `"name":"write"`)
			if werr == nil || hasWrite != (mode == proto.ModeAsk) || (q != nil) != (mode == proto.ModeAsk) {
				t.Errorf("%s forked=%v: written %v, write tool %v, asked %v", mode, forked, werr == nil, hasWrite, q != nil)
			}
		}
	}
}

// TestACeilingSetAfterASessionWasMadeStillHoldsWhenItReopens: a session recorded in ask, in a repository the owner trusts later with
// max_mode = "plan", reopens in plan: no write tool, no question.
func TestACeilingSetAfterASessionWasMadeStillHoldsWhenItReopens(t *testing.T) {
	f := newFixture(t, goinfertest.Calls("", "w1", "write", `{"path":"b.go","content":"package a\n"}`), goinfertest.Says("done"))
	withKit(f)
	st := trust.Open(t.TempDir())
	f.core.opt.Trust = st
	id, ws := loggedSession(t, f, proto.ModeAsk)
	os.MkdirAll(filepath.Join(ws, ".tack"), 0o755)
	os.WriteFile(filepath.Join(ws, trust.ConfigPath), []byte("[session]\nmax_mode = \"plan\"\n"), 0o644)
	grantNow(ws, st)
	if err := f.client.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: id}, nil); err != nil {
		t.Fatal(err)
	}
	f.waitTurnEnd(t)
	f.prompt(t, id, "write b.go")
	evs := f.waitTurnEnd(t)
	if permission(evs) != nil || strings.Contains(string(f.script.Requests[0]), `"name":"write"`) {
		t.Error("the session reopened above the ceiling")
	}
	if _, err := os.Stat(filepath.Join(ws, "b.go")); err == nil {
		t.Error("a file was written")
	}
}
