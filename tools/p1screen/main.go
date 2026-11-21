// Command p1screen reads the P1 screen (docs/measurements/e1-full-run-p1-rule-registration-2026-11-16.md) from its
// results.jsonl, as registered: per task, each arm's pass as 1 or 0; d = A1 − A0 per task, averaged over the tasks, in
// points; its one-sided 95% lower bound by bootstrap over tasks (10,000 resamples of the tasks with replacement, the
// 5th percentile of their means, seed 1); and the verdict against the margin m:
//
//	lower bound ≥ −m              PASS: the screen passes
//	−m − band ≤ lower bound < −m  INCONCLUSIVE: parked, Francis decides
//	lower bound < −m − band       FAIL: a miss
//
// It reads INCONCLUSIVE, never PASS, when A0 passes fewer than floor tasks (no power to see A1 trail it), and when the
// screen is not whole: a task without exactly one counted run of each arm, or a task count other than -n. A run is void,
// in no count, when the pilot voided it or its oracle failed to run. The capped-time ratio, each arm's cap hits and the
// per-repo passes are printed and decide nothing.
//
//	go run ./tools/p1screen [-m 29] [-band 8] [-n 20] [-floor 3] SCREEN/results.jsonl
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"slices"
	"sort"

	"github.com/townsendmerino/tack/internal/eval/pilot"
	"github.com/townsendmerino/tack/tools/gate"
)

// The registered bootstrap: resamples and seed are fixed before the run.
const (
	resamples = 10000
	seed      = 1
)

func main() { os.Exit(run(os.Args[1:], os.Stdout)) }

func run(args []string, w io.Writer) int {
	fs := flag.NewFlagSet("p1screen", flag.ContinueOnError)
	fs.SetOutput(w)
	m := fs.Float64("m", 29, "the margin, in points (Amendment 1: 29)")
	band := fs.Float64("band", 8, "the parked band below -m, in points")
	n := fs.Int("n", 20, "the registered task count")
	floor := fs.Int("floor", 3, "the fewest A0 passes the screen reads at; under it, INCONCLUSIVE")
	if err := fs.Parse(args); err != nil || fs.NArg() != 1 {
		fmt.Fprintln(w, gate.Verdict(gate.Inconclusive, "p1screen: usage: [-m 29] [-band 8] [-n 20] [-floor 3] SCREEN/results.jsonl"))
		return 2
	}
	recs, err := load(fs.Arg(0))
	if err != nil {
		fmt.Fprintln(w, gate.Verdict(gate.Fail, "p1screen: "+err.Error()))
		return 1
	}
	r, err := read(recs, *n)
	if err != nil {
		fmt.Fprintln(w, gate.Verdict(gate.Inconclusive, "p1screen: "+err.Error()))
		return 1
	}
	r.print(w)
	o, why := r.verdict(*m, *band, *floor)
	fmt.Fprintln(w, gate.Verdict(o, fmt.Sprintf("p1screen: %s (d %+.1f, lower bound %+.1f, m %.0f, A0 %d/%d, A1 %d/%d)",
		why, r.d, r.lower, *m, r.passes["A0"], r.tasks, r.passes["A1"], r.tasks)))
	if o != gate.OK {
		return 1
	}
	return 0
}

func load(path string) ([]pilot.Record, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []pilot.Record
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for line := 1; sc.Scan(); line++ {
		var r pilot.Record
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			return nil, fmt.Errorf("%s line %d: %w", path, line, err)
		}
		out = append(out, r)
	}
	return out, sc.Err()
}

// screen is the read: per task, each arm's pass; and what is reported beside the verdict.
type screen struct {
	tasks    int
	byTask   []int // task numbers, in order
	pass     map[int]map[string]bool
	repo     map[int]string
	passes   map[string]int
	capped   map[string]float64 // capped seconds, summed (PAR-1)
	capHits  map[string]int
	void     int
	d, lower float64 // points
	diffs    []float64
}

// read pairs each task's counted A0 and A1 runs. A task needs exactly one of each, and the screen exactly n tasks.
func read(recs []pilot.Record, n int) (*screen, error) {
	s := &screen{pass: map[int]map[string]bool{}, repo: map[int]string{}, passes: map[string]int{}, capped: map[string]float64{},
		capHits: map[string]int{}}
	count := map[int]map[string]int{}
	for _, r := range recs {
		if r.Run.Void != "" || r.OracleErr != "" {
			s.void++
			continue
		}
		arm := r.Run.Arm
		if arm != "A0" && arm != "A1" {
			return nil, fmt.Errorf("task %d: arm %q is not one of the screen's (A0, A1)", r.Task, arm)
		}
		if count[r.Task] == nil {
			count[r.Task], s.pass[r.Task] = map[string]int{}, map[string]bool{}
			s.byTask = append(s.byTask, r.Task)
		}
		count[r.Task][arm]++
		s.pass[r.Task][arm] = r.Pass
		s.repo[r.Task] = r.Repo
		s.capped[arm] += r.CappedSecs
		if r.Run.CapHit {
			s.capHits[arm]++
		}
	}
	for _, t := range s.byTask {
		if count[t]["A0"] != 1 || count[t]["A1"] != 1 {
			return nil, fmt.Errorf("task %d has %d counted A0 runs and %d A1: the screen is one of each", t, count[t]["A0"], count[t]["A1"])
		}
	}
	s.tasks = len(s.byTask)
	if s.tasks != n {
		return nil, fmt.Errorf("%d tasks have counted runs; the screen registers %d", s.tasks, n)
	}
	for _, t := range s.byTask {
		a0, a1 := b2f(s.pass[t]["A0"]), b2f(s.pass[t]["A1"])
		s.passes["A0"] += int(a0)
		s.passes["A1"] += int(a1)
		s.diffs = append(s.diffs, 100*(a1-a0))
	}
	s.d = mean(s.diffs)
	s.lower = lowerBound(s.diffs)
	return s, nil
}

// lowerBound is the one-sided 95% lower bound on the mean of diffs: the 5th percentile of the means of resamples
// bootstrap resamples of diffs, drawn with replacement with the registered seed. The percentile is the value at index
// ⌊0.05 · resamples⌋ of the sorted means.
func lowerBound(diffs []float64) float64 {
	rng := rand.New(rand.NewPCG(seed, seed))
	means := make([]float64, resamples)
	for i := range means {
		var sum float64
		for range diffs {
			sum += diffs[rng.IntN(len(diffs))]
		}
		means[i] = sum / float64(len(diffs))
	}
	sort.Float64s(means)
	return means[resamples/20]
}

// verdict is the registered rule on the lower bound, after the floor.
func (s *screen) verdict(m, band float64, floor int) (gate.Outcome, string) {
	switch {
	case s.passes["A0"] < floor:
		return gate.Inconclusive, fmt.Sprintf("A0 passed %d tasks, under the floor of %d: no power to see A1 trail it", s.passes["A0"], floor)
	case s.lower >= -m:
		return gate.OK, "the P1 screen passes"
	case s.lower >= -m-band:
		return gate.Inconclusive, "parked: Francis decides (a miss, or the 60-task continuation)"
	}
	return gate.Fail, "a miss: fix the loop and run the screen once more on the next 20 tasks"
}

func (s *screen) print(w io.Writer) {
	fmt.Fprintf(w, "tasks %d, void runs %d (not counted)\n", s.tasks, s.void)
	for _, a := range []string{"A0", "A1"} {
		fmt.Fprintf(w, "%s: %d/%d passed, %d cap hits, capped time %.0f s\n", a, s.passes[a], s.tasks, s.capHits[a], s.capped[a])
	}
	if s.capped["A0"] > 0 {
		fmt.Fprintf(w, "capped-time ratio A1/A0: %.2f (reported, not decisive)\n", s.capped["A1"]/s.capped["A0"])
	}
	repos := map[string][2]int{}
	var names []string
	for _, t := range s.byTask {
		r := s.repo[t]
		if _, ok := repos[r]; !ok {
			names = append(names, r)
		}
		c := repos[r]
		c[0] += int(b2f(s.pass[t]["A0"]))
		c[1] += int(b2f(s.pass[t]["A1"]))
		repos[r] = c
	}
	slices.Sort(names)
	for _, r := range names {
		fmt.Fprintf(w, "  %s: A0 %d, A1 %d\n", r, repos[r][0], repos[r][1])
	}
	fmt.Fprintf(w, "d = A1 - A0: %+.1f points; one-sided 95%% lower bound (bootstrap over tasks, %d resamples, seed %d): %+.1f\n",
		s.d, resamples, seed, s.lower)
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func mean(xs []float64) float64 {
	var s float64
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}
