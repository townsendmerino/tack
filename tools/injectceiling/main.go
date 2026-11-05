// Command injectceiling is C7's gate 0 for the turn-1 injection (e1-calibration-preregistration-2026-10-18.md §13.1),
// with no model: for each E1 task it builds the injection §13.1 specifies from ken over the base tree and the task
// statement, and reports whether the fix's raw spans land inside it, how much of FixGold it covers, and §13.1's verdict
// on the 12 calibration tasks.
//
//	go run ./tools/injectceiling -mirrors DIR [-model-dir DIR] [-budget 1600]
//
// It ends with VERDICT: PASS when every task was measured, and FAIL when one could not be. The gate's own outcome is
// a line of the report, read under §13.1's rule.
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/townsendmerino/aikit/chunk"
	"github.com/townsendmerino/aikit/chunk/treesitter"

	"github.com/townsendmerino/tack/internal/eval/pilot"
	"github.com/townsendmerino/tack/internal/kensearch"
	"github.com/townsendmerino/tack/internal/window"
	"github.com/townsendmerino/tack/tools/gate"
)

// §13.1's selection and packing constants.
const (
	defsPerName  = 3
	searchK      = 8
	bytesPerTok  = 3.0 // loop's fitDefaultRate
	defaultTasks = "eval/e1/calibration.jsonl"
	pilotTasks   = "eval/e1/pilot.jsonl"
)

// lowReach are the calibration tasks whose CB3 runs reached gold under half the time (e1-cb3-context-metrics-2026-10-28.md §4).
var lowReach = map[int]bool{1: true, 3: true, 4: true, 5: true, 6: true, 7: true, 8: true, 9: true}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, w, logw io.Writer) int {
	fs := flag.NewFlagSet("injectceiling", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	mirrors := fs.String("mirrors", "", "the E1 mirrors directory (<repo>.git)")
	modelDir := fs.String("model-dir", filepath.Join(os.Getenv("HOME"), ".ken", "model"), "ken's Model2Vec directory, for hybrid")
	budget := fs.Int("budget", 1600, "the injection's budget, tokens")
	selection := fs.String("selection", "first", "first: §13.1's selection (gate 0); posthoc: gate 0b's, with test and non-Go search hits and single-word lower-case identifiers dropped")
	if err := fs.Parse(args); err != nil || *mirrors == "" || *selection != "first" && *selection != "posthoc" {
		fmt.Fprintln(w, gate.Verdict(gate.Inconclusive, "injectceiling: usage: -mirrors DIR [-model-dir DIR] [-budget N] [-selection first|posthoc]"))
		return 2
	}
	posthoc = *selection == "posthoc"
	chunk.Register(kensearch.Chunker, treesitter.New(treesitter.WithParseTimeoutMicros(1_000_000)))
	ctx := context.Background()
	type suite struct {
		name, path string
	}
	var rows []row
	for _, s := range []suite{{"calibration", defaultTasks}, {"pilot", pilotTasks}} {
		ts, err := pilot.LoadTasks(s.path, 1, 1<<30)
		if err != nil {
			fmt.Fprintln(w, gate.Verdict(gate.Fail, "injectceiling: "+err.Error()))
			return 1
		}
		for _, t := range ts {
			fmt.Fprintf(logw, "%s task %d (%s %s)\n", s.name, t.N, t.Repo, t.Base[:8])
			r, err := measure(ctx, *mirrors, *modelDir, *budget, s.name, t)
			if err != nil {
				fmt.Fprintln(w, gate.Verdict(gate.Fail, fmt.Sprintf("injectceiling: %s task %d: %v", s.name, t.N, err)))
				return 1
			}
			rows = append(rows, r)
		}
	}
	report(w, rows, *budget)
	fmt.Fprintln(w, gate.Verdict(gate.OK, fmt.Sprintf("injectceiling: %d tasks measured", len(rows))))
	return 0
}

// cell is one injection's outcome against a task's gold.
type cell struct {
	lands    bool    // every raw span overlaps the injected lines
	reached  bool    // any FixGold line injected
	cov      float64 // share of FixGold lines injected
	tokens   int     // the injection's size, at bytesPerTok
	items    int
	rawLand  int // raw spans landed, of rawSpans
	rawSpans int
}

type row struct {
	suite      string
	task       int
	repo       string
	idents     []string
	found      int // identifiers with a definition
	hybrid     cell
	bm25       cell
	hybridAll  cell // no budget
	goldLines  int
	goldSpans  []pilot.LineSpan
	firstFiles []string
}

func measure(ctx context.Context, mirrors, modelDir string, budget int, suite string, t pilot.Task) (row, error) {
	r := row{suite: suite, task: t.N, repo: t.Repo}
	raw, gold, err := pilot.FixGold(ctx, mirrors, t.Repo, t.Base, t.Commit, t.TestFiles)
	if err != nil {
		return r, err
	}
	r.goldSpans, r.goldLines = gold, lineCount(gold)
	tmp, err := os.MkdirTemp("", "tack-injectceiling-*")
	if err != nil {
		return r, err
	}
	defer os.RemoveAll(tmp)
	if err := archive(mirrors, t.Repo, t.Base, tmp); err != nil {
		return r, err
	}
	r.idents = identifiers(t.Statement)
	if posthoc {
		r.idents = window.InjectIdentifiers(t.Statement)
	}
	for _, mode := range []string{"hybrid", "bm25"} {
		o := kensearch.Options{Mode: mode}
		if mode != "bm25" {
			o.ModelDir = modelDir
		}
		s, err := kensearch.Open(ctx, tmp, o)
		if err != nil {
			return r, fmt.Errorf("ken %s: %w", mode, err)
		}
		items, found, err := candidates(ctx, s, r.idents, t.Statement)
		if err == nil && posthoc {
			// Gate 0b's selection is the product's (internal/window), so the screen runs exactly what this measured.
			items, err = injectSpans(ctx, s, t.Statement)
		}
		s.Close()
		if err != nil {
			return r, err
		}
		packFn := pack
		if posthoc {
			packFn = injectPack
		}
		packed := packFn(tmp, items, budget)
		c := score(packed, raw, gold, tmp)
		if mode == "hybrid" {
			r.found = found
			r.hybrid = c
			r.hybridAll = score(packFn(tmp, items, 1<<30), raw, gold, tmp)
			for _, it := range packed {
				r.firstFiles = append(r.firstFiles, fmt.Sprintf("%s:%d-%d", it.File, it.Start, it.End))
			}
		} else {
			r.bm25 = c
		}
	}
	return r, nil
}

// archive writes repo's tree at rev into dir.
func archive(mirrors, repo, rev, dir string) error {
	var out bytes.Buffer
	cmd := exec.Command("git", "--git-dir", filepath.Join(mirrors, repo+".git"), "archive", "--format=tar", rev)
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git archive %s %s: %w", repo, rev, err)
	}
	x := exec.Command("tar", "-x", "-C", dir)
	x.Stdin = &out
	if b, err := x.CombinedOutput(); err != nil {
		return fmt.Errorf("tar: %v: %s", err, b)
	}
	return nil
}

var (
	backquoted = regexp.MustCompile("`([^`]+)`")
	recvMethod = regexp.MustCompile(`\(\*?([A-Za-z_]\w*)\)\.([A-Za-z_]\w*)`)
	qualIdent  = regexp.MustCompile(`[A-Za-z_]\w*(?:\.[A-Za-z_]\w*)*`)
	dropped    = map[string]bool{}
)

func init() {
	for s := range strings.FieldsSeq(`break case chan const continue default defer else fallthrough for func go goto if import
		interface map package range return select struct switch type var
		any bool byte comparable complex64 complex128 error float32 float64 int int8 int16 int32 int64 rune string uint
		uint8 uint16 uint32 uint64 uintptr true false iota nil append cap clear close complex copy delete imag len make
		max min new panic print println real recover`) {
		dropped[s] = true
	}
}

// identifiers is §13.1's named code: the qualified identifiers in the statement's backquoted spans, in order, without
// Go keywords, predeclared names or single letters.
func identifiers(statement string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range backquoted.FindAllStringSubmatch(statement, -1) {
		span := recvMethod.ReplaceAllString(m[1], "$1.$2")
		for _, id := range qualIdent.FindAllString(span, -1) {
			if dropped[id] || len(id) < 2 || seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// candidates is §13.1's items in packing order: each identifier's definitions (up to defsPerName, trying shorter names
// when a name finds nothing), then the statement's search hits by rank. found counts the identifiers with a definition.
func candidates(ctx context.Context, s *kensearch.Searcher, idents []string, statement string) ([]pilot.LineSpan, int, error) {
	var items []pilot.LineSpan
	found := 0
	for _, id := range idents {
		parts := strings.Split(id, ".")
		for n := len(parts); n >= 1; n-- {
			defs, err := s.Definition(ctx, strings.Join(parts[:n], "."))
			if err != nil || len(defs) == 0 {
				continue
			}
			found++
			for i, d := range defs {
				if i == defsPerName {
					break
				}
				start := d.StartLine
				if d.DocLine > 0 {
					start = d.DocLine
				}
				items = append(items, pilot.LineSpan{File: d.File, Start: start, End: d.EndLine})
			}
			break
		}
	}
	hits, err := s.Search(ctx, statement, searchK)
	if err != nil {
		return nil, 0, err
	}
	for _, h := range hits {
		if posthoc && (strings.HasSuffix(h.File, "_test.go") || !strings.HasSuffix(h.File, ".go")) {
			continue
		}
		items = append(items, pilot.LineSpan{File: h.File, Start: h.StartLine, End: h.EndLine})
	}
	return items, found, nil
}

// pack is §13.1's packing: whole items while they fit the budget, skipping an item whose lines are all packed or that
// does not fit, at bytesPerTok.
func pack(root string, items []pilot.LineSpan, budget int) []pilot.LineSpan {
	var out []pilot.LineSpan
	have := map[string]map[int]bool{}
	used := 0
	for _, it := range items {
		lines := fileLines(root, it.File)
		if it.Start < 1 || it.End > len(lines) || it.End < it.Start {
			continue
		}
		fresh := false
		for l := it.Start; l <= it.End; l++ {
			if !have[it.File][l] {
				fresh = true
				break
			}
		}
		if !fresh {
			continue
		}
		cost := int(float64(len(strings.Join(lines[it.Start-1:it.End], "\n"))+1)/bytesPerTok + 0.999)
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
		out = append(out, it)
	}
	return out
}

var fileCache = map[string][]string{}

func fileLines(root, rel string) []string {
	p := filepath.Join(root, filepath.FromSlash(rel))
	if l, ok := fileCache[p]; ok {
		return l
	}
	b, _ := os.ReadFile(p)
	l := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	fileCache[p] = l
	return l
}

func score(packed, raw, gold []pilot.LineSpan, root string) cell {
	in := map[string]map[int]bool{}
	tokens := 0
	for _, it := range packed {
		if in[it.File] == nil {
			in[it.File] = map[int]bool{}
		}
		for l := it.Start; l <= it.End; l++ {
			in[it.File][l] = true
		}
		lines := fileLines(root, it.File)
		tokens += int(float64(len(strings.Join(lines[it.Start-1:it.End], "\n"))+1)/bytesPerTok + 0.999)
	}
	c := cell{tokens: tokens, items: len(packed), rawSpans: len(raw)}
	for _, s := range raw {
		for l := s.Start; l <= s.End; l++ {
			if in[s.File][l] {
				c.rawLand++
				break
			}
		}
	}
	c.lands = len(raw) > 0 && c.rawLand == len(raw)
	hit, total := 0, 0
	for _, s := range gold {
		for l := s.Start; l <= s.End; l++ {
			total++
			if in[s.File][l] {
				hit++
			}
		}
	}
	if total > 0 {
		c.cov = float64(hit) / float64(total)
	}
	c.reached = hit > 0
	return c
}

func lineCount(spans []pilot.LineSpan) int {
	n := 0
	for _, s := range spans {
		n += s.End - s.Start + 1
	}
	return n
}

func mark(c cell) string {
	l := "no"
	if c.lands {
		l = "**yes**"
	}
	return fmt.Sprintf("%s (%d/%d) | %.2f | %d", l, c.rawLand, c.rawSpans, c.cov, c.tokens)
}

func report(w io.Writer, rows []row, budget int) {
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].suite != rows[j].suite {
			return rows[i].suite < rows[j].suite
		}
		return rows[i].task < rows[j].task
	})
	gateName := "gate 0"
	if posthoc {
		gateName = "gate 0b, the post-hoc selection"
	}
	fmt.Fprintf(w, "# injectceiling: C7 %s (§13.1), budget %d tokens at %.0f bytes/token\n\n", gateName, budget, bytesPerTok)
	fmt.Fprintln(w, "lands: every raw span of the fix overlaps the injection (raw spans landed / raw spans); cov: share of FixGold lines injected; tok: the injection's size.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| suite | task | repo | identifiers (with a definition) | hybrid lands | cov | tok | bm25 lands | cov | tok | no budget lands | cov | tok |")
	fmt.Fprintln(w, "|---|---|---|---|---|---|---|---|---|---|---|---|---|")
	for _, r := range rows {
		fmt.Fprintf(w, "| %s | %d | %s | %d (%d) | %s | %s | %s |\n", r.suite, r.task, r.repo, len(r.idents), r.found,
			mark(r.hybrid), mark(r.bm25), mark(r.hybridAll))
	}
	fmt.Fprintln(w)
	type tally struct{ lands, reached, low, n int }
	sum := func(suite string, pick func(row) cell) tally {
		var t tally
		for _, r := range rows {
			if r.suite != suite {
				continue
			}
			c := pick(r)
			t.n++
			if c.lands {
				t.lands++
				if suite == "calibration" && lowReach[r.task] {
					t.low++
				}
			}
			if c.reached {
				t.reached++
			}
		}
		return t
	}
	for _, s := range []string{"calibration", "pilot"} {
		for _, m := range []struct {
			name string
			pick func(row) cell
		}{{"hybrid", func(r row) cell { return r.hybrid }}, {"bm25", func(r row) cell { return r.bm25 }}, {"hybrid, no budget", func(r row) cell { return r.hybridAll }}} {
			t := sum(s, m.pick)
			low := ""
			if s == "calibration" {
				low = fmt.Sprintf(", %d of them among tasks 1, 3–9", t.low)
			}
			fmt.Fprintf(w, "- %s, %s: lands on %d of %d%s; reaches gold on %d of %d\n", s, m.name, t.lands, t.n, low, t.reached, t.n)
		}
	}
	t := sum("calibration", func(r row) cell { return r.hybrid })
	verdict := "not screened as specified (3 or fewer of 12)"
	switch {
	case t.lands >= 6 && t.low >= 3:
		verdict = "pass: the arm goes to a screen"
	case t.lands >= 4:
		verdict = "parked: reported to Francis with the no-budget figure"
	}
	if posthoc {
		p := sum("pilot", func(r row) cell { return r.hybrid })
		verdict = "does not pass"
		if t.lands >= 6 && p.lands >= 5 {
			verdict = "pass: the arm goes to a screen"
		}
		fmt.Fprintf(w, "\n**Gate 0b's rule (hybrid, %d tokens): %d of 12 calibration tasks land (at least 6 needed) and %d of %d pilot tasks (at least 5 needed): %s.** Among tasks 1, 3–9: %d, reported only.\n\n",
			budget, t.lands, p.lands, p.n, verdict, t.low)
	} else {
		fmt.Fprintf(w, "\n**§13.1's rule (calibration, hybrid, %d tokens): %d of 12 land, %d of them among tasks 1, 3–9: %s.**\n\n", budget, t.lands, t.low, verdict)
	}
	fmt.Fprintln(w, "## What each hybrid injection packed")
	for _, r := range rows {
		fmt.Fprintf(w, "- %s %d: gold %s; identifiers %v; packed %s\n", r.suite, r.task, spans(r.goldSpans), r.idents, strings.Join(r.firstFiles, ", "))
	}
}

func spans(s []pilot.LineSpan) string {
	var out []string
	for _, x := range s {
		out = append(out, fmt.Sprintf("%s:%d-%d", x.File, x.Start, x.End))
	}
	return strings.Join(out, ", ")
}

// posthoc is gate 0b's selection (§13.1): test and non-Go search hits and single-word lower-case identifiers dropped.
var posthoc bool

// injectSpans is window.InjectCandidates as spans.
func injectSpans(ctx context.Context, s *kensearch.Searcher, statement string) ([]pilot.LineSpan, error) {
	items, err := window.InjectCandidates(ctx, s, statement)
	var out []pilot.LineSpan
	for _, it := range items {
		out = append(out, pilot.LineSpan{File: it.File, Start: it.Start, End: it.End})
	}
	return out, err
}

// injectPack is window.InjectPack over spans.
func injectPack(root string, items []pilot.LineSpan, budget int) []pilot.LineSpan {
	var in []window.InjectItem
	for _, it := range items {
		in = append(in, window.InjectItem{File: it.File, Start: it.Start, End: it.End})
	}
	var out []pilot.LineSpan
	for _, it := range window.InjectPack(root, in, budget) {
		out = append(out, pilot.LineSpan{File: it.File, Start: it.Start, End: it.End})
	}
	return out
}
