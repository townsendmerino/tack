package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/townsendmerino/tack/internal/eval/pilot"
)

// screenFile writes a screen's results: n tasks, task i passing on A0 when a0(i) and on A1 when a1(i), each run 100 s
// when it passes and the 360 s cap when it fails; extra records are appended as given.
func screenFile(t *testing.T, n int, a0, a1 func(int) bool, extra ...pilot.Record) string {
	t.Helper()
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	for i := 1; i <= n; i++ {
		for arm, pass := range map[string]bool{"A0": a0(i), "A1": a1(i)} {
			r := pilot.Record{Task: i, Repo: []string{"ken", "goinfer"}[i%2], Pass: pass, CappedSecs: 360, Run: pilot.Result{Arm: arm, CapHit: !pass}}
			if pass {
				r.CappedSecs = 100
			}
			enc.Encode(r)
		}
	}
	for _, r := range extra {
		enc.Encode(r)
	}
	p := filepath.Join(t.TempDir(), "results.jsonl")
	os.WriteFile(p, b.Bytes(), 0o644)
	return p
}

func readScreen(t *testing.T, args ...string) (int, string) {
	t.Helper()
	var out bytes.Buffer
	code := run(args, &out)
	return code, out.String()
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}

func upTo(k int) func(int) bool { return func(i int) bool { return i <= k } }

// TestTheRegisteredRule: the verdict follows the lower bound against -m and the parked band, and the floor holds first.
func TestTheRegisteredRule(t *testing.T) {
	for name, c := range map[string]struct {
		a0, a1 func(int) bool
		want   string
	}{
		"equal arms":            {upTo(8), upTo(8), "VERDICT: PASS"},                            // d 0, bound 0
		"A1 one task short":     {upTo(8), upTo(7), "VERDICT: PASS"},                            // d -5
		"A1 four tasks short":   {upTo(8), upTo(4), "VERDICT: INCONCLUSIVE — p1screen: parked"}, // d -20, bound in [-37, -29)
		"A1 never passes":       {upTo(10), upTo(0), "VERDICT: FAIL — p1screen: a miss"},        // d -50
		"under the floor":       {upTo(2), upTo(2), "VERDICT: INCONCLUSIVE — p1screen: A0 passed 2 tasks, under the floor"},
		"A1 ahead, under floor": {upTo(2), upTo(9), "VERDICT: INCONCLUSIVE — p1screen: A0 passed 2 tasks"},
	} {
		code, out := readScreen(t, screenFile(t, 20, c.a0, c.a1))
		if !strings.HasPrefix(lastLine(out), c.want) || (code == 0) != strings.HasPrefix(c.want, "VERDICT: PASS") {
			t.Errorf("%s: exit %d, %s", name, code, out)
		}
	}
}

// TestTheBootstrap: the lower bound of a constant difference is that constant, it is fixed by the seed, and it sits
// below the mean by about the one-sided 95% normal width for a spread difference.
func TestTheBootstrap(t *testing.T) {
	if lb := lowerBound([]float64{-100, -100, -100}); lb != -100 {
		t.Errorf("constant: %v", lb)
	}
	d := make([]float64, 20)
	for i := range 4 {
		d[i] = -100 // d = -20, sd 40 (population), se about 8.9
	}
	a, b := lowerBound(d), lowerBound(d)
	if a != b {
		t.Fatalf("not fixed by the seed: %v, %v", a, b)
	}
	if a > -30 || a < -40 {
		t.Errorf("lower bound %v; want about -20 - 1.645 * 8.9, near -35", a)
	}
}

// TestAScreenNotWholeIsNotRead: a task without one counted run of each arm, or the wrong task count, reads
// INCONCLUSIVE; a void run is in no count, so a task with a void run and its re-run is whole.
func TestAScreenNotWholeIsNotRead(t *testing.T) {
	void := pilot.Record{Task: 3, Run: pilot.Result{Arm: "A1", Void: "cell: load"}}
	oracle := pilot.Record{Task: 4, Run: pilot.Result{Arm: "A0"}, OracleErr: "go: not found"}
	if code, out := readScreen(t, screenFile(t, 20, upTo(8), upTo(8), void, oracle)); code != 0 || !strings.Contains(out, "void runs 2") {
		t.Errorf("void runs beside their re-runs: exit %d, %s", code, out)
	}
	extra := pilot.Record{Task: 5, Run: pilot.Result{Arm: "A1"}, Pass: true}
	if _, out := readScreen(t, screenFile(t, 20, upTo(8), upTo(8), extra)); !strings.HasPrefix(lastLine(out), "VERDICT: INCONCLUSIVE") ||
		!strings.Contains(out, "task 5 has 1 counted A0 runs and 2 A1") {
		t.Errorf("a task with two A1 runs: %s", out)
	}
	if _, out := readScreen(t, screenFile(t, 19, upTo(8), upTo(8))); !strings.HasPrefix(lastLine(out), "VERDICT: INCONCLUSIVE") ||
		!strings.Contains(out, "19 tasks") {
		t.Errorf("19 of 20 tasks: %s", out)
	}
	stray := pilot.Record{Task: 21, Run: pilot.Result{Arm: "A0k"}}
	if _, out := readScreen(t, screenFile(t, 20, upTo(8), upTo(8), stray)); !strings.HasPrefix(lastLine(out), "VERDICT: INCONCLUSIVE") {
		t.Errorf("an arm that is not the screen's: %s", out)
	}
}

// TestWhatIsReported: each arm's passes, cap hits and capped time, their ratio, and the passes per repo.
func TestWhatIsReported(t *testing.T) {
	_, out := readScreen(t, screenFile(t, 20, upTo(10), upTo(5)))
	for _, want := range []string{"A0: 10/20 passed, 10 cap hits, capped time 4600 s", "A1: 5/20 passed, 15 cap hits, capped time 5900 s",
		"capped-time ratio A1/A0: 1.28", "  goinfer: A0 5, A1 3", "  ken: A0 5, A1 2", "d = A1 - A0: -25.0 points"} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in\n%s", want, out)
		}
	}
}
