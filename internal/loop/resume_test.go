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

// TestResumeCountsSessionTokens: the tokens a resumed session has used are the server's computation, as the live loop
// counts them: prefilled plus output where the server said what it prefilled, else the whole prompt plus output; and
// a session that has used its budget is stopped before its next request.
func TestResumeCountsSessionTokens(t *testing.T) {
	h := newHarness(t, Budget{})
	prefilled := 100
	ev := func(seq int64, typ proto.EventType, payload any) proto.Event {
		e, err := proto.NewEvent(seq, time.Now(), typ, 0, payload)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	text := []proto.ModelBlock{{Type: "text", Text: "a"}}
	history := []proto.Event{
		ev(1, proto.EvUserMessage, proto.UserMessage{Content: []proto.ContentBlock{proto.Text("x")}}),
		ev(2, proto.EvRequestSent, proto.RequestSent{Request: 1, Server: "s", Model: "m"}),
		ev(3, proto.EvUsage, proto.Usage{Request: 1, Server: "s", Prompt: 900, Prefilled: &prefilled, Output: 10}), // 110
		ev(4, proto.EvAssistantMessage, proto.AssistantMessage{Request: 1, Content: text, StopReason: "end_turn"}),
		ev(5, proto.EvTurnEnd, proto.TurnEnd{StopReason: proto.StopEndTurn}),
		ev(6, proto.EvUserMessage, proto.UserMessage{Content: []proto.ContentBlock{proto.Text("y")}}),
		ev(7, proto.EvRequestSent, proto.RequestSent{Request: 2, Server: "s", Model: "m"}),
		ev(8, proto.EvUsage, proto.Usage{Request: 2, Server: "s", Prompt: 50, Output: 5}), // 55
		ev(9, proto.EvAssistantMessage, proto.AssistantMessage{Request: 2, Content: text, StopReason: "end_turn"}),
		ev(10, proto.EvTurnEnd, proto.TurnEnd{StopReason: proto.StopEndTurn}),
	}
	cfg := h.sess.cfg
	cfg.Budget.SessionTokens = 165
	r, err := Resume(cfg, history)
	if err != nil {
		t.Fatal(err)
	}
	if r.tokens != 165 || r.requests != 2 || r.turns != 2 {
		t.Fatalf("resumed tokens %d, requests %d, turns %d; want 165 (110 + 55), 2, 2", r.tokens, r.requests, r.turns)
	}
	if stop, err := r.Turn(context.Background(), "z"); err != nil || stop != proto.StopBudget {
		t.Fatalf("a turn past the session's token budget: %q, %v; want stopped", stop, err)
	}
}

// TestResumeCountsARepeatedIDWithoutCause: two calls a response gives one id, each run, count as two toward the
// session's budget, as the live loop counted them, when the log has no causes to tell them apart.
func TestResumeCountsARepeatedIDWithoutCause(t *testing.T) {
	in := json.RawMessage(`{"pattern":"*.go"}`)
	ev := func(seq int64, typ proto.EventType, cause int64, payload any) proto.Event {
		e, err := proto.NewEvent(seq, time.Now(), typ, cause, payload)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	use := proto.ModelBlock{Type: "tool_use", ID: "t1", Name: "glob", Input: in}
	history := []proto.Event{
		ev(1, proto.EvUserMessage, 0, proto.UserMessage{Content: []proto.ContentBlock{proto.Text("x")}}),
		ev(2, proto.EvAssistantMessage, 0, proto.AssistantMessage{Request: 1, StopReason: "tool_use", Content: []proto.ModelBlock{use, use}}),
		ev(3, proto.EvToolCall, 2, proto.ToolCall{ID: "t1", Name: "glob", Input: in}),
		ev(4, proto.EvToolCall, 2, proto.ToolCall{ID: "t1", Name: "glob", Input: in}),
		ev(5, proto.EvToolStarted, 0, proto.ToolStarted{ID: "t1"}),
		ev(6, proto.EvToolFinished, 0, proto.ToolFinished{ID: "t1", Outcome: proto.OutcomeOK, Result: "a.go"}),
		ev(7, proto.EvToolStarted, 0, proto.ToolStarted{ID: "t1"}),
		ev(8, proto.EvToolFinished, 0, proto.ToolFinished{ID: "t1", Outcome: proto.OutcomeOK, Result: "a.go"}),
		ev(9, proto.EvTurnEnd, 1, proto.TurnEnd{StopReason: proto.StopEndTurn}),
	}
	if n := callsRan(history); n != 2 {
		t.Fatalf("callsRan = %d; want both calls counted", n)
	}
}
