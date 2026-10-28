// Command replaycontext scores captured E1 runs on trajectory context metrics (CB3;
// docs/measurements/e1-cb3-context-metrics-preregistration-2026-10-28.md). For each task it derives FixGold (non-test
// diff hunks at the base commit widened to their enclosing symbol in ken's tree-sitter index) and scores each run's
// events.jsonl against the base tree.
//
//	go run ./tools/replaycontext -mirrors DIR [-tasks FILE] [-gold FILE] [-dump FILE] [-include-notes] JOB/results.jsonl...
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/townsendmerino/tack/internal/eval/mine"
	"github.com/townsendmerino/tack/internal/eval/pilot"
	"github.com/townsendmerino/tack/tools/gate"
)

type goldCacheEntry struct {
	task int
	repo string
	base string
	comm string
	raw  []pilot.LineSpan
	sym  []pilot.LineSpan
}

type armAgg struct {
	job         string
	arm         string
	runs        int
	passes      int
	reached     int
	edited      int
	editedGold  int
	firstReqSum int
	finalCov    float64
	rawCov      float64
	fileCov     float64
	covReq1     float64
	cov4k       float64
	precision   float64
	aucCov      float64
	utilization float64
	editLoc     float64
	seenLines   int
	usedLines   int
	tokens      int
	annRuns     int
	annFinalCov float64
	annAUCCov   float64
}

func (a *armAgg) add(pass bool, sym, raw pilot.ContextMetrics, ann *pilot.ContextMetrics) {
	a.runs++
	if pass {
		a.passes++
	}
	if sym.FirstGoldReq > 0 {
		a.reached++
		a.firstReqSum += sym.FirstGoldReq
	}
	if sym.UsedLines > 0 {
		a.edited++
		if sym.EditLoc > 0 {
			a.editedGold++
		}
	}
	a.finalCov += sym.FinalCov
	a.rawCov += raw.FinalCov
	a.fileCov += sym.FileCov
	a.covReq1 += sym.CovReq1
	a.cov4k += sym.Cov4k
	a.precision += sym.Precision
	a.aucCov += sym.AUCCov
	a.utilization += sym.Utilization
	a.editLoc += sym.EditLoc
	a.seenLines += sym.SeenLines
	a.usedLines += sym.UsedLines
	a.tokens += sym.ToolOutputTokens
	if ann != nil {
		a.annRuns++
		a.annFinalCov += ann.FinalCov
		a.annAUCCov += ann.AUCCov
	}
}

type dumpRecord struct {
	Job       string                `json:"job"`
	Task      int                   `json:"task"`
	Repo      string                `json:"repo"`
	Arm       string                `json:"arm"`
	Rep       int                   `json:"rep,omitempty"`
	Pass      bool                  `json:"pass"`
	FixGold   pilot.ContextMetrics  `json:"fix_gold"`
	RawGold   pilot.ContextMetrics  `json:"raw_gold"`
	Annotated *pilot.ContextMetrics `json:"annotated_gold,omitempty"`
}

func main() {
	mirrors := flag.String("mirrors", "", "directory of bare mirrors, <repo>.git")
	scanDir := flag.String("scan", "", "scan directory for results.jsonl files")
	jobsFile := flag.String("jobs", "", "newline-separated file of results.jsonl paths")
	tasksFile := flag.String("tasks", "", "optional task JSONL file for test_files lookup")
	goldFile := flag.String("gold", "", "optional annotated gold JSONL file")
	dump := flag.String("dump", "", "write per-run ContextMetrics JSONL here")
	includeNotes := flag.Bool("include-notes", false, "count current_symbol notes on failed edits as seen spans")
	flag.Parse()

	resFiles, err := collectResultFiles(*scanDir, *jobsFile, flag.Args())
	if err != nil || *mirrors == "" || len(resFiles) == 0 {
		fmt.Println(gate.Verdict(gate.Inconclusive, "replaycontext: usage: -mirrors DIR [-scan DIR | -jobs FILE | JOB/results.jsonl...]"))
		os.Exit(2)
	}

	var dumpW io.Writer = io.Discard
	if *dump != "" {
		f, err := os.Create(*dump)
		if err != nil {
			fmt.Println(gate.Verdict(gate.Inconclusive, "replaycontext: "+err.Error()))
			os.Exit(2)
		}
		defer f.Close()
		dumpW = f
	}

	testFilesByCommit := loadTestFiles(*tasksFile)
	annotatedByTask, err := loadAnnotatedGold(*goldFile)
	if err != nil {
		fmt.Println(gate.Verdict(gate.Inconclusive, "replaycontext: "+err.Error()))
		os.Exit(2)
	}

	ctx := context.Background()
	goldCache := map[string]goldCacheEntry{}
	var goldOrder []string

	var aggs []*armAgg
	aggByKey := map[string]*armAgg{}
	getAgg := func(job, arm string) *armAgg {
		k := job + "\x00" + arm
		if a, ok := aggByKey[k]; ok {
			return a
		}
		a := &armAgg{job: job, arm: arm}
		aggByKey[k] = a
		aggs = append(aggs, a)
		return a
	}

	passAgg := &armAgg{job: "ALL", arm: "pass"}
	failAgg := &armAgg{job: "ALL", arm: "fail"}

	var totalRuns, missing int

	for _, res := range resFiles {
		f, err := os.Open(res)
		if err != nil {
			fmt.Println(gate.Verdict(gate.Inconclusive, "replaycontext: "+err.Error()))
			os.Exit(2)
		}
		job := jobLabel(res)
		isPass3 := strings.HasSuffix(filepath.ToSlash(res), "cal-baseline-2026-10-18/pass-3/results.jsonl")
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 1<<26)
		for sc.Scan() {
			var rec pilot.Record
			if json.Unmarshal(sc.Bytes(), &rec) != nil || rec.Run.Void != "" {
				continue
			}
			if isPass3 && rec.Task >= 5 {
				continue // tasks 5-12 of pass-3 were void and replaced by pass-3-rerun
			}
			totalRuns++
			base, err := pilot.BaseTree(*mirrors, rec.Repo, rec.Base)
			if err != nil {
				missing++
				fmt.Printf("MISSING base tree for %s@%s: %v\n", rec.Repo, rec.Base, err)
				continue
			}
			gk := rec.Repo + "@" + rec.Base + ".." + rec.Commit
			ge, ok := goldCache[gk]
			if !ok {
				tfs := testFilesByCommit[rec.Repo+"@"+rec.Commit]
				raw, sym, err := pilot.FixGold(ctx, *mirrors, rec.Repo, rec.Base, rec.Commit, tfs)
				if err != nil {
					missing++
					fmt.Printf("MISSING fix gold for %s: %v\n", gk, err)
					continue
				}
				ge = goldCacheEntry{task: rec.Task, repo: rec.Repo, base: rec.Base, comm: rec.Commit, raw: raw, sym: sym}
				goldCache[gk] = ge
				goldOrder = append(goldOrder, gk)
			}

			name := rec.Run.Arm
			if rec.Rep > 0 {
				name = fmt.Sprintf("%s-%d", rec.Run.Arm, rec.Rep)
			}
			events := filepath.Join(filepath.Dir(res), fmt.Sprintf("task-%02d", rec.Task), name, "events.jsonl")
			symM, err := pilot.ScoreContext(events, base, ge.sym, *includeNotes)
			if err != nil {
				missing++
				fmt.Printf("MISSING %s: %v\n", events, err)
				continue
			}
			rawM, err := pilot.ScoreContext(events, base, ge.raw, *includeNotes)
			if err != nil {
				missing++
				fmt.Printf("MISSING %s: %v\n", events, err)
				continue
			}
			var annM *pilot.ContextMetrics
			if annSpans, ok := annotatedByTask[fmt.Sprintf("%s@%s", rec.Repo, rec.Base)]; ok {
				m, err := pilot.ScoreContext(events, base, annSpans, *includeNotes)
				if err == nil {
					annM = &m
				}
			} else if annSpans, ok := annotatedByTask[fmt.Sprintf("task-%d", rec.Task)]; ok {
				m, err := pilot.ScoreContext(events, base, annSpans, *includeNotes)
				if err == nil {
					annM = &m
				}
			}

			getAgg(job, rec.Run.Arm).add(rec.Pass, symM, rawM, annM)
			if rec.Pass {
				passAgg.add(true, symM, rawM, annM)
			} else {
				failAgg.add(false, symM, rawM, annM)
			}

			if *dump != "" {
				b, _ := json.Marshal(dumpRecord{
					Job:       job,
					Task:      rec.Task,
					Repo:      rec.Repo,
					Arm:       rec.Run.Arm,
					Rep:       rec.Rep,
					Pass:      rec.Pass,
					FixGold:   symM,
					RawGold:   rawM,
					Annotated: annM,
				})
				fmt.Fprintln(dumpW, string(b))
			}
		}
		f.Close()
	}

	sort.Slice(goldOrder, func(i, j int) bool {
		a, b := goldCache[goldOrder[i]], goldCache[goldOrder[j]]
		if a.task != b.task {
			return a.task < b.task
		}
		return a.repo < b.repo
	})

	fmt.Println("task | repo | commit | raw_lines | gold_lines | fix_gold_spans")
	for _, gk := range goldOrder {
		ge := goldCache[gk]
		comm := ge.comm
		if len(comm) > 8 {
			comm = comm[:8]
		}
		fmt.Printf("%2d | %s | %s | %d | %d | %s\n",
			ge.task, ge.repo, comm, spanLineCount(ge.raw), spanLineCount(ge.sym), formatSpans(ge.sym))
	}
	fmt.Println()

	fmt.Println("job | arm | runs | pass | reached | edited_gold | FinalCov | RawCov | FileCov | CovReq1 | Cov4k | Precision | FirstReq | AUCCov | Util | EditLoc")
	for _, a := range aggs {
		printAggRow(a)
	}
	fmt.Println()
	fmt.Println("outcome_split | group | runs | pass | reached | edited_gold | FinalCov | RawCov | FileCov | CovReq1 | Cov4k | Precision | FirstReq | AUCCov | Util | EditLoc")
	if passAgg.runs > 0 {
		printAggRow(passAgg)
	}
	if failAgg.runs > 0 {
		printAggRow(failAgg)
	}

	scored := passAgg.runs + failAgg.runs
	sum := fmt.Sprintf("replaycontext: scored %d of %d runs across %d tasks (%d pass, %d fail; fail reached gold %d/%d, edited gold %d/%d)",
		scored, totalRuns, len(goldOrder), passAgg.runs, failAgg.runs, failAgg.reached, failAgg.runs, failAgg.editedGold, failAgg.runs)
	if missing > 0 {
		fmt.Println(gate.Verdict(gate.Inconclusive, fmt.Sprintf("%s; %d missing", sum, missing)))
		os.Exit(2)
	}
	fmt.Println(gate.Verdict(gate.OK, sum))
}

func printAggRow(a *armAgg) {
	if a.runs == 0 {
		return
	}
	n := float64(a.runs)
	firstReq := 0.0
	if a.reached > 0 {
		firstReq = float64(a.firstReqSum) / float64(a.reached)
	}
	fmt.Printf("%s | %s | %d | %d | %d/%d | %d/%d | %.3f | %.3f | %.3f | %.3f | %.3f | %.3f | %.2f | %.3f | %.3f | %.3f\n",
		a.job, a.arm, a.runs, a.passes, a.reached, a.runs, a.editedGold, a.runs,
		a.finalCov/n, a.rawCov/n, a.fileCov/n, a.covReq1/n, a.cov4k/n,
		a.precision/n, firstReq, a.aucCov/n, a.utilization/n, a.editLoc/n)
}

func collectResultFiles(scanDir, jobsFile string, args []string) ([]string, error) {
	var out []string
	if scanDir != "" {
		err := filepath.WalkDir(scanDir, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || d.Name() != "results.jsonl" {
				return nil
			}
			slash := filepath.ToSlash(p)
			if strings.Contains(slash, "aborted") || strings.Contains(slash, "/a1-2026-10-18/") {
				return nil
			}
			out = append(out, p)
			return nil
		})
		if err != nil {
			return nil, err
		}
		sort.Strings(out)
	}
	if jobsFile != "" {
		b, err := os.ReadFile(jobsFile)
		if err != nil {
			return nil, err
		}
		for _, line := range strings.Split(string(b), "\n") {
			if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
				out = append(out, line)
			}
		}
	}
	out = append(out, args...)
	return out, nil
}

func jobLabel(resPath string) string {
	dir := filepath.Dir(resPath)
	parent := filepath.Base(filepath.Dir(dir))
	base := filepath.Base(dir)
	if parent != "." && parent != "/" && parent != "e1" {
		return filepath.Join(parent, base)
	}
	return base
}

func loadTestFiles(extraPath string) map[string][]string {
	out := map[string][]string{}
	paths := []string{"eval/e1/calibration.jsonl", "eval/e1/pilot.jsonl"}
	if extraPath != "" {
		paths = append(paths, extraPath)
	}
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 1<<24)
		for sc.Scan() {
			var c mine.Candidate
			if json.Unmarshal(sc.Bytes(), &c) == nil && c.Repo != "" && c.Commit != "" && len(c.TestFiles) > 0 {
				out[c.Repo+"@"+c.Commit] = c.TestFiles
			}
		}
		f.Close()
	}
	return out
}

func loadAnnotatedGold(path string) (map[string][]pilot.LineSpan, error) {
	out := map[string][]pilot.LineSpan{}
	if path == "" {
		return out, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) == "" {
			continue
		}
		var g pilot.TaskGold
		if err := json.Unmarshal(sc.Bytes(), &g); err != nil {
			return nil, err
		}
		spans := pilot.MergeSpans(g.Spans)
		if g.Repo != "" && g.Base != "" {
			out[fmt.Sprintf("%s@%s", g.Repo, g.Base)] = spans
		}
		if g.Task > 0 {
			out[fmt.Sprintf("task-%d", g.Task)] = spans
		}
	}
	return out, sc.Err()
}

func spanLineCount(spans []pilot.LineSpan) int {
	n := 0
	for _, s := range pilot.MergeSpans(spans) {
		n += s.End - s.Start + 1
	}
	return n
}

func formatSpans(spans []pilot.LineSpan) string {
	var parts []string
	for _, s := range spans {
		parts = append(parts, fmt.Sprintf("%s:%d-%d", s.File, s.Start, s.End))
	}
	return strings.Join(parts, ", ")
}

