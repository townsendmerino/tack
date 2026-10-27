package tools

import (
	"context"
	"fmt"
	"sort"
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
// the line of cur most like old, from sym's spans, or, when that symbol is a type, the function or method funcLine finds.
// It is "" when no line shares a word with old, when the line is at file scope, or when sym cannot place the file.
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
	if d.Kind == "type" {
		if fl, fd, ok := funcLine(ctx, sym, r, cur, old, line); ok {
			line, d = fl, fd
		}
	}
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

// funcLineTries is the most lines funcLine asks the index about.
const funcLineTries = 20

// funcLine is the line of cur most like old (bestWindow's line scores) that a function or method holds, and that
// symbol, among the lines that score at least as high as line, the line of a type that bestLine chose: code that uses
// a struct's fields wins over the struct when both match the text as well (calibration §12.20's replay,
// e1-calibration-c6-t1-current-symbol-2026-10-27.md). Lines are tried best first; of equal scores, the one whose shared
// words are longer (matchedLen), since a long name is a specific one, then the earlier. At most funcLineTries are tried. It reports false when none of them is in a function or method.
func funcLine(ctx context.Context, sym Symbols, r, cur, old string, line int) (int, Def, bool) {
	lines, score, _, _ := bestWindow(cur, old)
	if line < 1 || line > len(score) {
		return 0, Def{}, false
	}
	var cands []int
	for i, s := range score {
		if s > 0 && s >= score[line-1] && i != line-1 {
			cands = append(cands, i)
		}
	}
	matched := func(i int) int { return matchedLen(lines[i], old) }
	sort.SliceStable(cands, func(a, b int) bool {
		if sa, sb := score[cands[a]], score[cands[b]]; sa != sb {
			return sa > sb
		}
		return matched(cands[a]) > matched(cands[b])
	})
	for k, i := range cands {
		if k == funcLineTries {
			break
		}
		if defs, err := sym.SymbolAt(ctx, r, i+1); err == nil && len(defs) > 0 && defs[0].Kind != "type" {
			return i + 1, defs[0], true
		}
	}
	return 0, Def{}, false
}

// matchedLen is the summed length of text's distinct words (as bestWindow counts them) that line holds.
func matchedLen(line, text string) int {
	words := map[string]bool{}
	for _, w := range wordRe.FindAllString(text, -1) {
		if len(w) > 1 {
			words[w] = true
		}
	}
	n := 0
	for _, w := range wordRe.FindAllString(line, -1) {
		if words[w] {
			n += len(w)
			delete(words, w)
		}
	}
	return n
}
