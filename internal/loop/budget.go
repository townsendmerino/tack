package loop

// DesignWindow is the window tack is designed for (DESIGN §2.3): the 16k context goinfer has measured working under an
// agent's tool schema. The tier-0 budget test holds the fixed prompt to FixedPromptBudget(DesignWindow).
const DesignWindow = 16384

// FixedPromptBudget is the hard budget for tack's fixed prompt, system instructions plus tool schemas, in tokens of the
// model's own tokenizer: a fifth of the served window (DESIGN §2.3, ADR-003). It is 3,276 at the 16k design window and
// grows with a larger one, so the user's share of any window is at least four fifths.
func FixedPromptBudget(window int) int { return window / 5 }
