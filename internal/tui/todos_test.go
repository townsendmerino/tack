package tui

import (
	"encoding/json"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/townsendmerino/tack/internal/proto"
)

func todoCall(t *testing.T, v *View, seq int64, id, items string, outcome string) {
	t.Helper()
	v.Apply(ev(t, seq, proto.EvToolCall, proto.ToolCall{ID: id, Name: "todo", Input: json.RawMessage(`{"items":` + items + `}`)}))
	v.Apply(ev(t, seq+1, proto.EvToolFinished, proto.ToolFinished{ID: id, Outcome: outcome, Result: "x"}))
}

// TestTodosAreTheLastSuccessfulTodoCall: the list is the last todo call that succeeded; a call the tool refused (two items in
// progress, a bad status) leaves the earlier list, and an empty list empties it.
func TestTodosAreTheLastSuccessfulTodoCall(t *testing.T) {
	var v View
	if len(v.Todos()) != 0 {
		t.Fatal("a fresh view has todos")
	}
	todoCall(t, &v, 1, "t1", `[{"text":"read a.go","status":"done"},{"text":"fix it","status":"in_progress"},{"text":"test","status":"pending"}]`, proto.OutcomeOK)
	got := v.Todos()
	if len(got) != 3 || got[0] != (Todo{"read a.go", "done"}) || got[1].Status != "in_progress" || got[2].Text != "test" {
		t.Fatalf("todos %+v", got)
	}
	todoCall(t, &v, 3, "t2", `[{"text":"x","status":"in_progress"},{"text":"y","status":"in_progress"}]`, proto.OutcomeError)
	if len(v.Todos()) != 3 {
		t.Errorf("a refused call replaced the list: %+v", v.Todos())
	}
	todoCall(t, &v, 5, "t3", `[]`, proto.OutcomeOK)
	if len(v.Todos()) != 0 {
		t.Errorf("an empty list did not empty it: %+v", v.Todos())
	}
}

// TestACallStillRunningIsNotTheList: a todo call with no tool.finished yet, and one finished cancelled or denied, changes nothing.
func TestACallStillRunningIsNotTheList(t *testing.T) {
	var v View
	v.Apply(ev(t, 1, proto.EvToolCall, proto.ToolCall{ID: "t1", Name: "todo", Input: json.RawMessage(`{"items":[{"text":"a","status":"pending"}]}`)}))
	if len(v.Todos()) != 0 {
		t.Error("an unfinished call is the list")
	}
	v.Apply(ev(t, 2, proto.EvToolFinished, proto.ToolFinished{ID: "t1", Outcome: proto.OutcomeCancelled}))
	if len(v.Todos()) != 0 {
		t.Error("a cancelled call is the list")
	}
}

// TestSlashTodosTogglesThePane: /todos draws the list in a pane above the composer, marked by status, and /todos again hides it;
// the frame still fits at every size.
func TestSlashTodosTogglesThePane(t *testing.T) {
	a := &App{Backend: &fakeBackend{}}
	a.Update(tea.WindowSizeMsg{Width: 40, Height: 14})
	todoCall(t, &a.Screen, 1, "t1", `[{"text":"read a.go","status":"done"},{"text":"fix it","status":"in_progress"},{"text":"test","status":"pending"}]`, proto.OutcomeOK)
	enter(a, "/todos")
	f := a.Frame()
	for _, want := range []string{"todos", "[x] read a.go", "[>] fix it", "[ ] test"} {
		if !strings.Contains(f, want) {
			t.Errorf("no %q:\n%s", want, f)
		}
	}
	if n := len(strings.Split(f, "\n")); n != 14 {
		t.Errorf("%d lines", n)
	}
	enter(a, "/todos")
	if f := a.Frame(); strings.Contains(f, "fix it") {
		t.Errorf("the pane stayed:\n%s", f)
	}
}

// TestTheTodoPaneSaysWhenThereAreNoneAndNeverTakesTheScreen: an empty list says so; a long one is cut to the pane's rows and the
// frame keeps its transcript rows and its exact size even on a small terminal.
func TestTheTodoPaneSaysWhenThereAreNoneAndNeverTakesTheScreen(t *testing.T) {
	a := &App{Backend: &fakeBackend{}}
	a.Update(tea.WindowSizeMsg{Width: 30, Height: 12})
	enter(a, "/todos")
	if f := a.Frame(); !strings.Contains(f, "no tasks") {
		t.Errorf("empty pane:\n%s", f)
	}
	var items []string
	for i := 0; i < 20; i++ {
		items = append(items, `{"text":"task `+strings.Repeat("x", i)+`","status":"pending"}`)
	}
	todoCall(t, &a.Screen, 1, "t1", "["+strings.Join(items, ",")+"]", proto.OutcomeOK)
	for _, h := range []int{12, 6, 4, 3, 2} {
		a.Update(tea.WindowSizeMsg{Width: 30, Height: h})
		lines := strings.Split(a.Frame(), "\n")
		if len(lines) != h {
			t.Fatalf("height %d: %d lines", h, len(lines))
		}
	}
	a.Update(tea.WindowSizeMsg{Width: 30, Height: 12})
	if f := a.Frame(); !strings.Contains(f, "more") {
		t.Errorf("a cut list does not say so:\n%s", f)
	}
}

// TestTodoRowsNeverExceedTheirRoom: a list cut to n rows, its "more" line included, is at most n lines at every n.
func TestTodoRowsNeverExceedTheirRoom(t *testing.T) {
	var list []Todo
	for i := 0; i < 12; i++ {
		list = append(list, Todo{Text: "t", Status: "pending"})
	}
	for n := 1; n <= 14; n++ {
		if got := len(todoRows(list, n)); got > n {
			t.Errorf("room %d: %d rows", n, got)
		}
	}
}

// TestTodoRowsAlwaysShowATaskWhenThereIsRoomForOne: with room for a single line the first task shows, not only a count of what
// was left out; with none there are no rows at all, however few the list.
func TestTodoRowsAlwaysShowATaskWhenThereIsRoomForOne(t *testing.T) {
	list := []Todo{{"first", "in_progress"}, {"second", "pending"}, {"third", "pending"}}
	got := todoRows(list, 1)
	if len(got) != 1 || got[0] != "[>] first" {
		t.Errorf("room 1: %q", got)
	}
	if got := todoRows(list[:1], 1); len(got) != 1 || got[0] != "[>] first" {
		t.Errorf("one task, room 1: %q", got)
	}
	for _, n := range []int{0, -1, -5} {
		if got := todoRows(list, n); len(got) != 0 {
			t.Errorf("room %d: %q", n, got)
		}
		if got := todoRows(nil, n); len(got) != 0 {
			t.Errorf("no tasks, room %d: %q", n, got)
		}
	}
	// With room for two or more, the cut is as before: tasks, then how many were left out.
	if got := todoRows(list, 2); len(got) != 2 || got[0] != "[>] first" || got[1] != "… 2 more" {
		t.Errorf("room 2: %q", got)
	}
}
