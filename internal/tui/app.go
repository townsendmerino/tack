package tui

import (
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"github.com/townsendmerino/tack/internal/proto"
)

// EventMsg is one of the session's events, in log order, from the daemon's subscription.
type EventMsg proto.Event

// SentMsg is the answer to a Backend.Prompt: nil Err means the daemon took the prompt.
type SentMsg struct{ Err error }

// Backend is what the terminal layer needs from the daemon connection: start a turn with the user's message, and answer an
// open permission question by the seq of its permission.requested event (decision allow or deny, scope once or session). The
// session's events come back as EventMsg through the program's Send.
type Backend interface {
	Prompt(text string) error
	Respond(id string, seq int64, decision, scope string) error
	// Cancel ends the session's running turn (session/cancel); with none running it does nothing and succeeds.
	Cancel() error
	// Complete lists the workspace paths a query after an @ matches (workspace/complete), best first.
	Complete(query string) ([]proto.CompletionItem, error)
}

// CancelledMsg is the answer to a Backend.Cancel: the turn's end arrives as events.
type CancelledMsg struct{ Err error }

// AnsweredMsg is the answer to a Backend.Respond: nil Err means the daemon took it, and the permission.resolved event that
// follows is what closes the question on screen.
type AnsweredMsg struct{ Err error }

// App is the Bubble Tea model (DESIGN §10): a View (Screen) of the session, a one-line composer under it, and keys. It holds no
// connection state; a prompt goes through Backend, whose call runs in a command so the UI never blocks on the daemon.
type App struct {
	Screen  View
	Backend Backend
	// Debounce is how long @ completion waits after a key before asking; zero means 50 ms.
	Debounce time.Duration

	complete      completion
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
	case AnsweredMsg:
		var pe *proto.Error
		switch {
		case m.Err == nil:
		case errors.As(m.Err, &pe) && pe.Code == proto.CodeNotPending:
			a.Screen.note("! that question was already answered")
		case errors.As(m.Err, &pe) && pe.Code == proto.CodeMethodNotFound:
			a.Screen.note("! the daemon is older than this screen and cannot take answers; restart it")
		default:
			a.Screen.note("! answer: " + m.Err.Error())
		}
	case CancelledMsg:
		var pe *proto.Error
		switch {
		case m.Err == nil:
		case errors.As(m.Err, &pe) && pe.Code == proto.CodeMethodNotFound:
			a.Screen.note("! the daemon is older than this screen and cannot cancel; restart it")
		default:
			a.Screen.note("! cancel: " + m.Err.Error())
		}
	case debounceMsg:
		return a, a.debounced(m)
	case completedMsg:
		a.completed(m)
	case tea.WindowSizeMsg:
		a.width, a.height = m.Width, m.Height
	case tea.KeyMsg:
		return a.key(m)
	}
	return a, nil
}

// key handles the popup's keys when paths are offered, and otherwise the composer's; a key that changes the composer starts a
// new completion request.
func (a *App) key(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if len(a.complete.items) > 0 {
		switch k.Type {
		case tea.KeyDown:
			a.complete.sel = (a.complete.sel + 1) % len(a.complete.items)
			return a, nil
		case tea.KeyUp:
			a.complete.sel = (a.complete.sel + len(a.complete.items) - 1) % len(a.complete.items)
			return a, nil
		case tea.KeyTab:
			return a, a.accept()
		case tea.KeyEnter:
			if !k.Alt { // Alt+Enter is a new line, whether or not paths are offered
				return a, a.accept()
			}
		case tea.KeyEsc:
			a.complete.reqID++ // an answer still on its way is for a popup that was closed
			a.complete.items, a.complete.sel = nil, 0
			return a, nil
		}
	}
	before := string(a.input)
	m, cmd := a.typing(k)
	if string(a.input) != before {
		cmd = tea.Batch(cmd, a.refresh())
	}
	return m, cmd
}

// cancel asks the daemon to end the running turn.
func (a *App) cancel() tea.Cmd {
	b := a.Backend
	return func() tea.Msg { return CancelledMsg{Err: b.Cancel()} }
}

func (a *App) typing(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.Type {
	case tea.KeyCtrlC, tea.KeyCtrlD:
		a.quit = true
		return a, tea.Quit
	case tea.KeyEsc:
		// Esc ends the turn, a pending question included (the daemon denies it); the composer keeps its text.
		return a, a.cancel()
	}
	// While a question is open its keys answer it, and nothing else is typed: a letter the user meant for the composer must
	// not be taken for an answer after the question has gone, nor one meant as an answer land in the composer.
	if q, ok := a.Screen.Pending(); ok && (k.Type == tea.KeyRunes || k.Type == tea.KeySpace || k.Type == tea.KeyEnter) {
		decision, scope := "", "once"
		if k.Type == tea.KeyRunes && len(k.Runes) == 1 {
			switch unicode.ToLower(k.Runes[0]) {
			case 'a':
				decision = proto.DecisionAllow
			case 's':
				decision, scope = proto.DecisionAllow, "session"
			case 'd':
				decision = proto.DecisionDeny
			}
		}
		if decision == "" {
			return a, nil
		}
		b := a.Backend
		return a, func() tea.Msg { return AnsweredMsg{Err: b.Respond(q.ID, q.Seq, decision, scope)} }
	}
	switch k.Type {
	case tea.KeyCtrlJ:
		a.input = append(a.input, '\n')
	case tea.KeyEnter:
		if k.Alt {
			a.input = append(a.input, '\n')
			break
		}
		text := strings.TrimSpace(string(a.input))
		if text == "" {
			return a, nil
		}
		if strings.HasPrefix(text, "//") {
			text = text[1:] // a prompt that really starts with a slash
		} else if cmd, handled, known := a.runSlash(text); handled {
			if known {
				a.input = nil
			}
			return a, cmd
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
		runes := k.Runes
		if k.Paste { // a paste keeps its lines: CRLF and CR are one newline each, and every other control is still dropped
			runes = []rune(strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(string(runes)))
		}
		for _, r := range runes {
			if k.Paste && r == '\n' {
				a.input = append(a.input, r)
				continue
			}
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
	composer := a.composerRows()
	rows := a.popup()
	avail := a.height - len(composer)
	if len(rows) > avail-2 { // the popup never takes the screen: the transcript keeps at least two rows
		rows = rows[:max(avail-2, 0)]
	}
	body := a.Screen.Render(a.width, avail-len(rows))
	for _, r := range rows {
		body += "\n" + r
	}
	return body + "\n" + strings.Join(composer, "\n")
}

// maxComposerRows is the most lines the composer shows; the last ones, when it holds more.
const maxComposerRows = 5

// composerRows draws the composer's last lines: "> " before the first line of the text and two spaces before the rest, each
// cut from its left to fit the width, with the cursor mark after the last. It leaves the transcript at least one row.
func (a *App) composerRows() []string {
	lines := strings.Split(string(a.input), "\n")
	n := min(len(lines), maxComposerRows, max(a.height-1, 1))
	first := len(lines) - n
	out := make([]string, 0, n)
	for i := first; i < len(lines); i++ {
		line, room := "  "+lines[i], a.width
		if i == 0 {
			line = "> " + lines[i]
		}
		if i == len(lines)-1 {
			room-- // the cursor mark
		}
		for runewidth.StringWidth(line) > room {
			_, w := utf8.DecodeRuneInString(line)
			line = line[w:]
		}
		if i == len(lines)-1 {
			line += "▏"
		}
		out = append(out, line)
	}
	return out
}

// View renders Frame; it is the tea.Model method.
func (a *App) View() string { return a.Frame() }
