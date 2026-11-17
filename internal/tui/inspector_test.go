package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/townsendmerino/tack/internal/proto"
)

func usage(t *testing.T, v *View, seq int64, req, prompt, output, wallMS int, reused *int) {
	t.Helper()
	v.Apply(ev(t, seq, proto.EvUsage, proto.Usage{Request: req, Server: "goinfer@local", Prompt: prompt, Output: output, WallMS: wallMS, Reused: reused}))
}

// TestTheInspectorSumsTheSessionsRequests: requests, output tokens and wall time sum over the conversation's requests; the
// summarizer's (request 0) are not counted; the fill is the last prompt against the window a compaction reported; a
// compaction is counted.
func TestTheInspectorSumsTheSessionsRequests(t *testing.T) {
	var v View
	r := 600
	usage(t, &v, 1, 1, 4000, 100, 1500, nil)
	usage(t, &v, 2, 0, 9000, 900, 9000, nil) // the summarizer
	usage(t, &v, 3, 2, 6000, 50, 2500, &r)
	v.Apply(ev(t, 4, proto.EvCompaction, proto.Compaction{Request: 3, Trigger: "window", Prompt: 12000, Window: 16384}))
	got := v.Inspect()
	if got.Requests != 2 || got.Output != 150 || got.WallMS != 4000 || got.Prompt != 6000 || got.Window != 16384 || got.Compactions != 1 {
		t.Errorf("inspect %+v", got)
	}
	if got.Reused == nil || *got.Reused != 600 {
		t.Errorf("reused %v", got.Reused)
	}
}

// TestTheInspectorShowsTheCompositionWhenTheDaemonSendsOne: context.assembled's tokens by kind are listed largest first; with
// none seen, the pane says the breakdown waits for the daemon's ledger instead of showing nothing.
func TestTheInspectorShowsTheCompositionWhenTheDaemonSendsOne(t *testing.T) {
	a := &App{Backend: &fakeBackend{}}
	a.Update(tea.WindowSizeMsg{Width: 60, Height: 16})
	enter(a, "/inspector")
	if f := a.Frame(); !strings.Contains(f, "── inspector") || !strings.Contains(f, "ledger") {
		t.Errorf("no composition yet:\n%s", f)
	}
	usage(t, &a.Screen, 1, 1, 4000, 100, 1500, nil)
	a.Screen.Apply(ev(t, 2, proto.EvContextAssembled, proto.ContextAssembled{Tokens: map[string]int{"history": 2100, "tools": 1100, "instructions": 1900}, Total: 5100, Exact: true}))
	f := a.Frame()
	h, i, tl := strings.Index(f, "history"), strings.Index(f, "instructions"), strings.Index(f, "tools")
	if h < 0 || i < 0 || tl < 0 || !(h < i && i < tl) {
		t.Errorf("kinds missing or not largest first (history %d, instructions %d, tools %d):\n%s", h, i, tl, f)
	}
	if strings.Contains(f, "ledger") || !strings.Contains(f, "2 requests") && !strings.Contains(f, "1 request") {
		t.Errorf("frame:\n%s", f)
	}
}

// TestTheInspectorNeverTakesTheScreen: at every size the frame is exactly height lines.
func TestTheInspectorNeverTakesTheScreen(t *testing.T) {
	a := &App{Backend: &fakeBackend{}}
	a.Screen.Apply(ev(t, 1, proto.EvContextAssembled, proto.ContextAssembled{Tokens: map[string]int{"a": 1, "b": 2, "c": 3, "d": 4, "e": 5, "f": 6, "g": 7, "h": 8, "i": 9, "j": 10}, Total: 55}))
	a.Update(tea.WindowSizeMsg{Width: 30, Height: 16})
	enter(a, "/inspector")
	for _, h := range []int{16, 8, 5, 4, 3, 2} {
		a.Update(tea.WindowSizeMsg{Width: 30, Height: h})
		if n := len(strings.Split(a.Frame(), "\n")); n != h {
			t.Errorf("height %d: %d lines", h, n)
		}
	}
}

// TestInspectorRowsNeverExceedTheirRoom: at every room, the cut composition and its "more" line fit.
func TestInspectorRowsNeverExceedTheirRoom(t *testing.T) {
	i := Inspection{Requests: 2, Prompt: 4000, Compactions: 1, Total: 55,
		Composition: map[string]int{"a": 1, "b": 2, "c": 3, "d": 4, "e": 5, "f": 6, "g": 7, "h": 8, "i": 9, "j": 10}}
	for n := 1; n <= 20; n++ {
		if got := len(inspectorRows(i, 16384, n)); got > n {
			t.Errorf("room %d: %d rows", n, got)
		}
	}
	i.Composition = nil
	for n := 1; n <= 6; n++ {
		if got := len(inspectorRows(i, 0, n)); got > n {
			t.Errorf("no composition, room %d: %d rows", n, got)
		}
	}
}
