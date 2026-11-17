package tui

import (
	"errors"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/townsendmerino/tack/internal/proto"
)

// debounce is how long the composer waits after a key before asking the daemon for paths (proposal-tui-protocol.md §3).
const debounce = 50 * time.Millisecond

// popupRows is the most paths drawn above the composer.
const popupRows = 5

// completion is the state of `@` completion. reqID numbers the requests: every change of the @ word makes a new one, and an
// answer for an older one is dropped, so a slow answer never replaces a newer one.
type completion struct {
	reqID int
	items []proto.CompletionItem
	sel   int
	off   bool // the daemon has no workspace/complete: ask nothing more, and say so once
}

// debounceMsg fires when a request's wait is over; it is a request only if id is still the latest.
type debounceMsg struct {
	id    int
	query string
}

// completedMsg is a request's answer.
type completedMsg struct {
	id    int
	items []proto.CompletionItem
	err   error
}

// atWord finds the @ word the composer ends in: an @ at the start or after a space, with no space after it. It returns the
// index of the @ and the text after it. An @ inside a word, as in an address, is not a path.
func atWord(input []rune) (start int, query string, ok bool) {
	for i := len(input) - 1; i >= 0; i-- {
		switch {
		case input[i] == ' ':
			return 0, "", false
		case input[i] == '@' && (i == 0 || input[i-1] == ' '):
			return i, string(input[i+1:]), true
		}
	}
	return 0, "", false
}

// refresh starts a request for the composer's @ word, after the debounce, or closes the popup when there is none. Any call
// supersedes the requests before it.
func (a *App) refresh() tea.Cmd {
	c := &a.complete
	c.reqID++
	c.items, c.sel = nil, 0
	_, query, ok := atWord(a.input)
	if !ok || c.off || a.Backend == nil {
		return nil
	}
	id, wait := c.reqID, a.Debounce
	if wait == 0 {
		wait = debounce
	}
	return tea.Tick(wait, func(time.Time) tea.Msg { return debounceMsg{id: id, query: query} })
}

// debounced issues the call for a request whose wait is over, if no later request has superseded it.
func (a *App) debounced(m debounceMsg) tea.Cmd {
	if m.id != a.complete.reqID {
		return nil
	}
	b := a.Backend
	return func() tea.Msg {
		items, err := b.Complete(m.query)
		return completedMsg{id: m.id, items: items, err: err}
	}
}

// completed shows a request's answer if it is still the latest.
func (a *App) completed(m completedMsg) {
	c := &a.complete
	if m.id != c.reqID {
		return
	}
	var pe *proto.Error
	switch {
	case errors.As(m.err, &pe) && pe.Code == proto.CodeMethodNotFound:
		c.off = true
		a.Screen.note("! completion is off: the daemon is older than this screen; restart it")
	case m.err != nil:
		a.Screen.note("! completion: " + m.err.Error())
	default:
		c.items, c.sel = m.items, 0
	}
}

// accept puts the selected path into the composer in place of the @ word: a space after a file, a slash after a directory so
// the word goes on into it.
func (a *App) accept() tea.Cmd {
	c := &a.complete
	if len(c.items) == 0 {
		return nil
	}
	it := c.items[c.sel]
	start, _, ok := atWord(a.input)
	if !ok {
		return a.refresh()
	}
	tail := " "
	if it.Kind == "dir" {
		tail = "/"
	}
	a.input = append(a.input[:start:start], []rune("@"+strings.TrimSuffix(it.Path, "/")+tail)...)
	return a.refresh()
}

// popup draws the offered paths, the selected one marked, for the rows above the composer.
func (a *App) popup() []string {
	c := &a.complete
	n := min(len(c.items), popupRows)
	if n == 0 {
		return nil
	}
	first := 0
	if c.sel >= n {
		first = c.sel - n + 1
	}
	var out []string
	for i := first; i < first+n; i++ {
		mark, name := "  ", c.items[i].Path
		if c.items[i].Kind == "dir" {
			name += "/"
		}
		if i == c.sel {
			mark = "▸ "
		}
		out = append(out, mark+name)
	}
	return out
}
