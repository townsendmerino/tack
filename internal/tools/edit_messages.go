package tools

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// The edit tool's messages (Options.EditMessages; e1-calibration-preregistration-2026-10-18.md §4.1): when an edit
// fails, say where the text or the file is instead. Off, edit answers as it always has.

// change is one successful edit or write in the session: the file, its content before and after, what made it, and
// the line it changed (an edit's first line; 1 for a write).
type change struct {
	abs           string
	before, after string
	by            string // "edit" or "write"
	line          int
}

// recordChange notes a successful edit or write of abs.
func (w *Workspace) recordChange(abs, before, after, by string, line int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.changes = append(w.changes, change{abs: abs, before: before, after: after, by: by, line: line})
}

// changeThatRemoved is the session's latest change to abs that took text out of it.
func (w *Workspace) changeThatRemoved(abs, text string) (change, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for i := len(w.changes) - 1; i >= 0; i-- {
		c := w.changes[i]
		if c.abs == abs && strings.Contains(c.before, text) && !strings.Contains(c.after, text) {
			return c, true
		}
	}
	return change{}, false
}

// nearestPath is an existing file that rel probably meant, or "": the file with the same name whose path shares the
// longest tail with rel. Dropping rel's leading directories first (a repository name taken as a directory) is a shortcut
// to the same answer that skips walking the workspace.
func nearestPath(ctx context.Context, ws *Workspace, rel string) string {
	rel = path.Clean(strings.TrimPrefix(filepath.ToSlash(rel), "./"))
	parts := strings.Split(rel, "/")
	for i := 1; i < len(parts); i++ {
		cand := strings.Join(parts[i:], "/")
		if st, err := os.Stat(filepath.Join(ws.root, filepath.FromSlash(cand))); err == nil && st.Mode().IsRegular() {
			return cand
		}
	}
	best, bestTail := "", 0
	walk(ctx, ws, ws.root, func(r string) {
		if path.Base(r) != parts[len(parts)-1] {
			return
		}
		rp := strings.Split(r, "/")
		tail := 0
		for tail < len(rp) && tail < len(parts) && rp[len(rp)-1-tail] == parts[len(parts)-1-tail] {
			tail++
		}
		if tail > bestTail || tail == bestTail && best != "" && len(r) < len(best) {
			best, bestTail = r, tail
		}
	})
	return best
}

// otherFile is where in the workspace, apart from abs, text occurs exactly: a relative path and a line, or "".
func otherFile(ctx context.Context, ws *Workspace, abs, text string) (string, int) {
	found, line := "", 0
	walk(ctx, ws, ws.root, func(r string) {
		p := filepath.Join(ws.root, filepath.FromSlash(r))
		if found != "" || p == abs {
			return
		}
		if st, err := os.Stat(p); err != nil || st.Size() > maxFileBytes {
			return
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return
		}
		if i := strings.Index(string(b), text); i >= 0 {
			found, line = r, strings.Count(string(b[:i]), "\n")+1
		}
	})
	return found, line
}

// bestLinesShown is how many lines bestLines shows.
const bestLinesShown = 12

var wordRe = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*|[0-9]+`)

// bestLines is the bestLinesShown consecutive lines of content most like text, numbered as read numbers them, or ""
// when no line shares a word with it. A line scores 3 when it equals one of text's lines (spacing aside), plus one for
// each of text's distinct words it holds.
func bestLines(content, text string) string {
	words := map[string]bool{}
	for _, w := range wordRe.FindAllString(text, -1) {
		if len(w) > 1 {
			words[w] = true
		}
	}
	whole := map[string]bool{}
	for _, l := range strings.Split(text, "\n") {
		if t := strings.TrimSpace(l); t != "" {
			whole[t] = true
		}
	}
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	score := make([]int, len(lines))
	for i, l := range lines {
		if whole[strings.TrimSpace(l)] {
			score[i] += 3
		}
		seen := map[string]bool{}
		for _, w := range wordRe.FindAllString(l, -1) {
			if words[w] && !seen[w] {
				seen[w] = true
				score[i]++
			}
		}
	}
	n := min(bestLinesShown, len(lines))
	sum := 0
	for i := 0; i < n; i++ {
		sum += score[i]
	}
	best, at := sum, 0
	for i := n; i < len(lines); i++ {
		sum += score[i] - score[i-n]
		if sum > best {
			best, at = sum, i-n+1
		}
	}
	if best == 0 {
		return ""
	}
	var b strings.Builder
	for i := at; i < at+n; i++ {
		fmt.Fprintf(&b, "%6d\t%s\n", i+1, lines[i])
	}
	return b.String()
}

// Message texts that do not depend on the call.
const unchangedEdit = "error: this edit changes nothing; if the change is already made, verify it with a build or test."

// notFound is the reply to an old_string that is not in rel (abs), with the edit messages on: the session's own change
// that removed it, else another file that has it, else the lines of rel most like it.
func notFound(ctx context.Context, ws *Workspace, rel, abs, cur, old string) Result {
	if c, ok := ws.changeThatRemoved(abs, old); ok {
		at := fmt.Sprintf(" at line %d", c.line)
		if c.by == "write" {
			at = ""
		}
		return errorf("this text isn't in %q now: your earlier %s of this file%s replaced it. Read %q again and edit what is "+
			"there.", rel, c.by, at, rel)
	}
	if other, line := otherFile(ctx, ws, abs, old); other != "" {
		return errorf("this text isn't in %q; it is in %s:%d. Edit that file, or read %q to find the text you meant.",
			rel, other, line, rel)
	}
	if lines := bestLines(cur, old); lines != "" {
		return errorf("this text isn't in %q. The lines most like it, with their numbers:\n%sold_string must match the "+
			"file exactly, whitespace included.", rel, lines)
	}
	return errorf("this text isn't in %q, and no line of it shares a word with old_string; read %q again before editing.",
		rel, rel)
}
