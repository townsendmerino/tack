package core

import (
	"errors"
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
