package loop

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
)

// TestResumeContinuesWhereTheLogEnds: a session reopened from its log (a daemon that did not run its first turn)
// holds the same conversation as the live one, counts the first turn toward its session budgets, and sends its next
// turn as a later turn, not a first one.
func TestResumeContinuesWhereTheLogEnds(t *testing.T) {
	h := newHarness(t, Budget{}, goinfertest.Calls("", "t1", "glob", `{"pattern":"*.go"}`), goinfertest.Says("first answer"))
	if stop, err := h.sess.Turn(context.Background(), "list"); err != nil || stop != proto.StopEndTurn {
		t.Fatalf("first turn: %q, %v", stop, err)
	}
	evs, err := h.st.Events("s_test", 0)
	if err != nil {
		t.Fatal(err)
	}
	cfg := h.sess.cfg
	cfg.Budget.SessionToolCalls = 1 // the first turn's glob used it up
	r, err := Resume(cfg, evs)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := r.Conversation().Messages(), h.sess.Conversation().Messages(); !reflect.DeepEqual(got, want) {
		t.Fatalf("resumed conversation:\n%+v\nlive:\n%+v", got, want)
	}
	if r.requests != h.sess.requests || r.tokens != h.sess.tokens || r.calls != h.sess.calls || r.turns != h.sess.turns {
		t.Fatalf("resumed counts requests %d tokens %d calls %d turns %d; live %d %d %d %d", r.requests, r.tokens, r.calls,
			r.turns, h.sess.requests, h.sess.tokens, h.sess.calls, h.sess.turns)
	}
	if stop, err := r.Turn(context.Background(), "and the docs"); err != nil || stop != proto.StopBudget {
		t.Fatalf("resumed turn: %q, %v; want the session's tool-call budget, spent by the first turn, to stop it", stop, err)
	}
	// The budget stops the turn before its request; the turn it opened shows how it was framed.
	msgs := r.Conversation().Messages()
	last := msgs[len(msgs)-1].Content
	if text := last[len(last)-1].Text; text != "and the docs" {
		t.Fatalf("the resumed turn opened with %q; want the bare text of a later turn, not a first turn's", text)
	}
}

// TestResumeRefusesALogInsideATurn: a log a crash cut off inside a turn is closed first (core's recovery); Resume does
// not guess how the turn ended.
func TestResumeRefusesALogInsideATurn(t *testing.T) {
	h := newHarness(t, Budget{}, goinfertest.Calls("", "t1", "glob", `{"pattern":"*.go"}`), goinfertest.Says("ok"))
	h.sess.Turn(context.Background(), "list")
	evs, _ := h.st.Events("s_test", 0)
	if _, err := Resume(h.sess.cfg, evs[:len(evs)-1]); err == nil {
		t.Fatal("Resume took a log whose last turn has no turn.end")
	}
}

// TestResumeCountsOnlyCallsThatRan: a call the turn's budget answered "not run" did not count toward the session's
// tool calls live, and does not count on resume either.
func TestResumeCountsOnlyCallsThatRan(t *testing.T) {
	t.Run("a repeated failed call", func(t *testing.T) {
		h := newHarness(t, Budget{}, goinfertest.Calls("", "t1", "read", `{"path":"nope.go"}`),
			goinfertest.Calls("", "t2", "read", `{"path":"nope.go"}`), goinfertest.Says("gave up"))
		if stop, err := h.sess.Turn(context.Background(), "read it"); err != nil || stop != proto.StopEndTurn {
			t.Fatalf("turn: %q, %v", stop, err)
		}
		evs, _ := h.st.Events("s_test", 0)
		r, err := Resume(h.sess.cfg, evs)
		if err != nil {
			t.Fatal(err)
		}
		if h.sess.calls != 1 || r.calls != h.sess.calls {
			t.Fatalf("live counted %d calls, resumed %d; want 1 and 1: the repeat was answered without running", h.sess.calls, r.calls)
		}
	})
	b := testBudget
	b.MaxToolCalls = 1
	h := newHarness(t, b, goinfertest.Calls("", "t1", "glob", `{"pattern":"*.go"}`, "t2", "glob", `{"pattern":"*.md"}`))
	if stop, err := h.sess.Turn(context.Background(), "list"); err != nil || stop != proto.StopBudget {
		t.Fatalf("turn: %q, %v; want the turn's tool-call budget to stop it", stop, err)
	}
	evs, _ := h.st.Events("s_test", 0)
	r, err := Resume(h.sess.cfg, evs)
	if err != nil {
		t.Fatal(err)
	}
	if h.sess.calls != 1 || r.calls != h.sess.calls {
		t.Fatalf("live counted %d calls, resumed %d; want 1 and 1", h.sess.calls, r.calls)
	}
}

// TestResumeCountsCallsWithoutCause: an event that names its call by id alone, with no cause, still counts the call
// it belongs to.
func TestResumeCountsCallsWithoutCause(t *testing.T) {
	in := json.RawMessage(`{"pattern":"*.go"}`)
	ev := func(seq int64, typ proto.EventType, cause int64, payload any) proto.Event {
		e, err := proto.NewEvent(seq, time.Now(), typ, cause, payload)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	history := []proto.Event{
		ev(1, proto.EvUserMessage, 0, proto.UserMessage{Content: []proto.ContentBlock{proto.Text("x")}}),
		ev(2, proto.EvAssistantMessage, 0, proto.AssistantMessage{Request: 1, StopReason: "tool_use", Content: []proto.ModelBlock{{Type: "tool_use", ID: "t1", Name: "glob", Input: in}}}),
		ev(3, proto.EvToolCall, 2, proto.ToolCall{ID: "t1", Name: "glob", Input: in}),
		ev(4, proto.EvToolStarted, 0, proto.ToolStarted{ID: "t1"}),
		ev(5, proto.EvToolFinished, 0, proto.ToolFinished{ID: "t1", Outcome: proto.OutcomeOK, Result: "a.go"}),
		ev(6, proto.EvTurnEnd, 1, proto.TurnEnd{StopReason: proto.StopEndTurn}),
	}
	if n := callsRan(history); n != 1 {
		t.Fatalf("callsRan = %d; want the started call counted", n)
	}
}
