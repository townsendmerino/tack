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
	if f := a.Frame(); !strings.Contains(f, "no changes") {
		t.Errorf("empty:\n%s", f)
	}
	enter(a, "/changes")
	seq := edited(t, &a.Screen, 1, "e1", "edit", "a.go", false, proto.OutcomeOK)
	edited(t, &a.Screen, seq, "w1", "write", "new/b.go", true, proto.OutcomeOK)
	enter(a, "/changes")
	f := a.Frame()
	for _, want := range []string{"── changes", "a.go", "1 edit", "new/b.go", "new"} {
		if !strings.Contains(f, want) {
			t.Errorf("no %q:\n%s", want, f)
		}
	}
	enter(a, "/todos")
	if f := a.Frame(); strings.Contains(f, "1 edit") || !strings.Contains(f, "── todos") {
		t.Errorf("another pane did not replace it:\n%s", f)
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
