package loop

import "context"

// DesignWindow is the window tack is designed for (DESIGN §2.3): the 16k context goinfer has measured working under an
// agent's tool schema. The tier-0 budget test holds the fixed prompt to FixedPromptBudget(DesignWindow).
const DesignWindow = 16384

// FixedPromptBudget is the hard budget for tack's fixed prompt, system instructions plus tool schemas, in tokens of the
// model's own tokenizer: a fifth of the served window (DESIGN §2.3, ADR-003). It is 3,276 at the 16k design window and
// grows with a larger one, so the user's share of any window is at least four fifths.
func FixedPromptBudget(window int) int { return window / 5 }

// Remaining is what a turn's budgets have left when a tool call runs: tokens (the lesser of the turn's and the
// session's) and tool calls (likewise, after this call). A task's child takes no more than it (core.subagent), so a
// subagent cannot carry a session past its budgets.
type Remaining struct{ Tokens, ToolCalls int }

type remainingKey struct{}

func withRemaining(ctx context.Context, r Remaining) context.Context {
	return context.WithValue(ctx, remainingKey{}, r)
}

// RemainingFrom is the budget a tool call's caller has left, if the loop said.
func RemainingFrom(ctx context.Context) (Remaining, bool) {
	r, ok := ctx.Value(remainingKey{}).(Remaining)
	return r, ok
}

type callIDKey struct{}

// CallFrom is the tool_use id of the call whose tool is running, if the loop said: a task's child records it as
// proto.SessionCreated.ParentCall.
func CallFrom(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(callIDKey{}).(string)
	return id, ok
}
