// Command calreport prints a calibration job's "reported, not decided on" figures from its results.jsonl, per arm:
// passes per task, stops by reason, window-full stops, cap hits, requests and wall time per run, the server's
// first-token times, done and verification refusals, the cell states, and §2's bar
// (e1-calibration-preregistration-2026-10-18.md §2, §12.2, §12.3). With -compare, each of another job's arms is set
// beside them task by task. It decides nothing: a record's verdict is read under its own registered rule.
//
//	go run ./tools/calreport [-compare OTHER/results.jsonl]... JOB/results.jsonl
//
// It ends with VERDICT: PASS when every line parsed and every counted run carries tack's summary, INCONCLUSIVE when
// there is no counted run or a run lacks its summary, and FAIL when a line does not parse.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/townsendmerino/tack/internal/eval/pilot"
	"github.com/townsendmerino/tack/tools/gate"
)

// §2's bar: a set is usable if an arm passes at least barPasses runs, on at least barTasks tasks.
const (
	barPasses = 4
	barTasks  = 2
)

type compareList []string

func (c *compareList) String() string     { return strings.Join(*c, ",") }
func (c *compareList) Set(s string) error { *c = append(*c, s); return nil }

func main() { os.Exit(run(os.Args[1:], os.Stdout)) }

func run(args []string, w io.Writer) int {
	fs := flag.NewFlagSet("calreport", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var compare compareList
	fs.Var(&compare, "compare", "another job's results.jsonl, set beside this one task by task; repeatable; a comma-separated list is one job split over files (C5's passes)")
	if err := fs.Parse(args); err != nil || fs.NArg() != 1 {
		fmt.Fprintln(w, gate.Verdict(gate.Inconclusive, "calreport: usage: [-compare OTHER/results.jsonl]... JOB/results.jsonl"))
		return 2
	}
	job, err := load(fs.Arg(0))
	if err != nil {
		fmt.Fprintln(w, gate.Verdict(gate.Fail, "calreport: "+err.Error()))
		return 1
	}
	var others []*jobData
	for _, c := range compare {
		o, err := loadMerged(strings.Split(c, ","))
		if err != nil {
			fmt.Fprintln(w, gate.Verdict(gate.Fail, "calreport: "+err.Error()))
			return 1
		}
		others = append(others, o)
	}
	report(w, job, others)
	switch {
	case job.counted() == 0:
		fmt.Fprintln(w, gate.Verdict(gate.Inconclusive, "calreport: no counted run in "+job.path))
		return 2
	case job.noSummary > 0:
		fmt.Fprintln(w, gate.Verdict(gate.Inconclusive, fmt.Sprintf("calreport: %d counted runs carry no tack summary", job.noSummary)))
		return 2
	}
	fmt.Fprintln(w, gate.Verdict(gate.OK, fmt.Sprintf("calreport: %d counted runs, %d void, over %d arms and %d tasks",
		job.counted(), len(job.voids), len(job.arms), len(job.tasks()))))
	return 0
}

// jobData is one results.jsonl, its void runs set aside and the rest grouped by arm.
type jobData struct {
	path      string
	arms      map[string][]pilot.Record
	armOrder  []string
	voids     []pilot.Record
	noSummary int
}

func (j *jobData) counted() int {
	n := 0
	for _, rs := range j.arms {
		n += len(rs)
	}
	return n
}

func (j *jobData) tasks() []int {
	seen := map[int]bool{}
	for _, rs := range j.arms {
		for _, r := range rs {
			seen[r.Task] = true
		}
	}
	var out []int
	for t := range seen {
		out = append(out, t)
	}
	sort.Ints(out)
	return out
}

func load(path string) (*jobData, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	j := &jobData{path: path, arms: map[string][]pilot.Record{}}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	line := 0
	for sc.Scan() {
		line++
		if strings.TrimSpace(sc.Text()) == "" {
			continue
		}
		var r pilot.Record
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, line, err)
		}
		if r.Run.Void != "" {
			j.voids = append(j.voids, r)
			continue
		}
		if r.TackRun == nil {
			j.noSummary++
		}
		if _, ok := j.arms[r.Run.Arm]; !ok {
			j.armOrder = append(j.armOrder, r.Run.Arm)
		}
		j.arms[r.Run.Arm] = append(j.arms[r.Run.Arm], r)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if line == 0 {
		return nil, errors.New(path + ": empty")
	}
	sort.Strings(j.armOrder)
	return j, nil
}

// loadMerged loads each path and merges them into one job, labeled by its paths.
func loadMerged(paths []string) (*jobData, error) {
	out := &jobData{path: strings.Join(paths, ","), arms: map[string][]pilot.Record{}}
	for _, p := range paths {
		j, err := load(p)
		if err != nil {
			return nil, err
		}
		for _, a := range j.armOrder {
			if _, ok := out.arms[a]; !ok {
				out.armOrder = append(out.armOrder, a)
			}
			out.arms[a] = append(out.arms[a], j.arms[a]...)
		}
		out.voids = append(out.voids, j.voids...)
		out.noSummary += j.noSummary
	}
	sort.Strings(out.armOrder)
	return out, nil
}

// windowFull says whether a run stopped on the window: a window_full stop, or the server refusing a prompt too long.
func windowFull(r pilot.Record) bool {
	if r.TackRun == nil {
		return false
	}
	if r.TackRun.StopReason == "window_full" {
		return true
	}
	for _, e := range r.TackRun.Errors {
		if strings.Contains(e, "prompt is too long") {
			return true
		}
	}
	return false
}

// stopKind is a run's stop reason, with a prompt-too-long error named as such.
func stopKind(r pilot.Record) string {
	if r.TackRun == nil {
		return "no summary"
	}
	s := r.TackRun.StopReason
	if s == "error" && windowFull(r) {
		return "prompt-too-long"
	}
	if s == "" {
		return "none"
	}
	return s
}

func report(w io.Writer, j *jobData, others []*jobData) {
	fmt.Fprintf(w, "# calreport: %s\n\n", j.path)
	builds(w, j)
	cells(w, j)
	perTask(w, j, others)
	stops(w, j)
	perRun(w, j)
	refusals(w, j)
	bar(w, j)
}

func builds(w io.Writer, j *jobData) {
	fmt.Fprintln(w, "## Builds and arms")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| arm | runs | tack | settings | server options | forced-call mode | sampling |")
	fmt.Fprintln(w, "|---|---|---|---|---|---|---|")
	for _, a := range j.armOrder {
		rs := j.arms[a]
		fmt.Fprintf(w, "| %s | %d | %s | %s | %s | %s | %s |\n", a, len(rs),
			distinct(rs, func(r pilot.Record) string { return short(r.Tack) }),
			distinct(rs, func(r pilot.Record) string { return orNone(strings.Join(r.Settings, " ")) }),
			distinct(rs, func(r pilot.Record) string { return orNone(strings.Join(r.ServerOptions, " ")) }),
			distinct(rs, func(r pilot.Record) string { return orNone(r.ForcedCallMode) }),
			distinct(rs, func(r pilot.Record) string { return orNone(samplingString(r.Sampling)) }))
	}
	fmt.Fprintln(w)
}

var cellRe = regexp.MustCompile(`load ([0-9.]+) of (\d+) cores, ([0-9.]+) GB available.*?Mac load ([0-9.]+)`)

func cells(w io.Writer, j *jobData) {
	fmt.Fprintln(w, "## Cells (§12.2)")
	fmt.Fprintln(w)
	var loads, avails, macs []float64
	power := map[string]int{}
	unparsed := 0
	for _, a := range j.armOrder {
		for _, r := range j.arms[a] {
			power[orNone(r.Power)]++
			m := cellRe.FindStringSubmatch(r.Cell)
			if m == nil {
				unparsed++
				continue
			}
			loads = append(loads, atof(m[1]))
			avails = append(avails, atof(m[3]))
			macs = append(macs, atof(m[4]))
		}
	}
	fmt.Fprintf(w, "- void runs: %d\n", len(j.voids))
	for _, v := range j.voids {
		fmt.Fprintf(w, "  - task %d %s rep %d: %s\n", v.Task, v.Run.Arm, v.Rep, v.Run.Void)
	}
	if len(loads) > 0 {
		fmt.Fprintf(w, "- server load at the cells' starts: %.2f–%.2f; memory available %.1f–%.1f GB; Mac 1-minute load %.2f–%.2f (recorded only)\n",
			minOf(loads), maxOf(loads), minOf(avails), maxOf(avails), minOf(macs), maxOf(macs))
	}
	fmt.Fprintf(w, "- power: %s\n", countsString(power))
	if unparsed > 0 {
		fmt.Fprintf(w, "- cell lines not recognized: %d\n", unparsed)
	}
	fmt.Fprintln(w)
}

// column is one arm of one job, for the task-by-task table.
type column struct {
	label string
	runs  []pilot.Record
}

func perTask(w io.Writer, j *jobData, others []*jobData) {
	var cols []column
	for _, a := range j.armOrder {
		cols = append(cols, column{label: a, runs: j.arms[a]})
	}
	for _, o := range others {
		for _, a := range o.armOrder {
			cols = append(cols, column{label: o.path + " " + a, runs: o.arms[a]})
		}
	}
	tasks := map[int]bool{}
	for _, c := range cols {
		for _, r := range c.runs {
			tasks[r.Task] = true
		}
	}
	var ts []int
	for t := range tasks {
		ts = append(ts, t)
	}
	sort.Ints(ts)

	fmt.Fprintln(w, "## Task by task: passes, window-full stops, cap hits")
	fmt.Fprintln(w)
	if len(others) > 0 {
		for i, c := range cols {
			fmt.Fprintf(w, "- column %d: %s\n", i+1, c.label)
		}
		fmt.Fprintln(w)
	}
	head := "| task |"
	rule := "|---|"
	for i, c := range cols {
		label := c.label
		if len(others) > 0 {
			label = strconv.Itoa(i + 1)
		}
		head += fmt.Sprintf(" %s passes | %s window-full | %s cap |", label, label, label)
		rule += "---|---|---|"
	}
	fmt.Fprintln(w, head)
	fmt.Fprintln(w, rule)
	for _, t := range append(ts, 0) {
		row := fmt.Sprintf("| %d |", t)
		if t == 0 {
			row = "| **all** |"
		}
		for _, c := range cols {
			n, pass, wf, capHit := 0, 0, 0, 0
			for _, r := range c.runs {
				if t != 0 && r.Task != t {
					continue
				}
				n++
				if r.Pass {
					pass++
				}
				if windowFull(r) {
					wf++
				}
				if r.Run.CapHit {
					capHit++
				}
			}
			if n == 0 {
				row += " — | — | — |"
				continue
			}
			row += fmt.Sprintf(" %d/%d | %d | %d |", pass, n, wf, capHit)
		}
		fmt.Fprintln(w, row)
	}
	fmt.Fprintln(w)
}

func stops(w io.Writer, j *jobData) {
	kinds := map[string]bool{}
	for _, a := range j.armOrder {
		for _, r := range j.arms[a] {
			kinds[stopKind(r)] = true
		}
	}
	var ks []string
	for k := range kinds {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	fmt.Fprintln(w, "## Stops by reason")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| arm | "+strings.Join(ks, " | ")+" |")
	fmt.Fprintln(w, "|---|"+strings.Repeat("---|", len(ks)))
	for _, a := range j.armOrder {
		c := map[string]int{}
		for _, r := range j.arms[a] {
			c[stopKind(r)]++
		}
		row := "| " + a + " |"
		for _, k := range ks {
			row += fmt.Sprintf(" %d |", c[k])
		}
		fmt.Fprintln(w, row)
	}
	fmt.Fprintln(w)
}

func perRun(w io.Writer, j *jobData) {
	fmt.Fprintln(w, "## Per run: requests, wall time, server first-token time")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| arm | requests: total, mean, median, range | wall s: total, median, max | first token ms: median, p90, max (requests) | prefilled tokens per request: median |")
	fmt.Fprintln(w, "|---|---|---|---|---|")
	for _, a := range j.armOrder {
		var reqs, walls, ttft, prefill []float64
		for _, r := range j.arms[a] {
			if r.TackRun != nil {
				reqs = append(reqs, float64(r.TackRun.Requests))
			}
			walls = append(walls, r.Run.WallSecs)
			for _, t := range r.Timed {
				if t.Prompt > 0 {
					ttft = append(ttft, float64(t.ServerTTFTms))
					prefill = append(prefill, float64(t.Prefilled))
				}
			}
		}
		fmt.Fprintf(w, "| %s | %.0f, %.1f, %.0f, %.0f–%.0f | %.0f, %.0f, %.0f | %s | %s |\n", a,
			sum(reqs), mean(reqs), quantile(reqs, 0.5), minOf(reqs), maxOf(reqs),
			sum(walls), quantile(walls, 0.5), maxOf(walls),
			ttftString(ttft), orDash(prefill, func(v []float64) string { return fmt.Sprintf("%.0f", quantile(v, 0.5)) }))
	}
	fmt.Fprintln(w)
}

func refusals(w io.Writer, j *jobData) {
	fmt.Fprintln(w, "## done refusals and how runs ended with done")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| arm | runs with a no-change refusal | runs with a verification refusal | done answers |")
	fmt.Fprintln(w, "|---|---|---|---|")
	for _, a := range j.armOrder {
		noChange, verify := 0, 0
		answers := map[string]int{}
		for _, r := range j.arms[a] {
			if r.TackRun == nil {
				continue
			}
			if r.TackRun.DoneRefused > 0 {
				noChange++
			}
			if r.TackRun.VerifyRefused > 0 {
				verify++
			}
			if r.TackRun.StopReason == "done" {
				answers[orNone(r.TackRun.Done)]++
			}
		}
		fmt.Fprintf(w, "| %s | %d | %d | %s |\n", a, noChange, verify, countsString(answers))
	}
	fmt.Fprintln(w)
}

func bar(w io.Writer, j *jobData) {
	fmt.Fprintf(w, "## §2's bar: at least %d passes on at least %d tasks\n\n", barPasses, barTasks)
	for _, a := range j.armOrder {
		passes := 0
		tasks := map[int]bool{}
		for _, r := range j.arms[a] {
			if r.Pass {
				passes++
				tasks[r.Task] = true
			}
		}
		state := "below the bar"
		if passes >= barPasses && len(tasks) >= barTasks {
			state = "at or above the bar"
		}
		fmt.Fprintf(w, "- %s: %d passes on %d tasks: %s\n", a, passes, len(tasks), state)
	}
	fmt.Fprintln(w)
}

func distinct(rs []pilot.Record, f func(pilot.Record) string) string {
	c := map[string]int{}
	for _, r := range rs {
		c[f(r)]++
	}
	if len(c) == 1 {
		for k := range c {
			return k
		}
	}
	return countsString(c)
}

func countsString(c map[string]int) string {
	if len(c) == 0 {
		return "—"
	}
	var ks []string
	for k := range c {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	parts := make([]string, len(ks))
	for i, k := range ks {
		parts[i] = fmt.Sprintf("%s ×%d", k, c[k])
	}
	return strings.Join(parts, ", ")
}

func samplingString(m map[string]any) string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	parts := make([]string, len(ks))
	for i, k := range ks {
		parts[i] = fmt.Sprintf("%s=%v", k, m[k])
	}
	return strings.Join(parts, " ")
}

func ttftString(v []float64) string {
	if len(v) == 0 {
		return "—"
	}
	return fmt.Sprintf("%.0f, %.0f, %.0f (%d)", quantile(v, 0.5), quantile(v, 0.9), maxOf(v), len(v))
}

func orDash(v []float64, f func([]float64) string) string {
	if len(v) == 0 {
		return "—"
	}
	return f(v)
}

func short(rev string) string {
	if len(rev) > 7 {
		return rev[:7]
	}
	return orNone(rev)
}

func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "none"
	}
	return s
}

func atof(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

func sum(v []float64) float64 {
	s := 0.0
	for _, x := range v {
		s += x
	}
	return s
}

func mean(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	return sum(v) / float64(len(v))
}

func minOf(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	m := v[0]
	for _, x := range v[1:] {
		m = min(m, x)
	}
	return m
}

func maxOf(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	m := v[0]
	for _, x := range v[1:] {
		m = max(m, x)
	}
	return m
}

// quantile is the nearest-rank q-quantile of v; the median of an even count is the mean of the two middle values.
func quantile(v []float64, q float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	if q == 0.5 {
		n := len(s)
		if n%2 == 1 {
			return s[n/2]
		}
		return (s[n/2-1] + s[n/2]) / 2
	}
	i := int(q*float64(len(s))+0.999999) - 1
	return s[min(max(i, 0), len(s)-1)]
}
