package main

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeKen stands in for ken, the clock and the host. Each SectionPaths call advances the clock by delay(mode, call);
// host gives each readHost its load, in order.
type fakeKen struct {
	clock  time.Time
	delay  func(mode string, c call) time.Duration
	chunks func(build int) int
	host   func(n int) float64
	reads  int
	builds []string
	calls  int // SectionPaths calls in all, warm-up included
	err    error
}

type fakeIndex struct {
	k      *fakeKen
	mode   string
	chunks int
}

func (x *fakeIndex) SectionPaths(_ context.Context, q string, k int, file string) ([][]string, error) {
	if k != sectionK {
		return nil, fmt.Errorf("k %d, want %d", k, sectionK)
	}
	x.k.calls++
	x.k.clock = x.k.clock.Add(x.k.delay(x.mode, call{file, q}))
	return [][]string{{"notes"}}, x.k.err
}
func (x *fakeIndex) Chunks() int  { return x.chunks }
func (x *fakeIndex) Close() error { return nil }

// install puts k in place of ken, the clock and the host for one test.
func (k *fakeKen) install(t *testing.T) {
	o, n, h := openIndex, now, readHost
	t.Cleanup(func() { openIndex, now, readHost = o, n, h })
	if k.chunks == nil {
		k.chunks = func(int) int { return 79919 }
	}
	if k.host == nil {
		k.host = func(int) float64 { return 0.5 }
	}
	openIndex = func(_ context.Context, _, mode, _ string, _ io.Writer) (index, error) {
		k.builds = append(k.builds, mode)
		return &fakeIndex{k: k, mode: mode, chunks: k.chunks(len(k.builds))}, nil
	}
	now = func() time.Time { return k.clock }
	readHost = func() (float64, float64, error) {
		k.reads++
		return k.host(k.reads), 9.5, nil
	}
}

// goinferTree is a tree holding the three instruction files.
func goinferTree(t *testing.T) string {
	dir := t.TempDir()
	for _, f := range files {
		p := filepath.Join(dir, filepath.FromSlash(f))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("# notes\n\n## Testing\n\nRun the tests for the package.\n\n## Releases\n\nTag a release.\n"), 0o644)
	}
	return dir
}

func runFake(t *testing.T, k *fakeKen, args ...string) (int, string) {
	k.install(t)
	var out bytes.Buffer
	code := run(append([]string{"-root", goinferTree(t)}, args...), &out, io.Discard)
	return code, out.String()
}

func flat(d time.Duration) func(string, call) time.Duration {
	return func(string, call) time.Duration { return d }
}

func TestBandsAreTheRegisteredRule(t *testing.T) {
	for _, c := range []struct {
		p95  time.Duration
		want string
	}{
		{0, "inline"}, {100 * time.Millisecond, "inline"}, {100*time.Millisecond + 1, "parked"},
		{400 * time.Millisecond, "parked"}, {400*time.Millisecond + 1, "off"}, {time.Second, "off"},
	} {
		if got := band(c.p95); got != c.want {
			t.Errorf("band(%v) = %s, want %s", c.p95, got, c.want)
		}
	}
}

func TestNearestRank(t *testing.T) {
	var ds []time.Duration
	for i := 1; i <= 20; i++ {
		ds = append(ds, time.Duration(i))
	}
	if nearest(ds, 0.95) != 19 || nearest(ds, 0.5) != 10 || nearest(ds[:1], 0.95) != 1 {
		t.Errorf("p95 %v, p50 %v, p95 of one %v; want 19, 10, 1", nearest(ds, 0.95), nearest(ds, 0.5), nearest(ds[:1], 0.95))
	}
}

func TestEachBandsVerdict(t *testing.T) {
	for _, c := range []struct {
		d    time.Duration
		code int
		want string
	}{
		{50 * time.Millisecond, 0, "VERDICT: PASS"},
		{250 * time.Millisecond, 2, "parked"},
		{500 * time.Millisecond, 1, "VERDICT: FAIL"},
	} {
		code, out := runFake(t, &fakeKen{delay: flat(c.d)}, "-rounds", "4")
		if code != c.code || !strings.Contains(out, c.want) {
			t.Errorf("%v: exit %d, want %d with %q:\n%s", c.d, code, c.code, c.want, out)
		}
	}
}

// TestTheRuleReadsHybridOnly: a slow bm25 does not decide.
func TestTheRuleReadsHybridOnly(t *testing.T) {
	d := func(mode string, _ call) time.Duration {
		if mode == "hybrid" {
			return 20 * time.Millisecond
		}
		return time.Second
	}
	if code, out := runFake(t, &fakeKen{delay: d}, "-rounds", "4"); code != 0 {
		t.Errorf("exit %d:\n%s", code, out)
	}
	if code, out := runFake(t, &fakeKen{delay: flat(time.Millisecond)}, "-modes", "bm25", "-rounds", "2"); code != 2 ||
		!strings.Contains(out, "reads hybrid") {
		t.Errorf("bm25 alone: exit %d:\n%s", code, out)
	}
}

// TestTheWorstCallDecides: one message slow in one file parks the route, though the p95 over all calls is fast.
func TestTheWorstCallDecides(t *testing.T) {
	d := func(_ string, c call) time.Duration {
		if c.file == files[1] && c.message == messages[3] {
			return 150 * time.Millisecond
		}
		return 10 * time.Millisecond
	}
	code, out := runFake(t, &fakeKen{delay: d}, "-rounds", "20")
	if code != 2 || !strings.Contains(out, "parked") || !strings.Contains(out, "over all calls p50 10ms, p95 10ms") {
		t.Errorf("exit %d:\n%s", code, out)
	}
}

// TestCellsInterleaveABBA: the modes alternate A B B A, and each cell reads the host before it starts.
func TestCellsInterleaveABBA(t *testing.T) {
	k := &fakeKen{delay: flat(time.Millisecond)}
	code, out := runFake(t, k, "-rounds", "4")
	var order []string
	for line := range strings.SplitSeq(out, "\n") {
		if rest, ok := strings.CutPrefix(line, "cell round "); ok {
			f := strings.Fields(rest)
			order = append(order, strings.TrimSuffix(f[1], ":"))
		}
	}
	want := []string{"bm25", "hybrid", "hybrid", "bm25", "bm25", "hybrid", "hybrid", "bm25"}
	if code != 0 || !slices.Equal(order, want) {
		t.Errorf("exit %d, cells %v, want %v:\n%s", code, order, want, out)
	}
	// Two builds, then one read per cell.
	if k.reads != 2+len(want) {
		t.Errorf("%d host reads, want %d", k.reads, 2+len(want))
	}
}

// TestAHeavyCellIsVoid: a cell started over the load bar is left out, not averaged: its slow calls do not reach the
// rule.
func TestAHeavyCellIsVoid(t *testing.T) {
	k := &fakeKen{}
	// Reads 1 and 2 are the builds; read 4 is round 1's hybrid cell.
	k.host = func(n int) float64 {
		if n == 4 {
			return 3
		}
		return 0.5
	}
	cell := 0
	k.delay = func(mode string, _ call) time.Duration {
		if k.reads == 4 && mode == "hybrid" {
			cell++
			return time.Second
		}
		return 20 * time.Millisecond
	}
	code, out := runFake(t, k, "-rounds", "4")
	if code != 0 || cell == 0 || !strings.Contains(out, "void: load over 2") || !strings.Contains(out, "hybrid: 3 of 4 cells valid") {
		t.Errorf("exit %d, %d slow calls:\n%s", code, cell, out)
	}
}

func TestTooManyVoidCellsIsInconclusive(t *testing.T) {
	k := &fakeKen{delay: flat(time.Millisecond)}
	k.host = func(n int) float64 {
		if n > 3 { // every cell after the first
			return 3
		}
		return 0.5
	}
	if code, out := runFake(t, k, "-rounds", "4"); code != 2 || !strings.Contains(out, "under half") {
		t.Errorf("exit %d:\n%s", code, out)
	}
}

// TestTheBuildWaitsForAQuietBox: a build is never started over the load bar, and nothing is timed.
func TestTheBuildWaitsForAQuietBox(t *testing.T) {
	k := &fakeKen{delay: flat(time.Millisecond), host: func(int) float64 { return 2.5 }}
	if code, out := runFake(t, k, "-rounds", "2"); code != 2 || len(k.builds) != 0 || k.calls != 0 {
		t.Errorf("exit %d, builds %v, %d calls:\n%s", code, k.builds, k.calls, out)
	}
}

// TestDifferingBuildsStop: builds that disagree on the chunk count stop the run before anything is timed, and one mode
// alone is built twice so its count is still checked.
func TestDifferingBuildsStop(t *testing.T) {
	k := &fakeKen{delay: flat(time.Millisecond), chunks: func(b int) int { return 79919 + b }}
	if code, out := runFake(t, k, "-modes", "hybrid", "-rounds", "2"); code != 2 || !slices.Equal(k.builds, []string{"hybrid", "hybrid"}) ||
		k.calls != 0 || !strings.Contains(out, "chunk counts differ") {
		t.Errorf("exit %d, builds %v, %d calls:\n%s", code, k.builds, k.calls, out)
	}
}

// TestAWarmUpPassIsNotTimed: each mode's first pass is left out of the rule.
func TestAWarmUpPassIsNotTimed(t *testing.T) {
	k := &fakeKen{}
	n := map[string]int{}
	k.delay = func(mode string, _ call) time.Duration {
		n[mode]++
		if n[mode] <= len(files)*len(messages) {
			return time.Second
		}
		return time.Millisecond
	}
	if code, out := runFake(t, k, "-rounds", "2"); code != 0 {
		t.Errorf("exit %d:\n%s", code, out)
	}
}

func TestAMissingFileIsInconclusive(t *testing.T) {
	k := &fakeKen{delay: flat(time.Millisecond)}
	k.install(t)
	var out bytes.Buffer
	dir := goinferTree(t)
	os.Remove(filepath.Join(dir, files[2]))
	if code := run([]string{"-root", dir}, &out, io.Discard); code != 2 || len(k.builds) != 0 {
		t.Errorf("exit %d, builds %v:\n%s", code, k.builds, out.String())
	}
}

func TestAnErrorIsInconclusive(t *testing.T) {
	k := &fakeKen{delay: flat(time.Millisecond), err: fmt.Errorf("not indexed")}
	if code, out := runFake(t, k, "-rounds", "2"); code != 2 || !strings.Contains(out, "not indexed") {
		t.Errorf("exit %d:\n%s", code, out)
	}
}

// TestMessagesAreTheExactnessChecks: the run times the messages whose answers searchfilescheck proved exact.
func TestMessagesAreTheExactnessChecks(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), filepath.Join("..", "searchfilescheck", "main.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var theirs []string
	ast.Inspect(f, func(n ast.Node) bool {
		if v, ok := n.(*ast.ValueSpec); ok && len(v.Names) == 1 && v.Names[0].Name == "messages" {
			for _, e := range v.Values[0].(*ast.CompositeLit).Elts {
				s, _ := strconv.Unquote(e.(*ast.BasicLit).Value)
				theirs = append(theirs, s)
			}
		}
		return true
	})
	if len(theirs) == 0 || !slices.Equal(messages, theirs) {
		t.Errorf("messages %q, searchfilescheck's %q", messages, theirs)
	}
}

func TestMeasurementChunkerHasNoTimeout(t *testing.T) {
	if parseTimeoutMicros != 0 {
		t.Errorf("parse timeout %d µs, want 0", parseTimeoutMicros)
	}
}

// TestTheRealRouteRuns: the run over ken itself, bm25 on a small tree, times every call; the rule then has no hybrid
// mode to read.
func TestTheRealRouteRuns(t *testing.T) {
	h := readHost
	readHost = func() (float64, float64, error) { return 0.5, 9.5, nil }
	t.Cleanup(func() { readHost = h })
	var out bytes.Buffer
	code := run([]string{"-root", goinferTree(t), "-modes", "bm25", "-rounds", "2"}, &out, io.Discard)
	if code != 2 || !strings.Contains(out.String(), "bm25: 2 of 2 cells valid") || !strings.Contains(out.String(), "reads hybrid") ||
		strings.Count(out.String(), ": p50 ") != len(files)*len(messages) {
		t.Errorf("exit %d:\n%s", code, out.String())
	}
}

// TestTheHostIsRead: the box's own load and memory read, where the run goes.
func TestTheHostIsRead(t *testing.T) {
	load, free, err := hostState()
	if err != nil || load < 0 || free <= 0 {
		t.Errorf("load %v, free %v GB, %v", load, free, err)
	}
}
