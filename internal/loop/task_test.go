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
