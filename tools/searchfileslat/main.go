// Command searchfileslat is P1 step 5's latency run of ken's route to instruction sections: what
// kensearch.Searcher.SectionPaths, the call core makes before a user message's first request, adds to a turn on a large
// repository. Its spec and pre-registered rule are docs/measurements/searchfiles-exactness-goinfer-2026-11-08.md, "Run
// spec: SearchFiles latency on goinfer".
//
//	go run ./tools/searchfileslat -root DIR -model-dir DIR [-modes bm25,hybrid] [-rounds 20]
//
// It reads DIR and writes nothing to it: ken opens without a snapshot or a watcher, and the index lives in memory. The
// untimed step builds one index per mode (one mode alone is built twice), each only at a load average of maxLoad or
// less, and stops unless every build has the same chunk count. One untimed warm-up pass per mode follows. Then each
// round times every call (each message of messages in each file) once per mode, the modes in A B B A order. A cell, one
// mode's pass in one round, records the load average and free memory at its start, and is void when the load is over
// maxLoad.
//
// The rule reads the hybrid mode: the worst call's p95 over its valid rounds (nearest rank). It ends with VERDICT: PASS
// at or under inlineBar (ken's route runs inline), INCONCLUSIVE when parked (up to offBar; the owner decides), FAIL over
// offBar (off the critical path or not at all), and INCONCLUSIVE when the run cannot read: a box too busy, too few
// valid cells, an error, or no hybrid mode.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/townsendmerino/aikit/chunk"
	"github.com/townsendmerino/aikit/chunk/treesitter"

	"github.com/townsendmerino/tack/internal/kensearch"
	"github.com/townsendmerino/tack/tools/gate"
)

const (
	// maxLoad is the 1-minute load average over which a build waits and a cell is void (the spec's "Records").
	maxLoad = 2.0
	// sectionK is the k core passes SectionPaths: core's sectionHits (internal/core/instructions.go).
	sectionK = 5
	// inlineBar and offBar are the pre-registered rule's bars (approved 2026-11-08).
	inlineBar = 100 * time.Millisecond
	offBar    = 400 * time.Millisecond
	// decides is the mode the rule reads.
	decides = "hybrid"
	// parseTimeoutMicros is 0, none, as in searchfilescheck: the index must not depend on the box's load.
	parseTimeoutMicros = 0
)

// files are goinfer's instruction files, from its root.
var files = []string{"CLAUDE.md", "internal/runtime/scheduler/CLAUDE.md", "internal/runtime/mem/CLAUDE.md"}

// messages are searchfilescheck's task-like messages (TestMessagesAreTheExactnessChecks holds the two lists together);
// its heading queries are left out, since no user sends them.
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

func init() {
	chunk.Register(kensearch.Chunker, treesitter.New(treesitter.WithParseTimeoutMicros(parseTimeoutMicros)))
}

// index is the part of kensearch.Searcher the run uses.
type index interface {
	SectionPaths(ctx context.Context, query string, k int, file string) ([][]string, error)
	Chunks() int
	Close() error
}

// The run's seams: a test stands in for ken, the clock and the host.
var (
	openIndex = func(ctx context.Context, root, mode, modelDir string, logw io.Writer) (index, error) {
		s, err := kensearch.Open(ctx, root, kensearch.Options{Mode: mode, ModelDir: modelDir, Log: logw})
		if err != nil {
			return nil, err
		}
		return s, nil
	}
	now      = time.Now
	readHost = hostState
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

type call struct{ file, message string }

func run(args []string, w, logw io.Writer) int {
	fl := flag.NewFlagSet("searchfileslat", flag.ContinueOnError)
	fl.SetOutput(io.Discard)
	root := fl.String("root", "", "the repository to time over (a goinfer tree exported at a fixed commit)")
	modelDir := fl.String("model-dir", "", "the Model2Vec directory, from ~/models, for every mode but bm25")
	modeList := fl.String("modes", "bm25,hybrid", "ken's modes, interleaved in this order")
	rounds := fl.Int("rounds", 20, "timed rounds per call and mode")
	fileList := fl.String("files", strings.Join(files, ","), "the instruction files, from the root")
	inconclusive := func(why string) int {
		fmt.Fprintln(w, gate.Verdict(gate.Inconclusive, "searchfileslat: "+why))
		return 2
	}
	if err := fl.Parse(args); err != nil || *root == "" || *rounds < 1 {
		return inconclusive("usage: -root DIR -model-dir DIR [-modes bm25,hybrid] [-rounds 20] [-files A,B]")
	}
	modes := strings.Split(*modeList, ",")
	var calls []call
	for f := range strings.SplitSeq(*fileList, ",") {
		f = filepath.ToSlash(strings.TrimSpace(f))
		if fi, err := os.Stat(filepath.Join(*root, filepath.FromSlash(f))); err != nil || fi.IsDir() {
			return inconclusive(fmt.Sprintf("%s is not a file in the tree: is -root an export of goinfer?", f))
		}
		for _, m := range messages {
			calls = append(calls, call{f, m})
		}
	}
	ctx := context.Background()

	// The untimed step. A build starts only on a quiet box; on a busy one the run stops rather than wait.
	builds := slices.Clone(modes)
	if len(builds) == 1 {
		builds = append(builds, builds[0])
	}
	idx := map[string]index{}
	defer func() {
		for _, x := range idx {
			x.Close()
		}
	}()
	counts := map[int]bool{}
	for i, mode := range builds {
		load, free, err := readHost()
		if err != nil {
			return inconclusive("cannot read the host's load: " + err.Error())
		}
		if load > maxLoad {
			return inconclusive(fmt.Sprintf("build %d (%s): load %.2f over %.0f; nothing timed", i+1, mode, load, maxLoad))
		}
		start := now()
		x, err := openIndex(ctx, *root, mode, *modelDir, logw)
		if err != nil {
			return inconclusive(fmt.Sprintf("build %d (%s): %v", i+1, mode, err))
		}
		fmt.Fprintf(w, "build %d: %s, %d chunks in %v (untimed step; load %.2f, %.1f GB free at its start)\n", i+1, mode,
			x.Chunks(), now().Sub(start).Round(time.Millisecond), load, free)
		counts[x.Chunks()] = true
		if _, dup := idx[mode]; dup {
			x.Close()
		} else {
			idx[mode] = x
		}
	}
	if len(counts) != 1 {
		return inconclusive("the builds' chunk counts differ; nothing timed")
	}

	for _, mode := range modes { // the warm-up pass, untimed
		for _, c := range calls {
			if _, err := idx[mode].SectionPaths(ctx, c.message, sectionK, c.file); err != nil {
				return inconclusive(fmt.Sprintf("warm-up %s %s %q: %v", mode, c.file, c.message, err))
			}
		}
	}

	times := map[string][][]time.Duration{} // per mode, per call, the valid rounds' durations
	valid := map[string]int{}
	for _, mode := range modes {
		times[mode] = make([][]time.Duration, len(calls))
	}
	for r := range *rounds {
		order := slices.Clone(modes)
		if r%2 == 1 {
			slices.Reverse(order)
		}
		for _, mode := range order {
			load, free, err := readHost()
			void := ""
			switch {
			case err != nil:
				void = "; void: " + err.Error()
			case load > maxLoad:
				void = fmt.Sprintf("; void: load over %.0f", maxLoad)
			}
			fmt.Fprintf(w, "cell round %d %s: load %.2f, %.1f GB free%s\n", r+1, mode, load, free, void)
			cell := make([]time.Duration, len(calls))
			for i, c := range calls {
				start := now()
				_, err := idx[mode].SectionPaths(ctx, c.message, sectionK, c.file)
				cell[i] = now().Sub(start)
				if err != nil {
					return inconclusive(fmt.Sprintf("round %d %s %s %q: %v", r+1, mode, c.file, c.message, err))
				}
			}
			if void == "" {
				valid[mode]++
				for i, d := range cell {
					times[mode][i] = append(times[mode][i], d)
				}
			}
		}
	}

	worst := map[string]time.Duration{}
	for _, mode := range modes {
		fmt.Fprintf(w, "%s: %d of %d cells valid\n", mode, valid[mode], *rounds)
		if valid[mode] == 0 {
			continue
		}
		var all []time.Duration
		for i, c := range calls {
			ds := slices.Sorted(slices.Values(times[mode][i]))
			p95 := nearest(ds, 0.95)
			worst[mode] = max(worst[mode], p95)
			all = append(all, ds...)
			fmt.Fprintf(w, "  %s %q: p50 %v, p95 %v, max %v\n", c.file, c.message, ms(nearest(ds, 0.5)), ms(p95), ms(ds[len(ds)-1]))
		}
		slices.Sort(all)
		fmt.Fprintf(w, "  %s: worst call's p95 %v; over all calls p50 %v, p95 %v, max %v\n", mode, ms(worst[mode]),
			ms(nearest(all, 0.5)), ms(nearest(all, 0.95)), ms(all[len(all)-1]))
	}

	if !slices.Contains(modes, decides) {
		return inconclusive("the rule reads " + decides + ", which was not run")
	}
	for _, mode := range modes {
		if 2*valid[mode] < *rounds {
			return inconclusive(fmt.Sprintf("%s: %d of %d cells valid, under half; the box was too busy", mode, valid[mode], *rounds))
		}
	}
	p95 := worst[decides]
	switch band(p95) {
	case "inline":
		fmt.Fprintln(w, gate.Verdict(gate.OK, fmt.Sprintf("searchfileslat: %s worst call's p95 %v ≤ %v: inline", decides, ms(p95), inlineBar)))
		return 0
	case "parked":
		return inconclusive(fmt.Sprintf("%s worst call's p95 %v in %v–%v: parked, the owner decides", decides, ms(p95), inlineBar, offBar))
	}
	fmt.Fprintln(w, gate.Verdict(gate.Fail, fmt.Sprintf("searchfileslat: %s worst call's p95 %v > %v: off the critical path", decides, ms(p95), offBar)))
	return 1
}

// band is the pre-registered rule's reading of a p95.
func band(p95 time.Duration) string {
	switch {
	case p95 <= inlineBar:
		return "inline"
	case p95 <= offBar:
		return "parked"
	}
	return "off"
}

// nearest is the p-quantile of sorted by nearest rank; sorted must not be empty.
func nearest(sorted []time.Duration, p float64) time.Duration {
	i := int(math.Ceil(float64(len(sorted))*p)) - 1
	return sorted[max(0, min(i, len(sorted)-1))]
}

// ms rounds d for the log.
func ms(d time.Duration) time.Duration { return d.Round(10 * time.Microsecond) }
