package loop

import (
	"context"
	"strings"
	"testing"

	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
	"github.com/townsendmerino/tack/internal/tools"
)

// taskHarness is a harness whose tools include task, answered by sub.
func taskHarness(t *testing.T, b Budget, sub tools.Subagent, replies ...goinfertest.Reply) *harness {
	t.Helper()
	h := newHarness(t, b, replies...)
	h.sess.cfg.Tools = tools.New(tools.Options{Subagent: sub})
	h.sess = New(h.sess.cfg)
	return h
}

// TestATaskCountsTowardTheParent: a task's answer is its result, with the child's session named, and the child's
// tokens are recorded on the call and counted toward the parent session, as a resume counts them.
func TestATaskCountsTowardTheParent(t *testing.T) {
	var asked string
	sub := func(_ context.Context, prompt string) (tools.SubagentAnswer, error) {
		asked = prompt
		return tools.SubagentAnswer{Text: "It is in mcp/token_budget.go.", Tokens: 4000, Session: "s_child"}, nil
	}
	h := taskHarness(t, Budget{}, sub,
		goinfertest.Calls("", "t1", "task", `{"prompt":"where is applyTokenBudget?"}`), goinfertest.Says("done"))
	before := h.sess.tokens
	if _, err := h.sess.Turn(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	var fin proto.ToolFinished
	for _, p := range h.payloads(proto.EvToolFinished) {
		fin = p.(proto.ToolFinished)
	}
	if asked != "where is applyTokenBudget?" || fin.Tokens != 4000 || fin.Outcome != proto.OutcomeOK ||
		!strings.Contains(fin.Result, "mcp/token_budget.go") || !strings.HasSuffix(fin.Result, "[subagent session s_child]") {
		t.Fatalf("asked %q; finished %+v", asked, fin)
	}
	var own int
	for _, p := range h.payloads(proto.EvUsage) {
		own += computedTokens(p.(proto.Usage))
	}
	if h.sess.tokens-before != own+4000 {
		t.Errorf("session tokens grew %d, want its own %d plus the child's 4000", h.sess.tokens-before, own)
	}
	evs, _ := h.st.Events("s_test", 0)
	r, err := Resume(h.sess.cfg, evs)
	if err != nil || r.tokens != h.sess.tokens {
		t.Errorf("resumed tokens %d, live %d, %v", r.tokens, h.sess.tokens, err)
	}
	h.checkRebuild(t)
}

// TestATaskCanTripTheParentsBudget: a child's tokens count toward the parent's turn, so a subagent cannot be used to
// run past it.
func TestATaskCanTripTheParentsBudget(t *testing.T) {
	sub := func(context.Context, string) (tools.SubagentAnswer, error) {
		return tools.SubagentAnswer{Text: "an answer", Tokens: 50000}, nil
	}
	b := testBudget
	b.TurnTokens = 10000 // the parent's own requests stay far under it; the child's 50,000 do not
	h := taskHarness(t, b, sub,
		goinfertest.Calls("", "t1", "task", `{"prompt":"look"}`), goinfertest.Says("done"))
	reason, err := h.sess.Turn(context.Background(), "go")
	if err != nil || reason != proto.StopBudget {
		t.Fatalf("reason %q, %v; want the turn's token budget to trip", reason, err)
	}
}

// TestATaskWithNoPromptIsAnError: the model must say what to ask; nothing runs.
func TestATaskWithNoPromptIsAnError(t *testing.T) {
	ran := false
	sub := func(context.Context, string) (tools.SubagentAnswer, error) {
		ran = true
		return tools.SubagentAnswer{}, nil
	}
	h := taskHarness(t, Budget{}, sub, goinfertest.Calls("", "t1", "task", `{"prompt":"  "}`), goinfertest.Says("done"))
	if _, err := h.sess.Turn(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	var fin proto.ToolFinished
	for _, p := range h.payloads(proto.EvToolFinished) {
		fin = p.(proto.ToolFinished)
	}
	if ran || fin.Outcome != proto.OutcomeError {
		t.Errorf("ran %v, finished %+v", ran, fin)
	}
}

// TestATasksToolCallsCountTowardTheParent: a child's tool calls are recorded on the call and counted toward the
// parent's tool-call budgets, live and on a resume.
func TestATasksToolCallsCountTowardTheParent(t *testing.T) {
	sub := func(context.Context, string) (tools.SubagentAnswer, error) {
		return tools.SubagentAnswer{Text: "an answer", Calls: 3}, nil
	}
	h := taskHarness(t, Budget{}, sub, goinfertest.Calls("", "t1", "task", `{"prompt":"look"}`), goinfertest.Says("done"))
	if _, err := h.sess.Turn(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	var fin proto.ToolFinished
	for _, p := range h.payloads(proto.EvToolFinished) {
		fin = p.(proto.ToolFinished)
	}
	if fin.Calls != 3 || h.sess.calls != 1+3 {
		t.Errorf("recorded %d calls, the session counts %d; want 3 and 4", fin.Calls, h.sess.calls)
	}
	evs, _ := h.st.Events("s_test", 0)
	if r, err := Resume(h.sess.cfg, evs); err != nil || r.calls != h.sess.calls {
		t.Errorf("resumed calls %d, live %d, %v", r.calls, h.sess.calls, err)
	}
}

// TestATasksToolCallsCanTripTheParent: the child's calls count toward the parent's turn, so the parent's tool-call
// budget holds across a subagent.
func TestATasksToolCallsCanTripTheParent(t *testing.T) {
	sub := func(context.Context, string) (tools.SubagentAnswer, error) {
		return tools.SubagentAnswer{Text: "an answer", Calls: 10}, nil
	}
	b := testBudget // 8 tool calls a turn
	h := taskHarness(t, b, sub, goinfertest.Calls("", "t1", "task", `{"prompt":"look"}`),
		goinfertest.Calls("", "t2", "glob", `{"pattern":"*.go"}`), goinfertest.Says("done"))
	reason, err := h.sess.Turn(context.Background(), "go")
	if err != nil || reason != proto.StopBudget {
		t.Fatalf("reason %q, %v; want the tool-call budget to trip", reason, err)
	}
}

// TestATaskSeesWhatTheParentHasLeft: the remaining budget a task's child is held to is the parent's, after the task
// call itself.
func TestATaskSeesWhatTheParentHasLeft(t *testing.T) {
	var got Remaining
	var ok bool
	sub := func(ctx context.Context, _ string) (tools.SubagentAnswer, error) {
		got, ok = RemainingFrom(ctx)
		return tools.SubagentAnswer{Text: "x"}, nil
	}
	h := taskHarness(t, testBudget, sub, goinfertest.Calls("", "t1", "task", `{"prompt":"look"}`), goinfertest.Says("done"))
	if _, err := h.sess.Turn(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	var used int
	for _, p := range h.payloads(proto.EvUsage) {
		used += computedTokens(p.(proto.Usage))
		break // the first request's, before the task ran
	}
	if !ok || got.ToolCalls != testBudget.MaxToolCalls-1 || got.Tokens != min(testBudget.TurnTokens, testBudget.SessionTokens)-used {
		t.Errorf("remaining %+v (%v); want %d calls and %d tokens", got, ok, testBudget.MaxToolCalls-1,
			min(testBudget.TurnTokens, testBudget.SessionTokens)-used)
	}
}
