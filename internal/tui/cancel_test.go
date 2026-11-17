package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/townsendmerino/tack/internal/proto"
)

// TestEscCancelsTheTurn: Esc asks the daemon to cancel through the backend, whether or not a question is open (the daemon
// ends a pending question as a deny), and it never reaches the composer, which keeps what was typed.
func TestEscCancelsTheTurn(t *testing.T) {
	fb := &fakeBackend{}
	a := &App{Backend: fb}
	typed(a, "draft")
	press(a, tea.KeyMsg{Type: tea.KeyEsc})
	if fb.cancels != 1 || string(a.input) != "draft" {
		t.Errorf("cancels %d, composer %q", fb.cancels, string(a.input))
	}
	a, fb = asked(t)
	press(a, tea.KeyMsg{Type: tea.KeyEsc})
	if fb.cancels != 1 || len(fb.answers) != 0 {
		t.Errorf("with a question open: cancels %d, answers %+v", fb.cancels, fb.answers)
	}
}

// TestACancelThatFailsShowsAsANote: an older daemon, which has no session/cancel, says so, and any other error is shown.
func TestACancelThatFailsShowsAsANote(t *testing.T) {
	for _, c := range []struct {
		err  error
		want string
	}{
		{&proto.Error{Code: proto.CodeMethodNotFound, Message: "x"}, "older than this screen"},
		{&proto.Error{Code: proto.CodeUnauthorized, Message: "needs the client token"}, "needs the client token"},
	} {
		fb := &fakeBackend{cancelErr: c.err}
		a := &App{Backend: fb}
		a.Update(tea.WindowSizeMsg{Width: 60, Height: 10})
		press(a, tea.KeyMsg{Type: tea.KeyEsc})
		if f := a.Frame(); !strings.Contains(f, c.want) {
			t.Errorf("error %v: want %q:\n%s", c.err, c.want, f)
		}
	}
}
