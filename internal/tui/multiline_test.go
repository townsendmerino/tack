package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"
)

// TestAltEnterAndCtrlJStartANewLineAndEnterSendsAll: Alt+Enter and Ctrl+J put a newline in the composer, plain Enter sends the
// whole text with its newlines, and backspace removes a newline like any rune.
func TestAltEnterAndCtrlJStartANewLineAndEnterSendsAll(t *testing.T) {
	for _, nl := range []tea.KeyMsg{{Type: tea.KeyEnter, Alt: true}, {Type: tea.KeyCtrlJ}} {
		fb := &fakeBackend{}
		a := &App{Backend: fb}
		a.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
		typed(a, "first")
		a.Update(nl)
		typed(a, "second")
		if got := string(a.input); got != "first\nsecond" {
			t.Fatalf("%v: composer %q", nl, got)
		}
		if f := a.Frame(); !strings.Contains(f, "> first") || !strings.Contains(f, "  second") {
			t.Fatalf("%v: both lines are not drawn:\n%s", nl, f)
		}
		press(a, tea.KeyMsg{Type: tea.KeyEnter})
		if len(fb.sent) != 1 || fb.sent[0] != "first\nsecond" || len(a.input) != 0 {
			t.Errorf("%v: sent %q, composer %q", nl, fb.sent, string(a.input))
		}
	}
	a := &App{Backend: &fakeBackend{}}
	typed(a, "a")
	a.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	a.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if string(a.input) != "a" {
		t.Errorf("backspace over a newline: %q", string(a.input))
	}
}

// TestANewlineAloneSendsNothing: a composer of only newlines and spaces is empty.
func TestANewlineAloneSendsNothing(t *testing.T) {
	fb := &fakeBackend{}
	a := &App{Backend: fb}
	a.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || len(fb.sent) != 0 {
		t.Errorf("sent %q", fb.sent)
	}
}

// TestAPasteKeepsItsLinesButNoOtherControl: a bracketed paste keeps newlines (a CR or CRLF becomes one) and still drops every
// other control rune; the same newline typed as a plain key rune is dropped, since only the keys above make one.
func TestAPasteKeepsItsLinesButNoOtherControl(t *testing.T) {
	a := &App{Backend: &fakeBackend{}}
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Paste: true, Runes: []rune("one\r\ntwo\rthree\nfour\x1b[2J\u202e")})
	if got := string(a.input); got != "one\ntwo\nthree\nfour[2J" {
		t.Errorf("pasted %q", got)
	}
	b := &App{Backend: &fakeBackend{}}
	b.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a\nb")})
	if got := string(b.input); got != "ab" {
		t.Errorf("typed %q", got)
	}
}

// TestTheComposerGrowsAndTheFrameStillFits: the composer takes up to five rows, showing its last lines with the cursor at the
// end; the whole frame is exactly height lines of at most width cells at every size, and the transcript keeps a row.
func TestTheComposerGrowsAndTheFrameStillFits(t *testing.T) {
	a := &App{Backend: &fakeBackend{}}
	for i := 1; i <= 8; i++ {
		typed(a, strings.Repeat("日", i))
		a.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	}
	typed(a, "last")
	for _, sz := range [][2]int{{80, 24}, {20, 8}, {10, 4}, {10, 3}, {10, 2}} {
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
		if !strings.HasSuffix(a.Frame(), "last▏") {
			t.Errorf("%dx%d: the cursor end is not last:\n%s", sz[0], sz[1], a.Frame())
		}
	}
	a.Update(tea.WindowSizeMsg{Width: 40, Height: 24})
	f := a.Frame()
	if strings.Count(f, "日") > 0 && strings.Contains(f, "> 日\n") {
		t.Errorf("the first line shows although only the last five fit:\n%s", f)
	}
	if rows := strings.Count(f, "\n  ") + strings.Count(f, "\n> "); rows > 5 {
		t.Errorf("%d composer rows", rows)
	}
}

// TestAnAtWordAfterANewlineCompletes: a newline separates words like a space, so an @ at the start of a later line completes.
func TestAnAtWordAfterANewlineCompletes(t *testing.T) {
	a, fb := completing(t)
	typeAndSettle(a, "see")
	_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	settle(a, cmd)
	typeAndSettle(a, "@ma")
	if len(fb.queries) == 0 || fb.queries[len(fb.queries)-1] != "ma" {
		t.Fatalf("queries %q", fb.queries)
	}
	_, cmd = a.Update(tea.KeyMsg{Type: tea.KeyTab})
	settle(a, cmd)
	if string(a.input) != "see\n@cmd/main.go " {
		t.Errorf("composer %q", string(a.input))
	}
}

// TestAltEnterWithThePopupOpenIsANewlineNotAnAccept: Enter takes the selection, Alt+Enter still starts a line.
func TestAltEnterWithThePopupOpenIsANewlineNotAnAccept(t *testing.T) {
	a, _ := completing(t)
	typeAndSettle(a, "@ma")
	a.Update(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	if !strings.HasSuffix(string(a.input), "\n") {
		t.Errorf("composer %q", string(a.input))
	}
}
