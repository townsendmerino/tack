package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/townsendmerino/tack/internal/proto"
)

// inMode is an App whose session is in mode, with a backend that answers setMode with got (the mode asked for when got is "").
func inMode(t *testing.T, mode proto.Mode) (*App, *fakeBackend) {
	t.Helper()
	fb := &fakeBackend{}
	a := &App{Backend: fb}
	a.Update(tea.WindowSizeMsg{Width: 70, Height: 16})
	a.Update(EventMsg(ev(t, 1, proto.EvSessionCreated, proto.SessionCreated{Workspace: "/w", Model: "m", Mode: mode})))
	return a, fb
}

var shiftTab = tea.KeyMsg{Type: tea.KeyShiftTab}

// TestShiftTabCyclesThePlanAskEditsAutoOrder: each press asks the daemon for the next mode, wrapping from auto to plan; the
// screen's mode changes only when the daemon says so, through mode.changed, so a refused change shows nothing.
func TestShiftTabCyclesThePlanAskEditsAutoOrder(t *testing.T) {
	for _, c := range []struct {
		from, want proto.Mode
	}{{proto.ModePlan, proto.ModeAsk}, {proto.ModeAsk, proto.ModeEdits}, {proto.ModeAuto, proto.ModePlan}} {
		a, fb := inMode(t, c.from)
		press(a, shiftTab)
		if len(fb.modes) != 1 || fb.modes[0] != c.want {
			t.Errorf("from %s: asked %v, want %s", c.from, fb.modes, c.want)
		}
		if a.Screen.Status.Mode != c.from {
			t.Errorf("from %s: the screen moved to %s before the daemon said so", c.from, a.Screen.Status.Mode)
		}
	}
	a, fb := inMode(t, proto.ModePlan)
	press(a, shiftTab)
	a.Update(EventMsg(ev(t, 2, proto.EvModeChanged, proto.ModeChanged{Mode: proto.ModeAsk, By: "user"})))
	press(a, shiftTab)
	if len(fb.modes) != 2 || fb.modes[1] != proto.ModeEdits {
		t.Errorf("second press asked %v", fb.modes)
	}
}

// TestMovingToAutoShowsWhatAutoAllowsAndAsksToConfirm: from edits the next mode is auto, which is not asked for until the user
// confirms with y; any other key, Esc included, stays in edits and types nothing; while it is open the frame says what auto
// allows and still fits.
func TestMovingToAutoShowsWhatAutoAllowsAndAsksToConfirm(t *testing.T) {
	a, fb := inMode(t, proto.ModeEdits)
	press(a, shiftTab)
	if len(fb.modes) != 0 {
		t.Fatalf("asked for auto before the user confirmed: %v", fb.modes)
	}
	f := a.Frame()
	for _, want := range []string{"auto", "sandbox", "[y]", "[n]"} {
		if !strings.Contains(f, want) {
			t.Errorf("the confirmation lacks %q:\n%s", want, f)
		}
	}
	if n := len(strings.Split(f, "\n")); n != 16 {
		t.Errorf("%d lines", n)
	}
	for _, k := range []tea.KeyMsg{{Type: tea.KeyEsc}, {Type: tea.KeyRunes, Runes: []rune("n")}, {Type: tea.KeyRunes, Runes: []rune("x")}, {Type: tea.KeyEnter}} {
		a, fb = inMode(t, proto.ModeEdits)
		press(a, shiftTab)
		press(a, k)
		if len(fb.modes) != 0 || len(a.input) != 0 || strings.Contains(a.Frame(), "[y]") {
			t.Errorf("key %v: asked %v, composer %q, confirmation still open:\n%s", k, fb.modes, string(a.input), a.Frame())
		}
	}
	a, fb = inMode(t, proto.ModeEdits)
	press(a, shiftTab)
	press(a, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if len(fb.modes) != 1 || fb.modes[0] != proto.ModeAuto {
		t.Errorf("after y: asked %v", fb.modes)
	}
	if strings.Contains(a.Frame(), "[y]") {
		t.Error("the confirmation stayed open after y")
	}
}

// TestAnythingThatIsNotAConfirmationDoesNotConfirm: a second Shift+Tab while the confirmation is open leaves it, not auto, and Ctrl-C
// still quits from it.
func TestAnythingThatIsNotAConfirmationDoesNotConfirm(t *testing.T) {
	a, fb := inMode(t, proto.ModeEdits)
	press(a, shiftTab)
	press(a, shiftTab)
	if len(fb.modes) != 0 {
		t.Errorf("a second Shift+Tab confirmed: %v", fb.modes)
	}
	press(a, shiftTab)
	if _, cmd := a.Update(tea.KeyMsg{Type: tea.KeyCtrlC}); cmd == nil || !a.quit {
		t.Error("Ctrl-C did not quit from the confirmation")
	}
}

// TestSetModeAnswersShowAsNotes: a ceiling that lowered the mode says so, a busy session says to wait, an older daemon says to
// restart it, and another error is shown.
func TestSetModeAnswersShowAsNotes(t *testing.T) {
	for _, c := range []struct {
		got  proto.Mode
		err  error
		want string
	}{
		{proto.ModeAsk, nil, "repository"},
		{"", &proto.Error{Code: proto.CodeBusy, Message: "x"}, "turn is running"},
		{"", &proto.Error{Code: proto.CodeMethodNotFound, Message: "x"}, "older than this screen"},
		{"", &proto.Error{Code: proto.CodeUnauthorized, Message: "needs the client token"}, "needs the client token"},
		{"", errors.New("pipe closed"), "pipe closed"},
	} {
		a, fb := inMode(t, proto.ModeAsk)
		fb.modeGot, fb.modeErr = c.got, c.err
		press(a, shiftTab) // asks for edits
		if f := a.Frame(); !strings.Contains(f, c.want) {
			t.Errorf("answer %v / %q: want %q:\n%s", c.err, c.got, c.want, f)
		}
	}
	a, fb := inMode(t, proto.ModeAsk)
	fb.modeGot = proto.ModeEdits
	press(a, shiftTab)
	if f := a.Frame(); strings.Contains(f, "repository") {
		t.Errorf("a granted change said a repository lowered it:\n%s", f)
	}
}
