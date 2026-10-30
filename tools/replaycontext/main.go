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
	suite string
	task  int
	repo  string
	base  string
	comm  string
	raw   []pilot.LineSpan
	sym   []pilot.LineSpan
}

type armAgg struct {
	job         string
	arm         string
	runs        int
	passes      int
	reached     int
	seenRuns    int
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
	if sym.SeenLines > 0 {
		a.seenRuns++
		a.precision += sym.Precision
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
	os.Exit(run(os.Args[1:], os.Stdout))
}

func run(args []string, stdout io.Writer) int {
	fs := flag.NewFlagSet("replaycontext", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	mirrors := fs.String("mirrors", "", "directory of bare mirrors, <repo>.git")
	scanDir := fs.String("scan", "", "scan directory for results.jsonl files")
	jobsFile := fs.String("jobs", "", "newline-separated file of results.jsonl paths")
	tasksFile := fs.String("tasks", "", "optional task JSONL file for test_files lookup")
	goldFile := fs.String("gold", "", "optional annotated gold JSONL file")
	dump := fs.String("dump", "", "write per-run ContextMetrics JSONL here")
	includeNotes := fs.Bool("include-notes", false, "count current_symbol notes on failed edits as seen spans")
	includeSuperseded := fs.Bool("include-superseded", false, "include void/smoke/superseded runs when scanning")
	outlineGate := fs.Bool("outline-gate", false, "also count C7 §13.2's gate 0: runs whose whole-file read stopped at 200 lines on a gold file with a gold span past the cut")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(stdout, gate.Verdict(gate.Inconclusive, "replaycontext: "+err.Error()))
		return 2
	}

	resFiles, err := collectResultFiles(*scanDir, *jobsFile, fs.Args(), *includeSuperseded)
	if err != nil || *mirrors == "" || len(resFiles) == 0 {
		fmt.Fprintln(stdout, gate.Verdict(gate.Inconclusive, "replaycontext: usage: -mirrors DIR [-scan DIR | -jobs FILE | JOB/results.jsonl...]"))
		return 2
	}

	var dumpW io.Writer = io.Discard
	if *dump != "" {
		f, err := os.Create(*dump)
		if err != nil {
			fmt.Fprintln(stdout, gate.Verdict(gate.Inconclusive, "replaycontext: "+err.Error()))
			return 2
		}
		defer f.Close()
		dumpW = f
	}

	testFilesByCommit, suiteByCommit := loadTaskMetadata(*tasksFile)
	annotatedByTask, err := loadAnnotatedGold(*goldFile)
	if err != nil {
		fmt.Fprintln(stdout, gate.Verdict(gate.Inconclusive, "replaycontext: "+err.Error()))
		return 2
	}

	ctx := context.Background()
	goldCache := map[string]goldCacheEntry{}
	outline := &outlineTally{runs: map[string]int{}, seen: map[string]int{}}
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

	var taskAggs []*armAgg
	taskAggByKey := map[string]*armAgg{}
	getTaskAgg := func(ge goldCacheEntry) *armAgg {
		comm := ge.comm
		if len(comm) > 8 {
			comm = comm[:8]
		}
		label := fmt.Sprintf("%s:t%02d:%s@%s", ge.suite, ge.task, ge.repo, comm)
		if a, ok := taskAggByKey[label]; ok {
			return a
		}
		a := &armAgg{job: label, arm: "all"}
		taskAggByKey[label] = a
		taskAggs = append(taskAggs, a)
		return a
	}

	passAgg := &armAgg{job: "ALL", arm: "pass"}
	failAgg := &armAgg{job: "ALL", arm: "fail"}
	failNoGoldAgg := &armAgg{job: "FAIL_MODE", arm: "first_gold_req==0"}
	failReadNoEditAgg := &armAgg{job: "FAIL_MODE", arm: "reached_no_gold_edit"}
	failEditedGoldAgg := &armAgg{job: "FAIL_MODE", arm: "edited_gold"}

	var totalRuns, missing int

	for _, res := range resFiles {
		f, err := os.Open(res)
		if err != nil {
			fmt.Fprintln(stdout, gate.Verdict(gate.Inconclusive, "replaycontext: "+err.Error()))
			return 2
		}
		job := jobLabel(res, *includeSuperseded)
		slash := filepath.ToSlash(res)
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 1<<26)
		for sc.Scan() {
			var rec pilot.Record
			if json.Unmarshal(sc.Bytes(), &rec) != nil || shouldSkipRecord(slash, rec, *includeSuperseded) {
				continue
			}
			totalRuns++
			base, err := pilot.BaseTree(*mirrors, rec.Repo, rec.Base)
			if err != nil {
				missing++
				fmt.Fprintf(stdout, "MISSING base tree for %s@%s: %v\n", rec.Repo, rec.Base, err)
				continue
			}
			gk := rec.Repo + "@" + rec.Base + ".." + rec.Commit
			ge, ok := goldCache[gk]
			if !ok {
				commitKey := rec.Repo + "@" + rec.Commit
				tfs := testFilesByCommit[commitKey]
				suite := suiteByCommit[commitKey]
				if suite == "" {
					if strings.Contains(slash, "/a1-") {
						suite = "pilot"
					} else {
						suite = "calibration"
					}
				}
				raw, sym, err := pilot.FixGold(ctx, *mirrors, rec.Repo, rec.Base, rec.Commit, tfs)
				if err != nil {
					missing++
					fmt.Fprintf(stdout, "MISSING fix gold for %s: %v\n", gk, err)
					continue
				}
				ge = goldCacheEntry{suite: suite, task: rec.Task, repo: rec.Repo, base: rec.Base, comm: rec.Commit, raw: raw, sym: sym}
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
				fmt.Fprintf(stdout, "MISSING %s: %v\n", events, err)
				continue
			}
			rawM, err := pilot.ScoreContext(events, base, ge.raw, *includeNotes)
			if err != nil {
				missing++
				fmt.Fprintf(stdout, "MISSING %s: %v\n", events, err)
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
			getTaskAgg(ge).add(rec.Pass, symM, rawM, annM)
			if *outlineGate {
				hit, err := outlineHit(events, base, ge.sym)
				if err != nil {
					missing++
					fmt.Fprintf(stdout, "MISSING outline gate for %s: %v\n", events, err)
				}
				outline.add(ge.suite, ge.task, hit)
			}
			if rec.Pass {
				passAgg.add(true, symM, rawM, annM)
			} else {
				failAgg.add(false, symM, rawM, annM)
				if symM.FirstGoldReq == 0 {
					failNoGoldAgg.add(false, symM, rawM, annM)
				} else if symM.EditLoc == 0 {
					failReadNoEditAgg.add(false, symM, rawM, annM)
				} else {
					failEditedGoldAgg.add(false, symM, rawM, annM)
				}
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
		if a.suite != b.suite {
			return a.suite < b.suite
		}
		if a.task != b.task {
			return a.task < b.task
		}
		return a.repo < b.repo
	})

	fmt.Fprintln(stdout, "suite | task | repo | commit | raw_lines | gold_lines | fix_gold_spans")
	for _, gk := range goldOrder {
		ge := goldCache[gk]
		comm := ge.comm
		if len(comm) > 8 {
			comm = comm[:8]
		}
		fmt.Fprintf(stdout, "%s | %2d | %s | %s | %d | %d | %s\n",
			ge.suite, ge.task, ge.repo, comm, spanLineCount(ge.raw), spanLineCount(ge.sym), formatSpans(ge.sym))
	}
	fmt.Fprintln(stdout)

	fmt.Fprintln(stdout, "job | arm | runs | pass | reached | edited_gold | FinalCov | RawCov | FileCov | CovReq1 | Cov4k | Precision | FirstReq | AUCCov | Util | EditLoc")
	for _, a := range aggs {
		printAggRow(stdout, a)
	}
	fmt.Fprintln(stdout)

	sort.Slice(taskAggs, func(i, j int) bool {
		return taskAggs[i].job < taskAggs[j].job
	})
	fmt.Fprintln(stdout, "task_group | arm | runs | pass | reached | edited_gold | FinalCov | RawCov | FileCov | CovReq1 | Cov4k | Precision | FirstReq | AUCCov | Util | EditLoc")
	for _, a := range taskAggs {
		printAggRow(stdout, a)
	}
	fmt.Fprintln(stdout)

	fmt.Fprintln(stdout, "outcome_split | group | runs | pass | reached | edited_gold | FinalCov | RawCov | FileCov | CovReq1 | Cov4k | Precision | FirstReq | AUCCov | Util | EditLoc")
	if passAgg.runs > 0 {
		printAggRow(stdout, passAgg)
	}
	if failAgg.runs > 0 {
		printAggRow(stdout, failAgg)
	}
	if failNoGoldAgg.runs > 0 {
		printAggRow(stdout, failNoGoldAgg)
	}
	if failReadNoEditAgg.runs > 0 {
		printAggRow(stdout, failReadNoEditAgg)
	}
	if failEditedGoldAgg.runs > 0 {
		printAggRow(stdout, failEditedGoldAgg)
	}

	if *outlineGate {
		outline.report(stdout)
	}
	scored := passAgg.runs + failAgg.runs
	sum := fmt.Sprintf("replaycontext: scored %d of %d runs across %d tasks (%d pass, %d fail; fail reached gold %d/%d, edited gold %d/%d)",
		scored, totalRuns, len(goldOrder), passAgg.runs, failAgg.runs, failAgg.reached, failAgg.runs, failAgg.editedGold, failAgg.runs)
	if missing > 0 {
		fmt.Fprintln(stdout, gate.Verdict(gate.Inconclusive, fmt.Sprintf("%s; %d missing", sum, missing)))
		return 2
	}
	fmt.Fprintln(stdout, gate.Verdict(gate.OK, sum))
	return 0
}

func printAggRow(w io.Writer, a *armAgg) {
	if a.runs == 0 {
		return
	}
	n := float64(a.runs)
	firstReq := 0.0
	if a.reached > 0 {
		firstReq = float64(a.firstReqSum) / float64(a.reached)
	}
	prec := 0.0
	if a.seenRuns > 0 {
		prec = a.precision / float64(a.seenRuns)
	}
	fmt.Fprintf(w, "%s | %s | %d | %d | %d/%d | %d/%d | %.3f | %.3f | %.3f | %.3f | %.3f | %.3f | %.2f | %.3f | %.3f | %.3f\n",
		a.job, a.arm, a.runs, a.passes, a.reached, a.runs, a.editedGold, a.runs,
		a.finalCov/n, a.rawCov/n, a.fileCov/n, a.covReq1/n, a.cov4k/n,
		prec, firstReq, a.aucCov/n, a.utilization/n, a.editLoc/n)
}

func isExcludedResultFile(slash string, includeSuperseded bool) bool {
	// Always exclude void pilot captures (sandbox denied workspace writes / module cache;
	// replaced by a1-2026-10-18-day2 per e1-pilot-a1-2026-10-18.md), aborted launches,
	// and post-CB3 runs (c6-bundle-2026-10-28, c6-qwen35-row-2026-10-29, c6-powered-2026-10-30), so -scan stays
	// CB3's set.
	if strings.Contains(slash, "aborted") ||
		strings.Contains(slash, "/a1-2026-10-18/") ||
		strings.Contains(slash, "/c6-bundle-2026-10-28/") || strings.Contains(slash, "/c6-qwen35-row-2026-10-29/") ||
		strings.Contains(slash, "/c6-powered-2026-10-30/") {
		return true
	}
	if includeSuperseded {
		return false
	}
	// Exclude void/smoke/superseded runs documented as not counted in the E1 records:
	// - a1-2026-10-18-day: void pilot run (sandbox denied Go module cache; e1-pilot-a1-2026-10-18.md)
	// - a1-smoke-* and */smoke-t1/*: 1-task preflight smoke runs ("not graded")
	// - c6-42-2026-10-20/runs: stopped screen superseded by c6-42-2026-10-20/runs-restart
	//   ("recorded, not counted"; e1-calibration-c6-42-2026-10-20.md, e1-calibration-c6-42-stopped-2026-10-20.md)
	return strings.Contains(slash, "/a1-2026-10-18-day/") ||
		strings.Contains(slash, "/a1-smoke-") ||
		strings.Contains(slash, "/smoke-t1/") ||
		strings.HasSuffix(slash, "/c6-42-2026-10-20/runs/results.jsonl")
}

func shouldSkipRecord(slash string, rec pilot.Record, includeSuperseded bool) bool {
	if rec.Run.Void != "" {
		return true
	}
	// Tasks 5-12 of cal-baseline-2026-10-18/pass-3 were void and replaced by pass-3-rerun
	// (e1-calibration-baseline-2026-10-18.md).
	if strings.HasSuffix(slash, "cal-baseline-2026-10-18/pass-3/results.jsonl") && rec.Task >= 5 {
		return true
	}
	if includeSuperseded {
		return false
	}
	// Per e1-calibration-c6-41-2026-10-20.md (§12.10 completeness rule), c6-41-2026-10-20
	// counts tasks 1-3 from runs/, tasks 4-5 from runs-resume/, and tasks 6-12 from runs-resume2/;
	// incomplete interrupted task blocks (runs/ task 4 and runs-resume/ task 6) are not counted.
	if strings.HasSuffix(slash, "c6-41-2026-10-20/runs/results.jsonl") && rec.Task >= 4 {
		return true
	}
	if strings.HasSuffix(slash, "c6-41-2026-10-20/runs-resume/results.jsonl") && rec.Task >= 6 {
		return true
	}
	return false
}

func collectResultFiles(scanDir, jobsFile string, args []string, includeSuperseded bool) ([]string, error) {
	var out []string
	if scanDir != "" {
		err := filepath.WalkDir(scanDir, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || d.Name() != "results.jsonl" {
				return nil
			}
			slash := filepath.ToSlash(p)
			if isExcludedResultFile(slash, includeSuperseded) {
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

func jobLabel(resPath string, includeSuperseded bool) string {
	dir := filepath.Dir(resPath)
	parent := filepath.Base(filepath.Dir(dir))
	base := filepath.Base(dir)
	if !includeSuperseded {
		if strings.HasPrefix(base, "pass-") && parent != "." && parent != "/" && parent != "e1" {
			return parent
		}
		if parent == "c6-41-2026-10-20" && strings.HasPrefix(base, "runs") {
			return parent
		}
	}
	if parent != "." && parent != "/" && parent != "e1" {
		return filepath.Join(parent, base)
	}
	return base
}

func loadTaskMetadata(extraPath string) (map[string][]string, map[string]string) {
	testFiles := map[string][]string{}
	suites := map[string]string{}
	type source struct {
		path  string
		suite string
	}
	sources := []source{
		{path: "eval/e1/calibration.jsonl", suite: "calibration"},
		{path: "eval/e1/pilot.jsonl", suite: "pilot"},
	}
	if extraPath != "" {
		suitesName := "custom"
		if strings.Contains(extraPath, "pilot") {
			suitesName = "pilot"
		} else if strings.Contains(extraPath, "calibration") {
			suitesName = "calibration"
		}
		sources = append(sources, source{path: extraPath, suite: suitesName})
	}
	for _, src := range sources {
		f, err := os.Open(src.path)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 1<<24)
		for sc.Scan() {
			var c mine.Candidate
			if json.Unmarshal(sc.Bytes(), &c) == nil && c.Repo != "" && c.Commit != "" {
				key := c.Repo + "@" + c.Commit
				if len(c.TestFiles) > 0 {
					testFiles[key] = c.TestFiles
				}
				if suites[key] == "" {
					suites[key] = src.suite
				}
			}
		}
		f.Close()
	}
	return testFiles, suites
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
