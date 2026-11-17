package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/townsendmerino/tack/internal/perms"
	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/sandbox"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
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
