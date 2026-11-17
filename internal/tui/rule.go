package tui

import (
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/townsendmerino/tack/internal/proto"
)

// ruleEdit is the one-line editor for a question's rule (the e key). It holds the rule being written and the rules the question
// listed, which are what the daemon holds the edit to: keep the start of one, add arguments after it. The screen does not check that:
// the daemon does, and a refusal comes back as a note with the question still open.
type ruleEdit struct {
	text  []rune
	shown []string
	idx   int
}

// openRuleEdit starts the editor on the first rule of the open question, or does nothing when there is no question or it lists no
// rule (a write offers none: nothing to edit).
func (a *App) openRuleEdit() {
	q, ok := a.Screen.Pending()
	if !ok || len(q.Rules) == 0 {
		return
	}
	a.rule = &ruleEdit{text: []rune(q.Rules[0]), shown: q.Rules}
}

// ruleKey handles a key while the editor is open. Esc goes back to the question's keys without answering or cancelling the turn;
// Tab moves to the next listed rule; Enter sends the text as the edited rule, allowing for the session, unless it is empty, which
// sends nothing and stays open so a blank is never mistaken for "no rule". Other keys edit the text; control characters never enter it.
func (a *App) ruleKey(k tea.KeyMsg) tea.Cmd {
	e := a.rule
	switch k.Type {
	case tea.KeyEsc:
		a.rule = nil
	case tea.KeyTab:
		e.idx = (e.idx + 1) % len(e.shown)
		e.text = []rune(e.shown[e.idx])
	case tea.KeyBackspace:
		if len(e.text) > 0 {
			e.text = e.text[:len(e.text)-1]
		}
	case tea.KeySpace:
		e.text = append(e.text, ' ')
	case tea.KeyRunes:
		for _, r := range k.Runes {
			if !hiddenRune(r) && r != utf8.RuneError && r >= 0x20 && r != 0x7f && (r < 0x80 || r > 0x9f) {
				e.text = append(e.text, r)
			}
		}
	case tea.KeyEnter:
		q, ok := a.Screen.Pending()
		text := strings.TrimSpace(string(e.text))
		if !ok || text == "" {
			return nil
		}
		a.rule = nil
		b := a.Backend
		return func() tea.Msg { return AnsweredMsg{Err: b.Respond(q.ID, q.Seq, proto.DecisionAllow, "session", text)} }
	}
	return nil
}

// ruleRows is the editor, drawn above the composer while it is open.
func (a *App) ruleRows() []string {
	if a.rule == nil {
		return nil
	}
	line := "> " + string(a.rule.text)
	for cells(line) > a.width-1 {
		_, n := utf8.DecodeRuneInString(line)
		line = line[n:]
	}
	hint := "[enter] allow for the session with this rule  [esc] back"
	if len(a.rule.shown) > 1 {
		hint = "[enter] allow with this rule  [tab] next rule  [esc] back"
	}
	return []string{
		fit("edit the rule for the session: keep the start, add arguments after it (the daemon refuses anything wider)", a.width),
		line + "▏",
		fit(hint, a.width),
	}
}
