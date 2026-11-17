package tui

import (
	"fmt"

	"github.com/townsendmerino/tack/internal/proto"
)

// Change is one file the session's edits and writes changed: how many of those calls finished ok, and whether the session made
// the file (its first pre-image was absent).
type Change struct {
	Path  string
	Edits int
	New   bool
}

// changeState folds edit and write calls into the changes. A call counts when its tool.finished says ok, and what it changed is
// what its checkpoint's pre-images name, so a call that failed or was denied, and a bash snapshot, change nothing.
type changeState struct {
	tool  map[string]string // call id → tool name, for edit and write calls
	files map[string][]proto.FileImage
	order []string
	by    map[string]*Change
}

func (s *changeState) call(e proto.ToolCall) {
	if e.Name != "edit" && e.Name != "write" {
		return
	}
	if s.tool == nil {
		s.tool, s.files, s.by = map[string]string{}, map[string][]proto.FileImage{}, map[string]*Change{}
	}
	s.tool[e.ID] = e.Name
}

func (s *changeState) checkpoint(e proto.Checkpoint) {
	if _, ok := s.tool[e.ID]; ok && len(e.Files) > 0 {
		s.files[e.ID] = e.Files
	}
}

func (s *changeState) finished(e proto.ToolFinished) {
	if _, ok := s.tool[e.ID]; !ok {
		return
	}
	files := s.files[e.ID]
	delete(s.tool, e.ID)
	delete(s.files, e.ID)
	if e.Outcome != proto.OutcomeOK {
		return
	}
	for _, f := range files {
		c := s.by[f.Path]
		if c == nil {
			c = &Change{Path: f.Path, New: f.Absent}
			s.by[f.Path] = c
			s.order = append(s.order, f.Path)
		}
		c.Edits++
	}
}

// Changes are the files the session has changed, in the order each was first changed.
func (v *View) Changes() []Change {
	var out []Change
	for _, p := range v.changes.order {
		out = append(out, *v.changes.by[p])
	}
	return out
}

// changeRows draws the changes for a pane, cut to rows lines with a count of what was left out.
func changeRows(list []Change, rows int) []string {
	if rows <= 0 {
		return nil
	}
	if len(list) == 0 {
		return []string{"no changes yet"}
	}
	show := list
	if len(show) > rows {
		// The count of what is left out takes a line of its own, except when there is only one line: then it is the first file
		// that shows, since a count with no file above it says nothing.
		show = show[:max(rows-1, 1)]
	}
	var out []string
	for _, c := range show {
		s := fmt.Sprintf("%s  %d edit", c.Path, c.Edits)
		if c.Edits != 1 {
			s += "s"
		}
		if c.New {
			s += "  new"
		}
		out = append(out, s)
	}
	if len(show) < len(list) && rows >= 2 {
		out = append(out, fmt.Sprintf("… %d more", len(list)-len(show)))
	}
	return out
}
