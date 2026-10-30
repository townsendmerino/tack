// Command repomapgate is C7's gate 0 for the generated repository map (e1-calibration-preregistration-2026-10-18.md
// §13.3), with no model: for each E1 task it builds the map §13.3 fixes from ken's structural index over the base tree,
// and the instruction head a run was shown there, and counts which of the two names the gold file and the gold symbol.
//
//	go run ./tools/repomapgate -mirrors DIR [-dump DIR]
//
// It ends with VERDICT: PASS when every task was measured, and FAIL when one could not be. The gate's own outcome is a
// line of the report, read under §13.3's rule.
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/townsendmerino/aikit/chunk"
	"github.com/townsendmerino/aikit/chunk/treesitter"

	"github.com/townsendmerino/tack/internal/core"
	"github.com/townsendmerino/tack/internal/eval/pilot"
	"github.com/townsendmerino/tack/internal/kensearch"
	"github.com/townsendmerino/tack/internal/tools"
	"github.com/townsendmerino/tack/tools/gate"
)

// §13.3's constants.
const (
	mapBytes      = 3000 // 1,000 tokens at bytes / 3
	symbolsPerRow = 8
	headWindow    = 16384
	mapTitle      = "Repository map (the most-referenced Go files and their exported symbols):"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, w, logw io.Writer) int {
	fl := flag.NewFlagSet("repomapgate", flag.ContinueOnError)
	fl.SetOutput(io.Discard)
	mirrors := fl.String("mirrors", "", "the E1 mirrors directory (<repo>.git)")
	dump := fl.String("dump", "", "write each task's map and head here")
	if err := fl.Parse(args); err != nil || *mirrors == "" {
		fmt.Fprintln(w, gate.Verdict(gate.Inconclusive, "repomapgate: usage: -mirrors DIR [-dump DIR]"))
		return 2
	}
	chunk.Register(kensearch.Chunker, treesitter.New(treesitter.WithParseTimeoutMicros(1_000_000)))
	var rows []row
	for _, s := range []struct{ name, path string }{{"calibration", "eval/e1/calibration.jsonl"}, {"pilot", "eval/e1/pilot.jsonl"}} {
		ts, err := pilot.LoadTasks(s.path, 1, 1<<30)
		if err != nil {
			fmt.Fprintln(w, gate.Verdict(gate.Fail, "repomapgate: "+err.Error()))
			return 1
		}
		for _, t := range ts {
			fmt.Fprintf(logw, "%s task %d (%s %s)\n", s.name, t.N, t.Repo, t.Base[:8])
			r, err := measure(context.Background(), *mirrors, s.name, t, *dump)
			if err != nil {
				fmt.Fprintln(w, gate.Verdict(gate.Fail, fmt.Sprintf("repomapgate: %s task %d: %v", s.name, t.N, err)))
				return 1
			}
			rows = append(rows, r)
		}
	}
	report(w, rows)
	fmt.Fprintln(w, gate.Verdict(gate.OK, fmt.Sprintf("repomapgate: %d tasks measured", len(rows))))
	return 0
}

type row struct {
	suite, repo                 string
	task, mapLen, headLen, rows int
	mapFile, mapSym             bool
	headFile, headSym           bool
	goldFiles                   []string
}

func measure(ctx context.Context, mirrors, suite string, t pilot.Task, dump string) (row, error) {
	r := row{suite: suite, repo: t.Repo, task: t.N}
	_, gold, err := pilot.FixGold(ctx, mirrors, t.Repo, t.Base, t.Commit, t.TestFiles)
	if err != nil {
		return r, err
	}
	tmp, err := os.MkdirTemp("", "tack-repomapgate-*")
	if err != nil {
		return r, err
	}
	defer os.RemoveAll(tmp)
	if err := archive(mirrors, t.Repo, t.Base, tmp); err != nil {
		return r, err
	}
	head := core.InstructionHead(tmp, headWindow)
	s, err := kensearch.Open(ctx, tmp, kensearch.Options{Mode: "bm25"})
	if err != nil {
		return r, err
	}
	defer s.Close()
	m, defs, err := buildMap(ctx, s, tmp)
	if err != nil {
		return r, err
	}
	r.mapLen, r.headLen, r.rows = len(m), len(head), strings.Count(m, "\n")
	seen := map[string]bool{}
	for _, g := range gold {
		if !seen[g.File] {
			seen[g.File] = true
			r.goldFiles = append(r.goldFiles, g.File)
		}
		r.mapFile = r.mapFile || strings.Contains(m, g.File)
		r.headFile = r.headFile || strings.Contains(head, g.File)
		for _, d := range defs[g.File] {
			if d.StartLine > g.End || d.EndLine < g.Start {
				continue
			}
			r.mapSym = r.mapSym || strings.Contains(m, d.Name)
			r.headSym = r.headSym || wholeWord(head, leaf(d.Name))
		}
	}
	if dump != "" {
		name := fmt.Sprintf("%s-%02d", suite, t.N)
		os.MkdirAll(dump, 0o755)
		os.WriteFile(filepath.Join(dump, name+".map.txt"), []byte(m), 0o644)
		os.WriteFile(filepath.Join(dump, name+".head.txt"), []byte(head), 0o644)
	}
	return r, nil
}

var word = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)

// buildMap is §13.3's map over root, and each mapped file's exported definitions, by path.
func buildMap(ctx context.Context, s *kensearch.Searcher, root string) (string, map[string][]tools.Def, error) {
	var goFiles, mapFiles []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		goFiles = append(goFiles, rel)
		if !strings.HasSuffix(rel, "_test.go") && !strings.Contains("/"+rel, "/testdata/") && !strings.Contains("/"+rel, "/vendor/") {
			mapFiles = append(mapFiles, rel)
		}
		return nil
	})
	if err != nil {
		return "", nil, err
	}
	// files[w]: how many .go files contain the word w.
	files := map[string]int{}
	for _, f := range goFiles {
		b, _ := os.ReadFile(filepath.Join(root, filepath.FromSlash(f)))
		set := map[string]bool{}
		for _, w := range word.FindAllString(string(b), -1) {
			set[w] = true
		}
		for w := range set {
			files[w]++
		}
	}
	type sym struct {
		name string
		refs int
	}
	type file struct {
		path  string
		score int
		syms  []sym
	}
	var ranked []file
	defs := map[string][]tools.Def{}
	for _, f := range mapFiles {
		out, err := s.Outline(ctx, f)
		if err != nil {
			continue // not indexed: no structure to map
		}
		fi := file{path: f}
		for _, d := range out {
			if !exported(leaf(d.Name)) {
				continue
			}
			defs[f] = append(defs[f], d)
			n := max(0, files[leaf(d.Name)]-1) // other files: its own file holds the name
			fi.syms = append(fi.syms, sym{d.Name, n})
			fi.score += n
		}
		if len(fi.syms) == 0 {
			continue
		}
		sort.SliceStable(fi.syms, func(i, j int) bool { return fi.syms[i].refs > fi.syms[j].refs })
		ranked = append(ranked, fi)
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].path < ranked[j].path
	})
	var b strings.Builder
	b.WriteString(mapTitle + "\n")
	for _, f := range ranked {
		var names []string
		for i, s := range f.syms {
			if i == symbolsPerRow {
				break
			}
			names = append(names, s.name)
		}
		line := f.path + ": " + strings.Join(names, ", ") + "\n"
		if b.Len()+len(line) > mapBytes {
			break
		}
		b.WriteString(line)
	}
	return b.String(), defs, nil
}

func leaf(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[i+1:]
	}
	return name
}

func exported(name string) bool {
	for _, r := range name {
		return unicode.IsUpper(r)
	}
	return false
}

func wholeWord(text, w string) bool {
	return w != "" && regexp.MustCompile(`\b`+regexp.QuoteMeta(w)+`\b`).MatchString(text)
}

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

func yes(b bool) string {
	if b {
		return "**yes**"
	}
	return "no"
}

func report(w io.Writer, rows []row) {
	fmt.Fprintln(w, "# repomapgate: C7 §13.3 gate 0, the generated repository map against the instruction head")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| suite | task | repo | gold files | map bytes (files) | map names gold file | gold symbol | head bytes | head names gold file | gold symbol |")
	fmt.Fprintln(w, "|---|---|---|---|---|---|---|---|---|---|")
	mapN, headN, mapS, headS := 0, 0, 0, 0
	for _, r := range rows {
		fmt.Fprintf(w, "| %s | %d | %s | %s | %d (%d) | %s | %s | %d | %s | %s |\n", r.suite, r.task, r.repo, strings.Join(r.goldFiles, ", "),
			r.mapLen, r.rows-1, yes(r.mapFile), yes(r.mapSym), r.headLen, yes(r.headFile), yes(r.headSym))
		for _, c := range []struct {
			b bool
			n *int
		}{{r.mapFile, &mapN}, {r.headFile, &headN}, {r.mapSym, &mapS}, {r.headSym, &headS}} {
			if c.b {
				*c.n++
			}
		}
	}
	verdict := "not screened"
	if mapN >= headN+6 {
		verdict = "goes to a screen"
	}
	fmt.Fprintf(w, "\n- gold file named: map %d of %d, head %d of %d\n- gold symbol named (reported only): map %d, head %d\n",
		mapN, len(rows), headN, len(rows), mapS, headS)
	fmt.Fprintf(w, "\n**§13.3's rule (the map names the gold file on at least 6 more tasks than the head): map %d, head %d: %s.**\n", mapN, headN, verdict)
}
