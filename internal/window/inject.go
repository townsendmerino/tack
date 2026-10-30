package window

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/townsendmerino/tack/internal/tools"
)

// The turn-1 injection (DESIGN §7.4's auto-context; e1-calibration-preregistration-2026-10-18.md §13.1, gate 0b's
// selection): before an edit task's first request, the code ken finds for the statement goes into the first message,
// after the instruction head and before the task.
const (
	// InjectBudget is the injection's budget in tokens: DESIGN §7.4's 10% of a 16k window.
	InjectBudget = 1600
	// injectBytesPerToken counts an item's tokens, as loop does before a response reports a rate.
	injectBytesPerToken = 3.0
	injectDefsPerName   = 3
	injectSearchK       = 8
)

// InjectIndex is what the injection asks ken: definitions by name and a search over the statement.
// kensearch.Searcher is one.
type InjectIndex interface {
	Definition(ctx context.Context, name string) ([]tools.Def, error)
	Search(ctx context.Context, query string, k int) ([]tools.Hit, error)
}

// InjectItem is one packed span of a file in root, with its text.
type InjectItem struct {
	File       string
	Start, End int
	Text       string
}

var (
	backquoted = regexp.MustCompile("`([^`]+)`")
	recvMethod = regexp.MustCompile(`\(\*?([A-Za-z_]\w*)\)\.([A-Za-z_]\w*)`)
	qualIdent  = regexp.MustCompile(`[A-Za-z_]\w*(?:\.[A-Za-z_]\w*)*`)
	goWords    = map[string]bool{}
)

func init() {
	for _, s := range strings.Fields(`break case chan const continue default defer else fallthrough for func go goto if import
		interface map package range return select struct switch type var
		any bool byte comparable complex64 complex128 error float32 float64 int int8 int16 int32 int64 rune string uint
		uint8 uint16 uint32 uint64 uintptr true false iota nil append cap clear close complex copy delete imag len make
		max min new panic print println real recover`) {
		goWords[s] = true
	}
}

// InjectIdentifiers is the statement's named code: the qualified identifiers in its backquoted spans, in order, each
// once, with (*T).m read as T.m. Go keywords and predeclared names, single letters, and single-word all-lower-case
// names (no dot, no upper case: `serve`, `kv`) are left out.
func InjectIdentifiers(statement string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range backquoted.FindAllStringSubmatch(statement, -1) {
		span := recvMethod.ReplaceAllString(m[1], "$1.$2")
		for _, id := range qualIdent.FindAllString(span, -1) {
			if goWords[id] || len(id) < 2 || seen[id] || !strings.Contains(id, ".") && strings.ToLower(id) == id {
				continue
			}
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// InjectCandidates is the injection's items in packing order, as spans: each identifier's definitions (up to 3, a
// doc comment included, trying the name without its last component when it finds nothing), then the statement's search
// hits by rank, without those in _test.go or non-Go files.
func InjectCandidates(ctx context.Context, ix InjectIndex, statement string) ([]InjectItem, error) {
	var items []InjectItem
	for _, id := range InjectIdentifiers(statement) {
		parts := strings.Split(id, ".")
		for n := len(parts); n >= 1; n-- {
			defs, err := ix.Definition(ctx, strings.Join(parts[:n], "."))
			if err != nil || len(defs) == 0 {
				continue
			}
			for _, d := range defs[:min(len(defs), injectDefsPerName)] {
				start := d.StartLine
				if d.DocLine > 0 {
					start = d.DocLine
				}
				items = append(items, InjectItem{File: d.File, Start: start, End: d.EndLine})
			}
			break
		}
	}
	hits, err := ix.Search(ctx, statement, injectSearchK)
	if err != nil {
		return nil, err
	}
	for _, h := range hits {
		if strings.HasSuffix(h.File, "_test.go") || !strings.HasSuffix(h.File, ".go") {
			continue
		}
		items = append(items, InjectItem{File: h.File, Start: h.StartLine, End: h.EndLine})
	}
	return items, nil
}

// InjectPack packs candidates from root in order: an item whose lines are all packed already is skipped, and one that
// does not fit what is left of budget tokens is skipped and packing goes on. Each packed item carries its text.
func InjectPack(root string, candidates []InjectItem, budget int) []InjectItem {
	var out []InjectItem
	have := map[string]map[int]bool{}
	files := map[string][]string{}
	used := 0
	for _, it := range candidates {
		lines, ok := files[it.File]
		if !ok {
			b, _ := os.ReadFile(filepath.Join(root, filepath.FromSlash(it.File)))
			lines = strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
			files[it.File] = lines
		}
		if it.Start < 1 || it.End > len(lines) || it.End < it.Start {
			continue
		}
		if allPacked(have[it.File], it) {
			continue
		}
		text := strings.Join(lines[it.Start-1:it.End], "\n")
		cost := InjectTokens(text)
		if used+cost > budget {
			continue
		}
		used += cost
		if have[it.File] == nil {
			have[it.File] = map[int]bool{}
		}
		for l := it.Start; l <= it.End; l++ {
			have[it.File][l] = true
		}
		it.Text = text
		out = append(out, it)
	}
	return out
}

func allPacked(have map[int]bool, it InjectItem) bool {
	for l := it.Start; l <= it.End; l++ {
		if !have[l] {
			return false
		}
	}
	return true
}

// InjectTokens is an item's cost: its text and a newline, at injectBytesPerToken, rounded up.
func InjectTokens(text string) int {
	return int(float64(len(text)+1)/injectBytesPerToken + 0.999)
}

// InjectText is the injection as the first message carries it, or "" for no items.
func InjectText(items []InjectItem) string {
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Code that may be relevant to the task, found by searching the repository for it:\n")
	for _, it := range items {
		fmt.Fprintf(&b, "\n%s, lines %d-%d:\n```go\n%s\n```\n", it.File, it.Start, it.End, it.Text)
	}
	return b.String()
}
