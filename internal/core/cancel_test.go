package core

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/proto"
)

// TestCancelDeniesThePendingQuestionAndEndsTheTurn: session/cancel while a question waits resolves it as a deny by the
// user, reason "cancelled", the turn ends with turn.end cancelled, nothing is written, and no daemon.halted is logged
// (a cancel is not a halt).
func TestCancelDeniesThePendingQuestionAndEndsTheTurn(t *testing.T) {
	_, tui, events, sid, ws, _ := askingSession(t)
	if err := tui.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: sid, Content: []proto.ContentBlock{proto.Text("add notes")}}, nil); err != nil {
		t.Fatal(err)
	}
	await(t, events, proto.EvPermissionRequested)
	if err := tui.Call(proto.MethodSessionCancel, proto.SessionCancelParams{Session: sid}, nil); err != nil {
		t.Fatalf("session/cancel: %v", err)
	}
	var types []proto.EventType
	var resolved proto.PermissionResolved
	var end proto.TurnEnd
loop:
	for {
		select {
		case ev := <-events:
			types = append(types, ev.Type)
			switch v, _ := ev.Decode(); ev.Type {
			case proto.EvPermissionResolved:
				resolved = v.(proto.PermissionResolved)
			case proto.EvTurnEnd:
				end = v.(proto.TurnEnd)
				break loop
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("no turn.end within 5s; saw %v", types)
		}
	}
	if resolved.Decision != proto.DecisionDeny || resolved.By != proto.DecidedByUser || !strings.Contains(strings.Join(resolved.Reasons, " "), "cancelled") {
		t.Errorf("resolved %+v; want a deny by the user, reason cancelled", resolved)
	}
	if end.StopReason != proto.StopCancelled {
		t.Errorf("turn ended %q; want cancelled", end.StopReason)
	}
	for _, typ := range types {
		if typ == proto.EvDaemonHalted {
			t.Error("a cancel logged daemon.halted")
		}
	}
	if _, err := os.ReadFile(filepath.Join(ws, "AGENTS.md")); err == nil {
		t.Error("the cancelled write ran")
	}
}

// TestCancelWhenIdleIsANoOp: no turn running is not an error, so a double Esc is harmless; and the session still takes
// its next prompt.
func TestCancelWhenIdleIsANoOp(t *testing.T) {
	_, tui, events, sid, _, _ := askingSession(t)
	for range 2 {
		if err := tui.Call(proto.MethodSessionCancel, proto.SessionCancelParams{Session: sid}, nil); err != nil {
			t.Fatalf("an idle cancel: %v", err)
		}
	}
	if err := tui.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: sid, Content: []proto.ContentBlock{proto.Text("add notes")}}, nil); err != nil {
		t.Fatalf("the prompt after an idle cancel: %v", err)
	}
	await(t, events, proto.EvPermissionRequested)
}

// TestCancelNeedsTheClientTokenAndAKnownSession: a connection without the token is unauthorized, and an unknown session
// is an error, not a silent success.
func TestCancelNeedsTheClientTokenAndAKnownSession(t *testing.T) {
	f, tui, _, sid, _, _ := askingSession(t)
	var pe *proto.Error
	if err := f.client.Call(proto.MethodSessionCancel, proto.SessionCancelParams{Session: sid}, nil); !errors.As(err, &pe) || pe.Code != proto.CodeUnauthorized {
		t.Errorf("without the token: %v; want unauthorized", err)
	}
	if err := tui.Call(proto.MethodSessionCancel, proto.SessionCancelParams{Session: "s_01JAMISSING000000000000000"}, nil); err == nil {
		t.Error("an unknown session was accepted")
	}
}
