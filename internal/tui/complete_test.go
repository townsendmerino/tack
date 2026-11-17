package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/townsendmerino/tack/internal/proto"
)

// completing is an App with a backend that offers paths for a query, sized so the popup has room.
func completing(t *testing.T) (*App, *fakeBackend) {
	t.Helper()
	fb := &fakeBackend{items: map[string][]proto.CompletionItem{
		"":    {{Path: "a.go", Kind: "file"}},
		"ma":  {{Path: "cmd/main.go", Kind: "file"}, {Path: "internal/map", Kind: "dir"}},
		"mai": {{Path: "cmd/main.go", Kind: "file"}},
	}}
	a := &App{Backend: fb, Debounce: 1}
	a.Update(tea.WindowSizeMsg{Width: 60, Height: 14})
	return a, fb
}

// settle runs the commands a key returns (the debounce, then the completion call) and feeds their messages back, as the program
// would, until none is left.
func settle(a *App, cmd tea.Cmd) {
	for cmd != nil {
		msg := cmd()
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, c := range batch {
				settle(a, c)
			}
			return
		}
		_, cmd = a.Update(msg)
	}
}

// typeAndSettle types s one key at a time, settling each key's commands, so the last query is the one that completes.
func typeAndSettle(a *App, s string) {
	for _, r := range s {
		k := rune1(r)
		if r == ' ' {
			k = tea.KeyMsg{Type: tea.KeySpace}
		}
		_, cmd := a.Update(k)
		settle(a, cmd)
	}
}

// TestAtOffersPathsAndTabAcceptsOne: typing @ and some letters asks the backend for that query and shows the paths above the
// composer; Down moves the selection and Tab puts the chosen path into the composer, replacing the @ word, with a space after a
// file and a slash after a directory.
func TestAtOffersPathsAndTabAcceptsOne(t *testing.T) {
	a, fb := completing(t)
	typeAndSettle(a, "look at @ma")
	if f := a.Frame(); !strings.Contains(f, "cmd/main.go") || !strings.Contains(f, "internal/map/") {
		t.Fatalf("no popup:\n%s", f)
	}
	if got := fb.queries[len(fb.queries)-1]; got != "ma" {
		t.Errorf("last query %q", got)
	}
	_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyDown})
	settle(a, cmd)
	_, cmd = a.Update(tea.KeyMsg{Type: tea.KeyTab})
	settle(a, cmd)
	if string(a.input) != "look at @internal/map/" {
		t.Errorf("composer %q after accepting a directory", string(a.input))
	}
	typeAndSettle(a, "")
	a, _ = completing(t)
	typeAndSettle(a, "@ma")
	_, cmd = a.Update(tea.KeyMsg{Type: tea.KeyTab})
	settle(a, cmd)
	if string(a.input) != "@cmd/main.go " {
		t.Errorf("composer %q after accepting a file", string(a.input))
	}
	if f := a.Frame(); strings.Contains(f, "internal/map") {
		t.Errorf("the popup stayed after accepting:\n%s", f)
	}
}

// TestAnAnswerForAnOlderQueryIsDropped: completions come back in any order, and one for a query the user has typed past shows
// nothing; only the latest request's answer is shown.
func TestAnAnswerForAnOlderQueryIsDropped(t *testing.T) {
	a, _ := completing(t)
	_, c1 := a.Update(rune1('@'))
	_, c2 := a.Update(rune1('m'))
	_, c3 := a.Update(rune1('a'))
	// The latest request's debounce and call run first, the older ones after it, and the older ones' answers are dropped.
	settle(a, c3)
	settle(a, c1)
	settle(a, c2)
	if f := a.Frame(); !strings.Contains(f, "cmd/main.go") {
		t.Errorf("the latest answer was lost:\n%s", f)
	}
	if got := len(a.complete.items); got != 2 {
		t.Errorf("%d items, want the query ma's 2", got)
	}
}

// TestNoAtNoCompletion: text without an @ word asks the backend nothing, and an @ inside a word (an address) is not a path.
func TestNoAtNoCompletion(t *testing.T) {
	a, fb := completing(t)
	typeAndSettle(a, "mail me at a@b.c now")
	if len(fb.queries) != 0 {
		t.Errorf("queries %q", fb.queries)
	}
}

// TestEscClosesThePopupBeforeItCancels: with the popup open Esc closes it and cancels nothing; the next Esc cancels the turn.
func TestEscClosesThePopupBeforeItCancels(t *testing.T) {
	a, fb := completing(t)
	typeAndSettle(a, "@ma")
	press(a, tea.KeyMsg{Type: tea.KeyEsc})
	if fb.cancels != 0 || len(a.complete.items) != 0 {
		t.Fatalf("cancels %d, items %d", fb.cancels, len(a.complete.items))
	}
	press(a, tea.KeyMsg{Type: tea.KeyEsc})
	if fb.cancels != 1 {
		t.Errorf("cancels %d", fb.cancels)
	}
}

// TestEnterAcceptsWhileThePopupIsOpenAndSendsOtherwise: Enter with the popup open takes the selection and sends nothing.
func TestEnterAcceptsWhileThePopupIsOpenAndSendsOtherwise(t *testing.T) {
	a, fb := completing(t)
	typeAndSettle(a, "@mai")
	press(a, tea.KeyMsg{Type: tea.KeyEnter})
	if len(fb.sent) != 0 || string(a.input) != "@cmd/main.go " {
		t.Errorf("sent %q, composer %q", fb.sent, string(a.input))
	}
	press(a, tea.KeyMsg{Type: tea.KeyEnter})
	if len(fb.sent) != 1 || fb.sent[0] != "@cmd/main.go" {
		t.Errorf("sent %q", fb.sent)
	}
}

// TestACompletionThatFailsIsNoteOnceAndOldDaemonTurnsItOff: an older daemon (method not found) says completion is off, once,
// and later @ words ask nothing; another error is a note and completion keeps trying.
func TestACompletionThatFailsIsNoteOnceAndOldDaemonTurnsItOff(t *testing.T) {
	a, fb := completing(t)
	fb.completeErr = &proto.Error{Code: proto.CodeMethodNotFound, Message: "x"}
	typeAndSettle(a, "@m")
	n := len(fb.queries)
	typeAndSettle(a, "a")
	if len(fb.queries) != n {
		t.Errorf("asked again after method not found: %q", fb.queries)
	}
	if f := a.Frame(); strings.Count(f, "completion is off") != 1 {
		t.Errorf("note:\n%s", f)
	}
	a, fb = completing(t)
	fb.completeErr = errors.New("pipe closed")
	typeAndSettle(a, "@m")
	typeAndSettle(a, "a")
	if len(fb.queries) < 2 {
		t.Errorf("gave up after a transient error: %q", fb.queries)
	}
}

// TestALateAnswerIsDroppedEvenAfterItsCallWasIssued: a completion call already in flight when the user types on is answered
// after, and its answer, for the older query, shows nothing.
func TestALateAnswerIsDroppedEvenAfterItsCallWasIssued(t *testing.T) {
	a, _ := completing(t)
	_, c := a.Update(rune1('@'))
	_, call := a.Update(c()) // the debounce fired: the call for the query "" is issued
	_, next := a.Update(rune1('x'))
	late := call() // its answer comes back after the user typed on
	a.Update(late)
	if len(a.complete.items) != 0 {
		t.Errorf("the older query's answer was shown: %+v", a.complete.items)
	}
	settle(a, next)
}
