package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"github.com/townsendmerino/tack/internal/proto"
)

type fakeBackend struct {
	sent []string
	err  error
}

func (f *fakeBackend) Prompt(text string) error { f.sent = append(f.sent, text); return f.err }

func typed(a *App, s string) {
	for _, r := range s {
		switch r {
		case ' ':
			a.Update(tea.KeyMsg{Type: tea.KeySpace})
		default:
			a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		}
	}
}

// run executes a command's message and feeds it back, as the program would.
func run(a *App, cmd tea.Cmd) {
	if cmd != nil {
		a.Update(cmd())
	}
}

// TestEnterSendsTheComposerThroughTheBackend: the typed text goes to the backend as one prompt, the composer empties, an
// empty composer sends nothing, and a refused prompt shows as a note in the transcript.
func TestEnterSendsTheComposerThroughTheBackend(t *testing.T) {
	fb := &fakeBackend{}
	a := &App{Backend: fb}
	a.Update(tea.WindowSizeMsg{Width: 40, Height: 10})
	_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("an empty composer sent a command")
	}
	typed(a, "where is A?")
	if f := a.Frame(); !strings.Contains(f, "> where is A?") {
		t.Fatalf("the composer is not drawn:\n%s", f)
	}
	_, cmd = a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	run(a, cmd)
	if len(fb.sent) != 1 || fb.sent[0] != "where is A?" || len(a.input) != 0 {
		t.Fatalf("sent %q, composer %q", fb.sent, string(a.input))
	}
	fb.err = errors.New("daemon gone")
	typed(a, "again")
	_, cmd = a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	run(a, cmd)
	if f := a.Frame(); !strings.Contains(f, "daemon gone") {
		t.Fatalf("a refused prompt is not shown:\n%s", f)
	}
}

// TestKeysCannotTypeAControl: a pasted control, bidi or zero-width rune never enters the composer; backspace removes one
// rune, and ctrl-c quits.
func TestKeysCannotTypeAControl(t *testing.T) {
	a := &App{Backend: &fakeBackend{}}
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a\x1b[2J‮b​c\x9b")})
	if got := string(a.input); got != "a[2Jbc" {
		t.Fatalf("composer %q", got)
	}
	a.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if got := string(a.input); got != "a[2Jb" {
		t.Fatalf("after backspace %q", got)
	}
	if _, cmd := a.Update(tea.KeyMsg{Type: tea.KeyCtrlC}); cmd == nil || !a.quit {
		t.Fatal("ctrl-c did not quit")
	}
}

// TestEventsDrawAndTheFrameFitsTheTerminal: an event reaches the transcript, and the whole frame, composer included, is
// exactly height lines of at most width cells at every size, with a wide composer cut from its left.
func TestEventsDrawAndTheFrameFitsTheTerminal(t *testing.T) {
	a := &App{Backend: &fakeBackend{}}
	a.Update(EventMsg(ev(t, 1, proto.EvUserMessage, proto.UserMessage{Content: []proto.ContentBlock{proto.Text("日本語 hello")}})))
	typed(a, strings.Repeat("日本", 30))
	for _, sz := range [][2]int{{80, 24}, {20, 6}, {10, 3}} {
		a.Update(tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})
		lines := strings.Split(a.Frame(), "\n")
		if len(lines) != sz[1] {
			t.Fatalf("%dx%d: %d lines", sz[0], sz[1], len(lines))
		}
		for i, l := range lines {
			if n := runewidth.StringWidth(l); n > sz[0] {
				t.Fatalf("%dx%d: line %d is %d cells: %q", sz[0], sz[1], i, n, l)
			}
		}
	}
	a.Update(tea.WindowSizeMsg{Width: 40, Height: 8})
	if f := a.Frame(); !strings.Contains(f, "日本語 hello") {
		t.Fatalf("the event is not drawn:\n%s", f)
	}
}
