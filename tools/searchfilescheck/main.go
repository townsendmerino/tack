// Command searchfilescheck is P1 step 5's by-day exactness check of ken's SearchFiles (DESIGN §12, K9 "On adoption";
// docs/design/p1-notes.md, step 5). ken's SearchFiles pulls at most 5,000 candidates from the whole repository's
// ranking and keeps those in the files asked for, so on a larger index it can miss a hit. For each query, this compares
// kensearch.SearchFile with the exact answer, the whole ranking (k = the index's size) filtered to the file, at each k.
//
//	go run ./tools/searchfilescheck -root DIR [-files CLAUDE.md,AGENTS.md] [-mode bm25] [-model-dir DIR]
//
// It reads DIR and writes nothing: ken opens without a snapshot or a watcher. The queries are every heading of each
// file and a fixed list of task-like messages. It reports the index's size and, per file and k, the queries whose
// answer differs, how many of the exact answers' hits it returned, and the deepest whole-ranking rank an exact answer
// needed; it takes no timings. It ends with VERDICT: PASS when every answer is exact, FAIL when one is not, and
// INCONCLUSIVE when nothing could be checked.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/townsendmerino/aikit/chunk"
	"github.com/townsendmerino/aikit/chunk/treesitter"

	"github.com/townsendmerino/tack/internal/kensearch"
	"github.com/townsendmerino/tack/internal/window"
	"github.com/townsendmerino/tack/tools/gate"
)

// ks are the answer sizes checked: a few sections for a message, and a deep list.
var ks = []int{5, 20}

// messages are task-like user messages, the kind ken's route runs SearchFiles over (DESIGN §2.3).
var messages = []string{
	"fix the rename bug",
	"add a flag to serve for the context size",
	"stage and commit the change",
	"run the tests for the package I touched",
	"why does staticcheck pass on an empty package",
	"queue the long benchmark for tonight",
	"the prefill is slow on Metal",
	"bump the pinned version and tag a release",
	"which model files should tests read",
	"write the measurement record for this run",
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, w, logw io.Writer) int {
	fl := flag.NewFlagSet("searchfilescheck", flag.ContinueOnError)
	fl.SetOutput(io.Discard)
	root := fl.String("root", "", "the repository to check (a goinfer checkout)")
	files := fl.String("files", "CLAUDE.md,AGENTS.md", "the instruction files to search, from the root; missing ones are skipped")
	mode := fl.String("mode", "bm25", "ken's mode: bm25, semantic, hybrid or hybrid-rerank")
	modelDir := fl.String("model-dir", "", "the Model2Vec directory, for every mode but bm25")
	if err := fl.Parse(args); err != nil || *root == "" {
		fmt.Fprintln(w, gate.Verdict(gate.Inconclusive, "searchfilescheck: usage: -root DIR [-files A,B] [-mode M] [-model-dir DIR]"))
		return 2
	}
	chunk.Register(kensearch.Chunker, treesitter.New(treesitter.WithParseTimeoutMicros(1_000_000)))
	ctx := context.Background()
	s, err := kensearch.Open(ctx, *root, kensearch.Options{Mode: *mode, ModelDir: *modelDir, Log: logw})
	if err != nil {
		fmt.Fprintln(w, gate.Verdict(gate.Fail, "searchfilescheck: "+err.Error()))
		return 1
	}
	defer s.Close()
	n := s.Chunks()
	fmt.Fprintf(w, "index: %d chunks, mode %s (SearchFiles is exact by construction up to 5000)\n", n, *mode)
	checked, wrong := 0, 0
	for f := range strings.SplitSeq(*files, ",") {
		b, err := os.ReadFile(filepath.Join(*root, filepath.FromSlash(f)))
		if err != nil {
			fmt.Fprintf(w, "%s: skipped (%v)\n", f, err)
			continue
		}
		queries := slices.Clone(messages)
		for _, sec := range window.ParseSections(string(b)) {
			if h := sec.Heading(); h != "" {
				queries = append(queries, h)
			}
		}
		for _, k := range ks {
			r, err := check(ctx, s, f, queries, k, n)
			if err != nil {
				fmt.Fprintln(w, gate.Verdict(gate.Fail, fmt.Sprintf("searchfilescheck: %s: %v", f, err)))
				return 1
			}
			checked += len(queries)
			wrong += len(r.differs)
			fmt.Fprintf(w, "%s k=%d: %d queries, %d differ, %d with no hit; %d of %d exact hits returned; deepest rank an exact "+
				"answer needed: %d\n", f, k, len(queries), len(r.differs), r.empty, r.found, r.exact, r.deepest)
			for _, d := range r.differs {
				fmt.Fprintf(w, "  differs: %q\n", d)
			}
		}
	}
	switch {
	case checked == 0:
		fmt.Fprintln(w, gate.Verdict(gate.Inconclusive, "searchfilescheck: no instruction file found to check"))
		return 2
	case wrong > 0:
		fmt.Fprintln(w, gate.Verdict(gate.Fail, fmt.Sprintf("searchfilescheck: %d of %d answers differ from the exact ranking", wrong, checked)))
		return 1
	}
	fmt.Fprintln(w, gate.Verdict(gate.OK, fmt.Sprintf("searchfilescheck: %d of %d answers exact over %d chunks", checked, checked, n)))
	return 0
}

type result struct {
	differs []string
	empty   int
	exact   int // hits in the exact answers
	found   int // of those, the ones SearchFile returned
	deepest int // the deepest whole-ranking rank among the exact answers' hits
}

// check compares SearchFile with the whole ranking filtered to file, for each query at k.
func check(ctx context.Context, s *kensearch.Searcher, file string, queries []string, k, n int) (result, error) {
	var r result
	for _, q := range queries {
		got, err := s.SearchFile(ctx, q, k, file)
		if err != nil {
			return r, err
		}
		all, err := s.Search(ctx, q, n)
		if err != nil {
			return r, err
		}
		var want []kensearch.SectionHit
		for i, h := range all {
			if h.File != file {
				continue
			}
			if len(want) == k {
				break
			}
			want = append(want, kensearch.SectionHit{StartLine: h.StartLine, EndLine: h.EndLine, Score: h.Score})
			r.deepest = max(r.deepest, i+1)
		}
		if len(want) == 0 {
			r.empty++
		}
		r.exact += len(want)
		for _, h := range want {
			for _, g := range got {
				if g.StartLine == h.StartLine && g.EndLine == h.EndLine {
					r.found++
					break
				}
			}
		}
		if !same(got, want) {
			r.differs = append(r.differs, q)
		}
	}
	return r, nil
}

// same compares two answers by span and score, in order.
func same(a, b []kensearch.SectionHit) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].StartLine != b[i].StartLine || a[i].EndLine != b[i].EndLine || a[i].Score != b[i].Score {
			return false
		}
	}
	return true
}
