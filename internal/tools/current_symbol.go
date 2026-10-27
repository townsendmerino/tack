package tools

import (
	"context"
	"fmt"
	"strings"
)

// The current-symbol note (Options.CurrentSymbol; e1-calibration-preregistration-2026-10-18.md §12.20): when an edit's
// old_string is not in the file and the session has already made an edit call to that file, the result carries the
// symbol the edit aimed at as the file has it now, without line numbers, so old_string can be copied from it.

// CurrentSymbolOpen starts the note. It spans lines, so loop.StripNotes finds it by this prefix.
const CurrentSymbolOpen = "\n\n[tack: the current text of "

// currentSymbolLines is the most lines the note shows; a longer symbol is cut to this many around the best line.
const currentSymbolLines = 40

// countEditCall counts an old_string edit call to abs, an existing file outside .git, and reports whether the session had
// made one before, whatever that call's outcome.
func (w *Workspace) countEditCall(abs string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.editCalls == nil {
		w.editCalls = map[string]int{}
	}
	w.editCalls[abs]++
	return w.editCalls[abs] > 1
}

// currentSymbolNote is the note for old, which is not in cur, the content of rel (abs): the innermost symbol holding
// the line of cur most like old, from sym's spans. It is "" when no line shares a word with old, when the line is at file
// scope, or when sym cannot place the file.
func currentSymbolNote(ctx context.Context, ws *Workspace, sym Symbols, rel, abs, cur, old string) string {
	line := bestLine(cur, old)
	if line == 0 {
		return ""
	}
	r := ws.Rel(abs)
	if _, err := sym.Sync(ctx, []string{r}); err != nil {
		return ""
	}
	defs, err := sym.SymbolAt(ctx, r, line)
	if err != nil || len(defs) == 0 {
		return ""
	}
	d := defs[0]
	lines := strings.Split(strings.TrimSuffix(cur, "\n"), "\n")
	a, b := d.StartLine, min(d.EndLine, len(lines))
	if a < 1 || b < a {
		return ""
	}
	of := ""
	if b-a+1 > currentSymbolLines {
		of = fmt.Sprintf(" of %d-%d", a, b)
		lo := max(a, line-currentSymbolLines/2)
		hi := min(b, lo+currentSymbolLines-1)
		a, b = max(a, hi-currentSymbolLines+1), hi
	}
	return fmt.Sprintf("%s%s in %s, lines %d-%d%s, exactly as the file has it now; copy old_string from it:\n%s\n]",
		CurrentSymbolOpen, d.Name, rel, a, b, of, strings.Join(lines[a-1:b], "\n"))
}
