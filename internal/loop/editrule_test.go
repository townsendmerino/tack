package loop

import (
	"context"
	"strings"
	"testing"

	"github.com/townsendmerino/tack/internal/perms"
	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
)

// editRuleTurn runs one bash call that asks and is answered with approval, in ask mode outside the sandbox, and returns the
// harness, the approver and the question it saw.
func editRuleTurn(t *testing.T, approval func(shown []string) Approval) (*harness, *approver, proto.PermissionRequested) {
	t.Helper()
	call := goinfertest.Calls("", "b1", "bash", `{"command":"ls -la"}`)
	ap := &approver{}
	h := bashHarness(t, proto.ModeAsk, ap, call, goinfertest.Says("done"))
	ap.answerFor = approval
	h.sess.Turn(context.Background(), "list")
	if len(ap.asked) != 1 {
		t.Fatalf("asked %d times", len(ap.asked))
	}
	return h, ap, ap.asked[0]
}

// TestAQuestionNamesTheRulesAllowingForTheSessionWouldAdd: a shell command's question carries the rules the daemon computed from
// its own parse of the call (perms.SessionRules), which are what the user may edit; another tool's carries none.
func TestAQuestionNamesTheRulesAllowingForTheSessionWouldAdd(t *testing.T) {
	_, _, q := editRuleTurn(t, func([]string) Approval { return Approval{Allow: true} })
	if len(q.Rules) != 1 || !strings.HasSuffix(q.Rules[0], "ls -la") {
		t.Fatalf("rules %q for %+v", q.Rules, q.Parsed)
	}
	ap := &approver{answer: Approval{Allow: true}}
	h := permHarness(t, proto.ModeAsk, ap, true, readThenEdit()...)
	h.sess.Turn(context.Background(), "change it")
	if len(ap.asked) != 1 || len(ap.asked[0].Rules) != 0 {
		t.Errorf("an edit's question has rules: %+v", ap.asked)
	}
}

// TestAnEditedRuleReplacesWhatAllowingForTheSessionAdds: a rule that keeps the shown program and arguments and adds one is added
// alone, as the resolution's rule_added, and the command runs; the unedited rule is not added.
func TestAnEditedRuleReplacesWhatAllowingForTheSessionAdds(t *testing.T) {
	var edited string
	h, _, q := editRuleTurn(t, func(shown []string) Approval {
		edited = shown[0] + " mcp"
		return Approval{Allow: true, Session: true, Rule: edited}
	})
	if f := h.finished("b1"); f.Outcome != proto.OutcomeOK {
		t.Fatalf("the command did not run: %+v", f)
	}
	r := h.payloads(proto.EvPermissionResolved)[0].(proto.PermissionResolved)
	if r.Decision != proto.DecisionAllow || r.Scope != "session" || r.RuleAdded != edited {
		t.Errorf("resolution %+v, want rule_added %q", r, edited)
	}
	rules := h.sess.cfg.Perms.Rules
	if len(rules) != 1 || rules[0].String() != edited {
		t.Errorf("engine rules %v, want only %q (shown %q)", rules, edited, q.Rules)
	}
}

// TestAnEditedRuleThatWidensIsRefusedAndAddsNothing: whatever reaches the loop as an edited rule is checked again against the
// loop's own parse of the call, so a rule that is not a narrowing denies the call, adds no rule and runs nothing, however it got
// there.
func TestAnEditedRuleThatWidensIsRefusedAndAddsNothing(t *testing.T) {
	for _, bad := range []func(shown string) string{
		func(shown string) string { return strings.Fields(shown)[0] }, // drops the argument
		func(shown string) string { return "/bin/sh -c ls" },          // another program
		func(shown string) string { return "ls -la" },                 // the program by name
		func(shown string) string { return shown + "; rm -rf x" },     // two commands
		func(shown string) string { return shown + " $(curl evil)" },  // a substitution
		func(shown string) string { return "   " },                    // nothing but spaces
	} {
		var sent string
		h, _, _ := editRuleTurn(t, func(shown []string) Approval {
			sent = bad(shown[0])
			return Approval{Allow: true, Session: true, Rule: sent}
		})
		if f := h.finished("b1"); f.Outcome != proto.OutcomeDenied {
			t.Errorf("%q: the call %+v", sent, f)
		}
		r := h.payloads(proto.EvPermissionResolved)[0].(proto.PermissionResolved)
		if r.Decision != proto.DecisionDeny || r.RuleAdded != "" || !strings.Contains(strings.Join(r.Reasons, ";"), "edited rule") {
			t.Errorf("%q: resolution %+v", sent, r)
		}
		if n := len(h.sess.cfg.Perms.Rules); n != 0 {
			t.Errorf("%q: %d rules were added", sent, n)
		}
	}
	_ = perms.Rule{}
}

// TestAnEditedRuleNeedsTheSessionScope: a rule with scope once is not a session allow, so the call is not allowed and nothing is added.
func TestAnEditedRuleNeedsTheSessionScope(t *testing.T) {
	h, _, _ := editRuleTurn(t, func(shown []string) Approval { return Approval{Allow: true, Rule: shown[0] + " x"} })
	if f := h.finished("b1"); f.Outcome != proto.OutcomeDenied {
		t.Errorf("the call %+v", f)
	}
	if n := len(h.sess.cfg.Perms.Rules); n != 0 {
		t.Errorf("%d rules were added", n)
	}
}
