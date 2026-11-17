package tui

import (
	"fmt"
	"sort"

	"github.com/townsendmerino/tack/internal/proto"
)

// Inspection is what the inspector pane shows (DESIGN §11.2's Context and Usage tabs, as far as the events carry them): the
// conversation's requests with their output tokens and wall time, the last prompt against the window, how much of it the
// server reused, compactions, and the composition by kind when the daemon sends one.
type Inspection struct {
	Requests    int
	Output      int
	WallMS      int
	Prompt      int  // the last request's prompt tokens
	Reused      *int // of them, the ones the server reused; nil when it did not say
	Window      int  // the window a compaction last reported, 0 before one
	Compactions int
	Composition map[string]int // tokens by kind from the last context.assembled; nil before one
	Total       int
}

// inspectState folds usage, compaction and context.assembled events. A request 0 (the summarizer, outside the conversation) is
// not the conversation's and counts nowhere.
type inspectState struct{ i Inspection }

func (s *inspectState) apply(p any) {
	switch e := p.(type) {
	case proto.Usage:
		if e.Request == 0 {
			return
		}
		s.i.Requests++
		s.i.Output += e.Output
		s.i.WallMS += e.WallMS
		s.i.Prompt, s.i.Reused = e.Prompt, e.Reused
	case proto.Compaction:
		s.i.Compactions++
		if e.Window > 0 {
			s.i.Window = e.Window
		}
	case proto.ContextAssembled:
		s.i.Composition, s.i.Total = e.Tokens, e.Total
	}
}

// Inspect is the session's inspection as of the events folded so far.
func (v *View) Inspect() Inspection { return v.inspect.i }

// inspectorRows draws the inspection in at most rows lines.
func inspectorRows(i Inspection, window, rows int) []string {
	if i.Window > 0 {
		window = i.Window
	}
	var out []string
	req := fmt.Sprintf("%d requests", i.Requests)
	if i.Requests == 1 {
		req = "1 request"
	}
	out = append(out, fmt.Sprintf("%s · %d output tokens · %.1f s", req, i.Output, float64(i.WallMS)/1000))
	if i.Prompt > 0 {
		fill := "window " + kilo(i.Prompt)
		if window > 0 {
			fill += "/" + kilo(window)
		}
		if i.Reused != nil {
			fill += fmt.Sprintf(" · %.0f%% reused", 100*float64(*i.Reused)/float64(i.Prompt))
		}
		out = append(out, fill)
	}
	if i.Compactions > 0 {
		out = append(out, fmt.Sprintf("%d compactions", i.Compactions))
	}
	if i.Composition == nil {
		out = append(out, "composition by kind: waits for the daemon's context ledger")
		return out[:min(len(out), rows)]
	}
	kinds := make([]string, 0, len(i.Composition))
	for k := range i.Composition {
		kinds = append(kinds, k)
	}
	sort.Slice(kinds, func(a, b int) bool {
		if i.Composition[kinds[a]] != i.Composition[kinds[b]] {
			return i.Composition[kinds[a]] > i.Composition[kinds[b]]
		}
		return kinds[a] < kinds[b]
	})
	for _, k := range kinds {
		out = append(out, fmt.Sprintf("  %-14s %s", k, kilo(i.Composition[k])))
	}
	if len(out) > rows {
		out = append(out[:max(rows-1, 0)], fmt.Sprintf("… %d more", len(out)-max(rows-1, 0)))
	}
	return out
}
