package tui

import (
	"errors"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/townsendmerino/tack/internal/proto"
)

// ModeSetMsg is the answer to a Backend.SetMode: Asked is the mode the screen asked for, Got the mode the daemon says the session
// now has (a trusted repository can lower it), Err why it was not changed. The change itself arrives as mode.changed.
type ModeSetMsg struct {
	Asked, Got proto.Mode
	Err        error
}

// nextMode is the mode Shift+Tab moves to from m: plan, ask, edits, auto, and round again. Before the session's mode is known it
// is taken as plan.
func nextMode(m proto.Mode) proto.Mode {
	switch m {
	case proto.ModeAsk:
		return proto.ModeEdits
	case proto.ModeEdits:
		return proto.ModeAuto
	case proto.ModeAuto:
		return proto.ModePlan
	}
	return proto.ModeAsk
}

// setMode asks the daemon to move the session to m.
func (a *App) setMode(m proto.Mode, confirmed bool) tea.Cmd {
	b := a.Backend
	return func() tea.Msg {
		got, err := b.SetMode(m, confirmed)
		return ModeSetMsg{Asked: m, Got: got, Err: err}
	}
}

// cycleMode is Shift+Tab: the next mode, except that auto is not asked for until the user has seen what it allows and confirmed
// (confirmAuto, drawn by confirmRows).
func (a *App) cycleMode() tea.Cmd {
	next := nextMode(a.Screen.Status.Mode)
	if next == proto.ModeAuto {
		a.confirmAuto = true
		return nil
	}
	return a.setMode(next, false)
}

// confirmKey answers the auto confirmation: y confirms, and any other key, Esc and a second Shift+Tab included, declines. The key
// is taken either way, so it is never typed into the composer.
func (a *App) confirmKey(k tea.KeyMsg) tea.Cmd {
	a.confirmAuto = false
	if k.Type == tea.KeyRunes && len(k.Runes) == 1 && (k.Runes[0] == 'y' || k.Runes[0] == 'Y') {
		return a.setMode(proto.ModeAuto, true)
	}
	return nil
}

// modeSet shows a refused or lowered change as a note; a granted one is shown by its mode.changed.
func (a *App) modeSet(m ModeSetMsg) {
	var pe *proto.Error
	switch {
	case m.Err == nil && m.Got != "" && m.Got != m.Asked:
		a.Screen.note("! the repository caps this session at " + string(m.Got) + " mode")
	case m.Err == nil:
	case errors.As(m.Err, &pe) && pe.Code == proto.CodeBusy:
		a.Screen.note("! a turn is running; change the mode when it ends")
	case errors.As(m.Err, &pe) && pe.Code == proto.CodeMethodNotFound:
		a.Screen.note("! the daemon is older than this screen and cannot change the mode; restart it")
	default:
		a.Screen.note("! mode: " + m.Err.Error())
	}
}

// confirmRows is the confirmation for auto, drawn above the composer while it is open.
func (a *App) confirmRows() []string {
	if !a.confirmAuto {
		return nil
	}
	return []string{
		fit("switch to auto mode? auto approves, without asking, file edits and shell commands that stay inside the sandbox.", a.width),
		fit("It still asks for what leaves the sandbox, destroys work, or changes a CI definition or an instruction file.", a.width),
		fit("[y] switch to auto   [n] stay in "+string(a.Screen.Status.Mode), a.width),
	}
}
