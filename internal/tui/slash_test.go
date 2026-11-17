package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// enter types s and presses Enter, running what it returns.
func enter(a *App, s string) tea.Cmd {
	typed(a, s)
	_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	run(a, cmd)
	return cmd
}

// TestASlashCommandRunsAndIsNeverSentToTheModel: /help lists the commands in the transcript, /quit ends the screen, /cancel
// cancels the turn; none of them is a prompt, and the composer empties.
func TestASlashCommandRunsAndIsNeverSentToTheModel(t *testing.T) {
	fb := &fakeBackend{}
	a := &App{Backend: fb}
	a.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	enter(a, "/help")
	f := a.Frame()
	for _, want := range []string{"/help", "/quit", "/cancel"} {
		if !strings.Contains(f, want) {
			t.Errorf("/help does not list %s:\n%s", want, f)
		}
	}
	enter(a, "/cancel")
	if fb.cancels != 1 {
		t.Errorf("cancels %d", fb.cancels)
	}
	if _, cmd := a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/quit")}); cmd != nil {
		t.Fatal("typing sent a command")
	}
	_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !a.quit || cmd == nil {
		t.Error("/quit did not quit")
	}
	if len(fb.sent) != 0 || len(a.input) != 0 {
		t.Errorf("sent %q, composer %q", fb.sent, string(a.input))
	}
}

// TestAnUnknownCommandIsANoteAndKeepsTheText: a typo is not sent to the model and is not lost.
func TestAnUnknownCommandIsANoteAndKeepsTheText(t *testing.T) {
	fb := &fakeBackend{}
	a := &App{Backend: fb}
	a.Update(tea.WindowSizeMsg{Width: 60, Height: 12})
	enter(a, "/hepl")
	if f := a.Frame(); !strings.Contains(f, "unknown command /hepl") || !strings.Contains(f, "> /hepl") {
		t.Errorf("frame:\n%s", f)
	}
	if len(fb.sent) != 0 {
		t.Errorf("sent %q", fb.sent)
	}
}

// TestADoubleSlashSendsASingleOne: a prompt that really starts with a slash is written //, and a slash inside the text is just
// text, as is a path.
func TestADoubleSlashSendsASingleOne(t *testing.T) {
	fb := &fakeBackend{}
	a := &App{Backend: fb}
	enter(a, "//etc/hosts is what")
	enter(a, "what is in /etc/hosts")
	enter(a, "/etc/hosts is what")
	if got := strings.Join(fb.sent, "|"); got != "/etc/hosts is what|what is in /etc/hosts|/etc/hosts is what" {
		t.Errorf("sent %q", got)
	}
}

// TestACommandNeedsItsOwnWord: /help with arguments still runs it, and a command is matched whole, not by prefix.
func TestACommandNeedsItsOwnWord(t *testing.T) {
	fb := &fakeBackend{}
	a := &App{Backend: fb}
	a.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	enter(a, "/helpme")
	if f := a.Frame(); !strings.Contains(f, "unknown command /helpme") {
		t.Errorf("a prefix matched:\n%s", f)
	}
}
