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

// TestInspectorRowsRender: the exact lines for a session with a window, a reuse figure, a compaction and a composition, with the
// reused share rounded to a whole percent and the kinds largest first; the same without a window, without a reuse figure (the
// server did not say), with one request, and with no composition yet.
func TestInspectorRowsRender(t *testing.T) {
	r := 3000
	full := Inspection{Requests: 3, Output: 1234, WallMS: 8750, Prompt: 6000, Reused: &r, Window: 16384, Compactions: 2,
		Composition: map[string]int{"tools": 1100, "history": 2100, "instructions": 1900}, Total: 5100}
	want := []string{
		"3 requests · 1234 output tokens · 8.8 s",
		"window 6.0k/16.4k · 50% reused",
		"2 compactions",
		"  history        2.1k",
		"  instructions   1.9k",
		"  tools          1.1k",
	}
	if got := inspectorRows(full, 0, 20); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("full:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// The status line's window is the fallback until a compaction reports one.
	noWin := full
	noWin.Window = 0
	if got := inspectorRows(noWin, 8192, 20); got[1] != "window 6.0k/8.2k · 50% reused" {
		t.Errorf("fallback window: %q", got[1])
	}
	if got := inspectorRows(noWin, 0, 20); got[1] != "window 6.0k · 50% reused" {
		t.Errorf("no window at all: %q", got[1])
	}
	noReuse := full
	noReuse.Reused = nil
	if got := inspectorRows(noReuse, 0, 20); got[1] != "window 6.0k/16.4k" {
		t.Errorf("reuse not reported: %q", got[1])
	}
	zero := 0
	noReuse.Reused = &zero
	if got := inspectorRows(noReuse, 0, 20); got[1] != "window 6.0k/16.4k · 0% reused" {
		t.Errorf("reuse of none: %q", got[1])
	}
	bare := Inspection{Requests: 1, Output: 10, WallMS: 500}
	want = []string{"1 request · 10 output tokens · 0.5 s", "composition by kind: waits for the daemon's context ledger"}
	if got := inspectorRows(bare, 0, 20); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("bare:\n%s", strings.Join(got, "\n"))
	}
}

// TestInspectorRowsCutAtOneLineAndNone: with room for one line the first line shows, with none there are no rows, and a cut
// composition ends with how many lines it left out.
func TestInspectorRowsCutAtOneLineAndNone(t *testing.T) {
	i := Inspection{Requests: 2, Output: 5, WallMS: 1000, Prompt: 4000,
		Composition: map[string]int{"a": 1, "b": 2, "c": 3, "d": 4}}
	if got := inspectorRows(i, 0, 1); len(got) != 1 || !strings.HasPrefix(got[0], "2 requests") {
		t.Errorf("room 1: %q", got)
	}
	for _, n := range []int{0, -3} {
		if got := inspectorRows(i, 0, n); len(got) != 0 {
			t.Errorf("room %d: %q", n, got)
		}
	}
	got := inspectorRows(i, 0, 4) // two summary lines, then room for two kinds and the count
	if len(got) != 4 || got[2] != "  d              0.0k" || !strings.HasPrefix(got[3], "… ") || !strings.HasSuffix(got[3], " more") {
		t.Errorf("room 4: %q", got)
	}
}
