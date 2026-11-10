package tui

import (
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"github.com/townsendmerino/tack/internal/proto"
)

// EventMsg is one of the session's events, in log order, from the daemon's subscription.
type EventMsg proto.Event

// SentMsg is the answer to a Backend.Prompt: nil Err means the daemon took the prompt.
type SentMsg struct{ Err error }

// Backend is what the terminal layer needs from the daemon connection: start a turn with the user's message. The
// session's events come back as EventMsg through the program's Send.
type Backend interface {
	Prompt(text string) error
}

// App is the Bubble Tea model (DESIGN §10): a View (Screen) of the session, a one-line composer under it, and keys. It holds no
// connection state; a prompt goes through Backend, whose call runs in a command so the UI never blocks on the daemon.
type App struct {
	Screen  View
	Backend Backend

	input         []rune
	width, height int
	quit          bool
}

// Init starts nothing: the caller subscribes before it runs the program.
func (a *App) Init() tea.Cmd { return nil }

// Update folds in an event, a resize, a key or a prompt's answer.
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case EventMsg:
		a.Screen.Apply(proto.Event(m))
	case SentMsg:
		if m.Err != nil {
			a.Screen.note("! prompt: " + m.Err.Error())
		}
	case tea.WindowSizeMsg:
		a.width, a.height = m.Width, m.Height
	case tea.KeyMsg:
		return a.key(m)
	}
	return a, nil
}

func (a *App) key(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.Type {
	case tea.KeyCtrlC, tea.KeyCtrlD:
		a.quit = true
		return a, tea.Quit
	case tea.KeyEnter:
		text := strings.TrimSpace(string(a.input))
		if text == "" {
			return a, nil
		}
		a.input = nil
		a.Screen.Scroll = 0
		b := a.Backend
		return a, func() tea.Msg { return SentMsg{Err: b.Prompt(text)} }
	case tea.KeyBackspace:
		if len(a.input) > 0 {
			a.input = a.input[:len(a.input)-1]
		}
	case tea.KeyPgUp:
		a.Screen.Scroll += max(a.height/2, 1)
	case tea.KeyPgDown:
		a.Screen.Scroll = max(a.Screen.Scroll-max(a.height/2, 1), 0)
	case tea.KeySpace:
		a.input = append(a.input, ' ')
	case tea.KeyRunes:
		for _, r := range k.Runes {
			if !hiddenRune(r) && r != utf8.RuneError && r >= 0x20 && r != 0x7f && (r < 0x80 || r > 0x9f) {
				a.input = append(a.input, r)
			}
		}
	}
	return a, nil
}

// Frame is the screen: the view above the composer line, which is cut from its left when it outgrows the width so the
// cursor end stays visible.
func (a *App) Frame() string {
	if a.width <= 0 || a.height <= 0 {
		return ""
	}
	if a.height < 2 {
		return a.Screen.Render(a.width, a.height)
	}
	line := "> " + string(a.input)
	for runewidth.StringWidth(line) > a.width-1 {
		_, n := utf8.DecodeRuneInString(line)
		line = line[n:]
	}
	return a.Screen.Render(a.width, a.height-1) + "\n" + line + "▏"
}

// View renders Frame; it is the tea.Model method.
func (a *App) View() string { return a.Frame() }
