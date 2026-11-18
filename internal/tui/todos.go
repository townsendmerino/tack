package tui

import (
	"encoding/json"
	"fmt"

	"github.com/townsendmerino/tack/internal/proto"
)

// Todo is one item of the session's task list.
type Todo struct {
	Text   string
	Status string // pending, in_progress or done
}

// todoState folds the model's todo calls into the task list. The list lives in the event log as the last todo call's input
// (internal/tools todoTool), so a call counts only once its tool.finished says it succeeded: a call the tool refused, or one
// still running, cancelled or denied, leaves the list as it was.
type todoState struct {
	list    []Todo
	pending map[string][]Todo // todo calls not finished, by call id
}

func (s *todoState) call(e proto.ToolCall) {
	if e.Name != "todo" {
		return
	}
	var in struct {
		Items []struct {
			Text   string `json:"text"`
			Status string `json:"status"`
		} `json:"items"`
	}
	if json.Unmarshal(e.Input, &in) != nil {
		return
	}
	items := make([]Todo, 0, len(in.Items))
	for _, it := range in.Items {
		items = append(items, Todo{Text: it.Text, Status: it.Status})
	}
	if s.pending == nil {
		s.pending = map[string][]Todo{}
	}
	s.pending[e.ID] = items
}

func (s *todoState) finished(e proto.ToolFinished) {
	items, ok := s.pending[e.ID]
	if !ok {
		return
	}
	delete(s.pending, e.ID)
	if e.Outcome == proto.OutcomeOK {
		s.list = items
	}
}

// Todos is the session's task list as of the events folded so far.
func (v *View) Todos() []Todo { return v.todos.list }

// todoRows draws the list for a pane: one line per item, marked as the tool marks it, cut to rows lines with a count of what
// was left out.
func todoRows(list []Todo, rows int) []string {
	if rows <= 0 {
		return nil
	}
	if len(list) == 0 {
		return []string{"no tasks yet"}
	}
	mark := map[string]string{"pending": "[ ]", "in_progress": "[>]", "done": "[x]"}
	show := list
	if len(show) > rows {
		// The count of what is left out takes a line of its own, except when there is only one line: then it is the first task
		// that shows, since a count with no task above it says nothing.
		show = show[:max(rows-1, 1)]
	}
	var out []string
	for _, it := range show {
		m := mark[it.Status]
		if m == "" {
			m = "[?]"
		}
		out = append(out, m+" "+it.Text)
	}
	if len(show) < len(list) && rows >= 2 {
		out = append(out, fmt.Sprintf("… %d more", len(list)-len(show)))
	}
	return out
}
