package tools

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// Edit by line range (Options.LineEdits; calibration pre-registration §4.9): edit takes start_line and end_line in
// place of old_string, and new_string replaces those lines. The model copies two numbers instead of a block of text,
// so the guard is on the numbers: every line of the range must have been shown to the model by read or symbol, and must
// be the same now as when it was shown. A range whose lines have moved since, after the model's own edit or any other
// change, is refused instead of editing the wrong place.

// shownLines is what read and symbol showed of one file: its content then, and the ranges shown of it (1-based,
// inclusive).
type shownLines struct {
	content string
	ranges  [][2]int
}

// showed records that the model was shown lines start..end of abs, whose content is content. Ranges shown of an
// earlier content are dropped.
func (w *Workspace) showed(abs, content string, start, end int) {
	if end < start {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.shown == nil {
		w.shown = map[string]shownLines{}
	}
	s := w.shown[abs]
	if s.content != content {
		s = shownLines{content: content}
	}
	s.ranges = append(s.ranges, [2]int{start, end})
	w.shown[abs] = s
}

// lastShown is what the model was last shown of abs, and whether it was shown any of it.
func (w *Workspace) lastShown(abs string) (shownLines, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	s, ok := w.shown[abs]
	return s, ok
}

// covers reports whether the ranges together hold every line of start..end.
func (s shownLines) covers(start, end int) bool {
	for line := start; line <= end; line++ {
		in := false
		for _, r := range s.ranges {
			if r[0] <= line && line <= r[1] {
				in = true
				break
			}
		}
		if !in {
			return false
		}
	}
	return true
}

// byLines replaces lines start..end of the file given with newText, under the guard above and the edit tool's own
// checks: an edit that changes nothing, a repeated edit (refuseDup), a line read cut.
func (t editTool) byLines(ctx context.Context, ws *Workspace, given string, start, end int, newText string) Result {
	p, err := ws.Resolve(given)
	if err != nil {
		return ws.pathError(given, err)
	}
	if inGit(ws, p) {
		return errorf("%q is inside .git, which tools never change", given)
	}
	if st, err := os.Stat(p); err == nil && st.IsDir() {
		return errorf("%q is a directory; edit needs a file", given)
	}
	if start < 1 || end < start {
		return errorf("start_line and end_line are line numbers as read shows them, from 1, with start_line <= end_line")
	}
	shown, ok := ws.lastShown(p)
	if !ok {
		return errorf("read %q before editing it by line", given)
	}
	if !shown.covers(start, end) {
		return errorf("lines %d-%d of %q were not all shown to you; read them first", start, end, given)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return ws.pathError(given, err)
	}
	cur := string(data)
	a, b, ok := lineRange(cur, start, end)
	sa, sb, sok := lineRange(shown.content, start, end)
	if !ok || !sok {
		return errorf("%q no longer has lines %d-%d; read it again", given, start, end)
	}
	old := cur[a:b]
	if old != shown.content[sa:sb] {
		return errorf("lines %d-%d of %q are not what you read there: the file has changed since, and an edit above them "+
			"moves them. Read them again", start, end, given)
	}
	for l := range strings.SplitSeq(strings.TrimSuffix(old, "\n"), "\n") {
		if cut(l) != l {
			return errorf("lines %d-%d of %q hold a line longer than read shows whole; change it with old_string", start, end, given)
		}
	}
	if newText != "" && strings.HasSuffix(old, "\n") && !strings.HasSuffix(newText, "\n") {
		newText += "\n"
	}
	if old == newText {
		if t.messages {
			return Result{Text: unchangedEdit, IsError: true}
		}
		return errorf("new_string is lines %d-%d as they are; nothing to change", start, end)
	}
	if t.refuseDup {
		if line, ok := ws.appliedEdit(p, old, newText); ok {
			return errorf("this exact edit was already applied to %q at line %d earlier in this session; read %q to see it", given,
				line, given)
		}
	}
	out := cur[:a] + newText + cur[b:]
	if err := writeFile(p, out); err != nil {
		return errorf("%q: %s", given, ws.scrub(err.Error()))
	}
	ws.saw(p, out)
	ws.recordChange(p, cur, out, "edit", start, old, newText)
	where := t.where(ctx, ws, p, start)
	if newText == "" {
		return Result{Text: fmt.Sprintf("edited %s: deleted lines %d-%d%s", given, start, end, where)}
	}
	newEnd := start + strings.Count(strings.TrimSuffix(newText, "\n"), "\n")
	return Result{Text: fmt.Sprintf("edited %s: replaced lines %d-%d, now lines %d-%d%s", given, start, end, start, newEnd,
		where)}
}
