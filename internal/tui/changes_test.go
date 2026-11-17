package tui

import (
	"encoding/json"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/townsendmerino/tack/internal/proto"
)

// edited folds an edit or write call into v: the call, its pre-image checkpoint, and its finish.
func edited(t *testing.T, v *View, seq int64, id, tool, path string, absent bool, outcome string) int64 {
	t.Helper()
	v.Apply(ev(t, seq, proto.EvToolCall, proto.ToolCall{ID: id, Name: tool, Input: json.RawMessage(`{"path":"` + path + `"}`)}))
	img := proto.FileImage{Path: path, Absent: absent}
	if !absent {
		img.Blob = "sha256:" + strings.Repeat("0", 64)
	}
	v.Apply(ev(t, seq+1, proto.EvCheckpoint, proto.Checkpoint{ID: id, Workspace: "/w", Files: []proto.FileImage{img}}))
	v.Apply(ev(t, seq+2, proto.EvToolFinished, proto.ToolFinished{ID: id, Outcome: outcome, Result: "x"}))
	return seq + 3
}

// TestChangesAreTheFilesEditsAndWritesChanged: per file, in the order each was first changed, with how many finished edits and
// whether the session made the file; a call that failed or was denied changes nothing, and a read, a bash call and a snapshot
// checkpoint are not changes.
func TestChangesAreTheFilesEditsAndWritesChanged(t *testing.T) {
	var v View
	seq := edited(t, &v, 1, "e1", "edit", "a.go", false, proto.OutcomeOK)
	seq = edited(t, &v, seq, "w1", "write", "new/b.go", true, proto.OutcomeOK)
	seq = edited(t, &v, seq, "e2", "edit", "a.go", false, proto.OutcomeOK)
	seq = edited(t, &v, seq, "e3", "edit", "c.go", false, proto.OutcomeError)
	seq = edited(t, &v, seq, "e4", "edit", "d.go", false, proto.OutcomeDenied)
	seq = edited(t, &v, seq, "e5", "edit", "new/b.go", false, proto.OutcomeOK)
	v.Apply(ev(t, seq, proto.EvToolCall, proto.ToolCall{ID: "b1", Name: "bash", Input: json.RawMessage(`{"command":"ls"}`)}))
	v.Apply(ev(t, seq+1, proto.EvCheckpoint, proto.Checkpoint{ID: "b1", Workspace: "/w", Snapshot: "abc"}))
	v.Apply(ev(t, seq+2, proto.EvToolFinished, proto.ToolFinished{ID: "b1", Outcome: proto.OutcomeOK}))
	got := v.Changes()
	want := []Change{{Path: "a.go", Edits: 2}, {Path: "new/b.go", Edits: 2, New: true}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("changes %+v, want %+v", got, want)
	}
}

// TestAWriteOverAFileIsNotNew: a write whose pre-image exists changes a file that was there.
func TestAWriteOverAFileIsNotNew(t *testing.T) {
	var v View
	edited(t, &v, 1, "w1", "write", "a.go", false, proto.OutcomeOK)
	if got := v.Changes(); len(got) != 1 || got[0].New {
		t.Errorf("changes %+v", got)
	}
}

// TestSlashChangesTogglesThePane: /changes lists each file with its edit count and marks one the session made; with none it says
// so; a rewind note does not clear it.
func TestSlashChangesTogglesThePane(t *testing.T) {
	a := &App{Backend: &fakeBackend{}}
	a.Update(tea.WindowSizeMsg{Width: 50, Height: 14})
	enter(a, "/changes")
	if rows := a.paneRows(10); len(rows) != 2 || !strings.HasPrefix(rows[0], "── changes") || rows[1] != "no changes yet" {
		t.Errorf("empty pane: %q", rows)
	}
	enter(a, "/changes")
	if rows := a.paneRows(10); len(rows) != 0 {
		t.Errorf("the pane stayed: %q", rows)
	}
	seq := edited(t, &a.Screen, 1, "e1", "edit", "a.go", false, proto.OutcomeOK)
	edited(t, &a.Screen, seq, "w1", "write", "new/b.go", true, proto.OutcomeOK)
	enter(a, "/changes")
	// The pane's own rows, not the frame: the transcript also names both files, and "new" is a part of "new/b.go".
	rows := a.paneRows(10)
	want := []string{"a.go  1 edit", "new/b.go  1 edit  new"}
	if len(rows) != 3 || !strings.HasPrefix(rows[0], "── changes") || rows[1] != want[0] || rows[2] != want[1] {
		t.Fatalf("pane rows %q, want a header then %q", rows, want)
	}
	if f := a.Frame(); !strings.Contains(f, strings.Join(rows, "\n")) {
		t.Errorf("the frame does not draw the pane's rows together:\n%s", f)
	}
	enter(a, "/todos")
	rows = a.paneRows(10)
	if len(rows) == 0 || !strings.HasPrefix(rows[0], "── todos") {
		t.Fatalf("another pane did not replace it: %q", rows)
	}
	for _, r := range rows {
		if strings.Contains(r, "edit") {
			t.Errorf("the replaced pane's row is still there: %q", r)
		}
	}
}

// TestChangeRowsPluralAndNewMarks: two edits say "2 edits", and only a file the session made says "new".
func TestChangeRowsPluralAndNewMarks(t *testing.T) {
	got := changeRows([]Change{{Path: "a.go", Edits: 2}, {Path: "b.go", Edits: 1, New: true}, {Path: "c.go", Edits: 3, New: true}}, 10)
	want := []string{"a.go  2 edits", "b.go  1 edit  new", "c.go  3 edits  new"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("rows %q, want %q", got, want)
	}
}

// TestChangeRowsShowAFileWhenThereIsRoomForOneLine: with room for one line the first file shows, not only a count; with none there
// are no rows.
func TestChangeRowsShowAFileWhenThereIsRoomForOneLine(t *testing.T) {
	list := []Change{{Path: "a.go", Edits: 1}, {Path: "b.go", Edits: 1}, {Path: "c.go", Edits: 1}}
	if got := changeRows(list, 1); len(got) != 1 || got[0] != "a.go  1 edit" {
		t.Errorf("room 1: %q", got)
	}
	for _, n := range []int{0, -1} {
		if got := changeRows(list, n); len(got) != 0 {
			t.Errorf("room %d: %q", n, got)
		}
		if got := changeRows(nil, n); len(got) != 0 {
			t.Errorf("none, room %d: %q", n, got)
		}
	}
	if got := changeRows(list, 2); len(got) != 2 || got[1] != "… 2 more" {
		t.Errorf("room 2: %q", got)
	}
}

// TestChangeRowsNeverExceedTheirRoom: a list cut to n rows, its "more" line included, is at most n lines at every n.
func TestChangeRowsNeverExceedTheirRoom(t *testing.T) {
	var list []Change
	for i := 0; i < 12; i++ {
		list = append(list, Change{Path: "f.go", Edits: 1})
	}
	for n := 1; n <= 14; n++ {
		if got := len(changeRows(list, n)); got > n {
			t.Errorf("room %d: %d rows", n, got)
		}
	}
}
