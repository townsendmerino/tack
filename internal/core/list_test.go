package core

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/store"
)

func listOf(t *testing.T, cl *Client, ws string) []proto.SessionInfo {
	t.Helper()
	var r proto.SessionListResult
	if err := cl.Call(proto.MethodSessionList, proto.SessionListParams{Workspace: ws}, &r); err != nil {
		t.Fatalf("session/list: %v", err)
	}
	return r.Sessions
}

func byID(ss []proto.SessionInfo, id proto.SessionID) (proto.SessionInfo, bool) {
	for _, s := range ss {
		if s.ID == id {
			return s, true
		}
	}
	return proto.SessionInfo{}, false
}

// TestSessionListThroughTheCore: session/list lists every session in the store, newest first, with its workspace,
// model, current mode, the time its last turn began and its status: running while a turn waits on a question, idle
// once it ends, interrupted for a log whose turn no daemon is running. A mode change made since the last call shows,
// the workspace filter keeps only that workspace's sessions, and the method is refused without the client token.
func TestSessionListThroughTheCore(t *testing.T) {
	f, tui, events, asking, ws, _ := askingSession(t)
	other := f.workspace(t)
	plan, err := f.sessionIn(t, other, proto.ModePlan)
	if err != nil {
		t.Fatal(err)
	}
	// A log another process left with a turn open: a session.created and a user.message, and no turn.end.
	gone := store.NewSessionID(time.Now().Add(time.Hour))
	l, err := f.core.opt.Store.Create(gone)
	if err != nil {
		t.Fatal(err)
	}
	l.Append(proto.EvSessionCreated, 0, proto.SessionCreated{Workspace: other, Model: "main", Mode: proto.ModeAsk})
	l.Append(proto.EvUserMessage, 0, proto.UserMessage{Content: []proto.ContentBlock{proto.Text("hi")}})
	l.Close()

	all := listOf(t, tui, "")
	if len(all) != 3 || all[0].ID != gone || all[1].ID != plan || all[2].ID != asking {
		t.Fatalf("all sessions, newest first: %+v", all)
	}
	if s := all[2]; s.Workspace != ws || s.Model != "main" || s.Mode != proto.ModeAuto || s.LastTurnAt != nil || s.Status != proto.StatusIdle || s.Created.IsZero() {
		t.Errorf("the asking session before a turn: %+v", s)
	}
	if s := all[0]; s.Status != proto.StatusInterrupted || s.LastTurnAt == nil || s.Mode != proto.ModeAsk {
		t.Errorf("the log with a turn open: %+v", s)
	}

	if err := tui.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: asking, Content: []proto.ContentBlock{proto.Text("add notes")}}, nil); err != nil {
		t.Fatal(err)
	}
	q := await(t, events, proto.EvPermissionRequested)
	if s, _ := byID(listOf(t, tui, ""), asking); s.Status != proto.StatusRunning || s.LastTurnAt == nil {
		t.Errorf("while its turn waits on a question: %+v", s)
	}
	v, _ := q.Decode()
	if err := tui.Call(proto.MethodPermissionRespond, proto.PermissionRespondParams{Session: asking, Seq: q.Seq, ID: v.(proto.PermissionRequested).ID,
		Decision: proto.DecisionAllow, Scope: "once"}, nil); err != nil {
		t.Fatal(err)
	}
	await(t, events, proto.EvTurnEnd)
	if s, _ := byID(listOf(t, tui, ""), asking); s.Status != proto.StatusIdle {
		t.Errorf("after its turn: %+v", s)
	}

	var mr proto.SessionSetModeResult
	if err := tui.Call(proto.MethodSessionSetMode, proto.SessionSetModeParams{Session: plan, Mode: proto.ModeAsk}, &mr); err != nil {
		t.Fatal(err)
	}
	if s, _ := byID(listOf(t, tui, ""), plan); s.Mode != mr.Mode {
		t.Errorf("after setMode to %s: %+v", mr.Mode, s)
	}

	if in := listOf(t, tui, other+"/"); len(in) != 2 || in[0].ID != gone || in[1].ID != plan {
		t.Errorf("filtered to %s: %+v", other, in)
	}
	var pe *proto.Error
	if err := f.unprivileged(t).Call(proto.MethodSessionList, proto.SessionListParams{}, nil); !errors.As(err, &pe) || pe.Code != proto.CodeUnauthorized {
		t.Errorf("without the token: %v; want unauthorized", err)
	}
}

// writeLog replaces session id's log with evs, numbered from 1, as the store writes them: one event per line.
func writeLog(t *testing.T, f *fixture, id proto.SessionID, evs ...proto.Event) {
	t.Helper()
	var b []byte
	for i, ev := range evs {
		ev.Seq = int64(i + 1)
		line, err := json.Marshal(ev)
		if err != nil {
			t.Fatal(err)
		}
		b = append(append(b, line...), '\n')
	}
	p := filepath.Join(f.core.opt.Store.Root(), "sessions", string(id), "events.jsonl")
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func ev(t *testing.T, typ proto.EventType, payload any) proto.Event {
	t.Helper()
	e, err := proto.NewEvent(0, time.Now(), typ, 0, payload)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// TestSessionListReadsTheLogs: a subagent is listed with its parent; a session the guard holds is held until it is
// released; a log that no longer matches what was read (shorter) is read again from the start; a log that cannot be
// read fails the call and leaves nothing of itself for the next; and the workspace filter matches the path a client
// knows, through a symlink, as session/new resolves it (macOS's /var is /private/var).
func TestSessionListReadsTheLogs(t *testing.T) {
	f := newFixture(t)
	ws := f.workspace(t)
	created := func(mode proto.Mode, parent proto.SessionID) proto.Event {
		return ev(t, proto.EvSessionCreated, proto.SessionCreated{Workspace: ws, Model: "main", Mode: mode, Parent: parent})
	}
	now := time.Now()
	parent, child, held, short, bad := store.NewSessionID(now), store.NewSessionID(now.Add(time.Second)),
		store.NewSessionID(now.Add(2*time.Second)), store.NewSessionID(now.Add(3*time.Second)), store.NewSessionID(now.Add(4*time.Second))
	writeLog(t, f, parent, created(proto.ModeAsk, ""))
	writeLog(t, f, child, created(proto.ModePlan, parent))
	writeLog(t, f, held, created(proto.ModeAsk, ""), ev(t, proto.EvSessionHeld, proto.SessionHeld{Commands: []string{"x"}}))
	user := ev(t, proto.EvUserMessage, proto.UserMessage{Content: []proto.ContentBlock{proto.Text("hi")}})
	end := ev(t, proto.EvTurnEnd, proto.TurnEnd{StopReason: proto.StopEndTurn})
	writeLog(t, f, short, created(proto.ModeAsk, ""), user, end)
	writeLog(t, f, bad, created(proto.ModeAsk, ""))

	all := listOf(t, f.client, "")
	if s, _ := byID(all, child); s.Parent != parent || s.Mode != proto.ModePlan {
		t.Errorf("the subagent: %+v", s)
	}
	if s, _ := byID(all, held); s.Status != proto.StatusHeld {
		t.Errorf("the held session: %+v", s)
	}
	if s, _ := byID(all, short); s.LastTurnAt == nil || s.Status != proto.StatusIdle {
		t.Errorf("before the log is cut: %+v", s)
	}

	writeLog(t, f, held, created(proto.ModeAsk, ""), ev(t, proto.EvSessionHeld, proto.SessionHeld{Commands: []string{"x"}}),
		ev(t, proto.EvSessionReleased, proto.SessionReleased{By: "user"}))
	writeLog(t, f, short, created(proto.ModeEdits, "")) // shorter than what was read: read again from the start
	all = listOf(t, f.client, "")
	if s, _ := byID(all, held); s.Status != proto.StatusIdle {
		t.Errorf("after the release: %+v", s)
	}
	if s, _ := byID(all, short); s.Mode != proto.ModeEdits || s.LastTurnAt != nil {
		t.Errorf("the cut log, read again from the start: %+v", s)
	}

	// A hold, then a line that cannot be read: the call fails, and the hold it read must not outlive it.
	unreadable := ev(t, proto.EvSessionCreated, proto.SessionCreated{})
	unreadable.Payload = json.RawMessage(`{"workspace":5}`)
	writeLog(t, f, bad, created(proto.ModeAsk, ""), ev(t, proto.EvSessionHeld, proto.SessionHeld{Commands: []string{"x"}}), unreadable)
	var r proto.SessionListResult
	if err := f.client.Call(proto.MethodSessionList, proto.SessionListParams{}, &r); err == nil {
		t.Fatalf("a log that cannot be read was listed: %+v", r.Sessions)
	}
	writeLog(t, f, bad, created(proto.ModeAsk, ""), user, end)
	if s, _ := byID(listOf(t, f.client, ""), bad); s.Status != proto.StatusIdle {
		t.Errorf("after a failed read, the log as it is now: %+v; want idle, with nothing kept from the failed read", s)
	}

	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(ws, link); err != nil {
		t.Fatal(err)
	}
	if in := listOf(t, f.client, link); len(in) != 5 {
		t.Errorf("filtered by a symlink to the workspace: %d sessions; want all 5", len(in))
	}
}

// TestListStatusAcrossTheRead: the status reads this Core's turns before and after the log is read, so a turn that ends
// or begins during the read shows running, never interrupted; only a turn the log holds open and no turn here ran
// across the read is interrupted.
func TestListStatusAcrossTheRead(t *testing.T) {
	idle, running := turnState{here: true, turn: 1}, turnState{here: true, running: true, turn: 1}
	for name, c := range map[string]struct {
		open, held    bool
		before, after turnState
		want          string
	}{
		"ended during the read":   {true, false, running, idle, proto.StatusRunning},
		"began during the read":   {false, false, idle, running, proto.StatusRunning},
		"began and ended between": {true, false, idle, turnState{here: true, turn: 2}, proto.StatusRunning},
		"open, nothing ran here":  {true, false, idle, idle, proto.StatusInterrupted},
		"another process's":       {true, false, turnState{}, turnState{}, proto.StatusInterrupted},
		"held":                    {false, true, idle, idle, proto.StatusHeld},
		"idle":                    {false, false, idle, idle, proto.StatusIdle},
	} {
		if got := listStatus(&listed{turnOpen: c.open, held: c.held}, c.before, c.after); got != c.want {
			t.Errorf("%s: %s; want %s", name, got, c.want)
		}
	}
}
