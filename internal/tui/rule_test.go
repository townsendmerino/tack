package tui

import (
	"encoding/json"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/townsendmerino/tack/internal/proto"
)

// askedWithRules is an App whose bash call c1 has an open question at seq 7 that lists the rules allowing for the session would add.
func askedWithRules(t *testing.T, rules ...string) (*App, *fakeBackend) {
	t.Helper()
	fb := &fakeBackend{}
	a := &App{Backend: fb}
	a.Update(tea.WindowSizeMsg{Width: 80, Height: 16})
	a.Update(EventMsg(ev(t, 6, proto.EvToolCall, proto.ToolCall{ID: "c1", Name: "bash", Input: json.RawMessage(`{"command":"go vet ./..."}`)})))
	a.Update(EventMsg(ev(t, 7, proto.EvPermissionRequested, proto.PermissionRequested{ID: "c1", Tool: "bash", Mode: proto.ModeAsk,
		Command: "go vet ./...", Reasons: []string{"no rule covers it"}, Rules: rules})))
	return a, fb
}

func key(r string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(r)} }

// TestTheEditKeyIsOfferedOnlyWhenThereIsARuleToEdit: the question's hint names [e]dit when it lists rules, and a question that lists
// none (a write) neither shows the key nor opens an editor on it.
func TestTheEditKeyIsOfferedOnlyWhenThereIsARuleToEdit(t *testing.T) {
	a, _ := askedWithRules(t, "/usr/bin/go vet")
	if f := a.Frame(); !strings.Contains(f, "[e]dit rule") {
		t.Errorf("no edit key offered:\n%s", f)
	}
	a, fb := askedWithRules(t)
	if f := a.Frame(); strings.Contains(f, "[e]dit") {
		t.Errorf("an edit key offered with nothing to edit:\n%s", f)
	}
	press(a, key("e"))
	if f := a.Frame(); strings.Contains(f, "edit the rule") || len(fb.answers) != 0 {
		t.Errorf("an editor opened on a question with no rules:\n%s", f)
	}
}

// TestEditingARuleSendsItAsAllowForTheSession: e opens a one-line editor holding the shown rule; typed characters and backspace
// change it; Enter answers allow, scope session, with exactly that text as the rule; nothing is sent before Enter and nothing of it
// reaches the composer.
func TestEditingARuleSendsItAsAllowForTheSession(t *testing.T) {
	a, fb := askedWithRules(t, "/usr/bin/go vet")
	press(a, key("e"))
	f := a.Frame()
	if !strings.Contains(f, "edit the rule") || !strings.Contains(f, "> /usr/bin/go vet") {
		t.Fatalf("the editor does not hold the shown rule:\n%s", f)
	}
	typed(a, " ./internal/tui/...")
	press(a, key("x"))
	press(a, tea.KeyMsg{Type: tea.KeyBackspace})
	if len(fb.answers) != 0 {
		t.Fatalf("answered before Enter: %+v", fb.answers)
	}
	press(a, tea.KeyMsg{Type: tea.KeyEnter})
	want := answer{id: "c1", seq: 7, decision: proto.DecisionAllow, scope: "session", rule: "/usr/bin/go vet ./internal/tui/..."}
	if len(fb.answers) != 1 || fb.answers[0] != want {
		t.Errorf("answers %+v, want %+v", fb.answers, want)
	}
	if len(a.input) != 0 || strings.Contains(a.Frame(), "edit the rule") {
		t.Errorf("composer %q; the editor stayed:\n%s", string(a.input), a.Frame())
	}
}

// TestEscLeavesTheEditorAndKeepsTheQuestionOpen: Esc in the editor goes back to the question's keys without answering and without
// cancelling the turn; the question can then be answered as before.
func TestEscLeavesTheEditorAndKeepsTheQuestionOpen(t *testing.T) {
	a, fb := askedWithRules(t, "/usr/bin/go vet")
	press(a, key("e"))
	press(a, tea.KeyMsg{Type: tea.KeyEsc})
	if fb.cancels != 0 || len(fb.answers) != 0 || strings.Contains(a.Frame(), "edit the rule") {
		t.Fatalf("cancels %d, answers %+v:\n%s", fb.cancels, fb.answers, a.Frame())
	}
	press(a, key("d"))
	if len(fb.answers) != 1 || fb.answers[0].decision != proto.DecisionDeny || fb.answers[0].rule != "" {
		t.Errorf("answers %+v", fb.answers)
	}
}

// TestSeveralRulesAreCycledWithTabAndOneIsSent: with more than one shown the editor starts on the first, Tab moves to the next, and
// only the one in the editor is sent: one rule per question.
func TestSeveralRulesAreCycledWithTabAndOneIsSent(t *testing.T) {
	a, fb := askedWithRules(t, "/usr/bin/git reset", "/usr/bin/make test")
	press(a, key("e"))
	press(a, tea.KeyMsg{Type: tea.KeyTab})
	typed(a, " ./pkg")
	press(a, tea.KeyMsg{Type: tea.KeyEnter})
	if len(fb.answers) != 1 || fb.answers[0].rule != "/usr/bin/make test ./pkg" {
		t.Errorf("answers %+v", fb.answers)
	}
}

// TestAnEmptyEditorSendsNothing: Enter on an emptied editor answers nothing and keeps it open, so a blank can never go as "no rule"
// and become a plain allow for the session.
func TestAnEmptyEditorSendsNothing(t *testing.T) {
	a, fb := askedWithRules(t, "/usr/bin/go")
	press(a, key("e"))
	for range "/usr/bin/go" {
		press(a, tea.KeyMsg{Type: tea.KeyBackspace})
	}
	press(a, tea.KeyMsg{Type: tea.KeyEnter})
	if len(fb.answers) != 0 || !strings.Contains(a.Frame(), "edit the rule") {
		t.Errorf("answers %+v; the editor closed:\n%s", fb.answers, a.Frame())
	}
}

// TestARefusedEditShowsAsANoteAndTheQuestionStaysAnswerable: the daemon's refusal (invalid params) is a note, the question is
// still open, and its keys work again.
func TestARefusedEditShowsAsANoteAndTheQuestionStaysAnswerable(t *testing.T) {
	a, fb := askedWithRules(t, "/usr/bin/go vet")
	fb.respondErr = &proto.Error{Code: proto.CodeInvalidParams, Message: "permission/respond: rule is not a narrowing of what was shown"}
	press(a, key("e"))
	press(a, tea.KeyMsg{Type: tea.KeyEnter})
	f := a.Frame()
	if !strings.Contains(f, "not a narrowing") || !strings.Contains(f, "[a]llow") {
		t.Errorf("the refusal or the question's keys are not shown:\n%s", f)
	}
}

// TestTheEditorClosesWhenTheQuestionIsAnsweredElsewhere: a permission.resolved from another client ends the editor, so Enter
// cannot send a rule for a question that is gone.
func TestTheEditorClosesWhenTheQuestionIsAnsweredElsewhere(t *testing.T) {
	a, fb := askedWithRules(t, "/usr/bin/go vet")
	press(a, key("e"))
	a.Update(EventMsg(ev(t, 8, proto.EvPermissionResolved, proto.PermissionResolved{ID: "c1", Decision: proto.DecisionAllow, By: proto.DecidedByUser, Scope: "once"})))
	if strings.Contains(a.Frame(), "edit the rule") {
		t.Errorf("the editor stayed:\n%s", a.Frame())
	}
	press(a, tea.KeyMsg{Type: tea.KeyEnter})
	if len(fb.answers) != 0 {
		t.Errorf("a rule was sent for a resolved question: %+v", fb.answers)
	}
}
