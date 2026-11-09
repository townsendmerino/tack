package main

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/townsendmerino/tack/internal/eval/pilot"
)

func writeResults(t *testing.T, recs []pilot.Record, extra ...string) string {
	t.Helper()
	var b strings.Builder
	for _, r := range recs {
		line, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	for _, e := range extra {
		b.WriteString(e + "\n")
	}
	p := filepath.Join(t.TempDir(), "results.jsonl")
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const cell = "nobara load 0.50 of 16 cores, 59.5 GB available with the model loaded; Mac load 3.00 2.00 1.00, power ac"

func rec(task int, arm string, pass bool, stop string, errs ...string) pilot.Record {
	return pilot.Record{
		Tack: "3382aa83ac9e", Task: task, Run: pilot.Result{Arm: arm, WallSecs: 60}, Pass: pass, Power: "ac", Cell: cell,
		TackRun: &pilot.TackSummary{Requests: 10, StopReason: stop, Errors: errs},
		Timed:   []pilot.Timed{{Prompt: 9000, Prefilled: 500, ServerTTFTms: 2000}, {Prompt: 0, ServerTTFTms: 0}},
	}
}

// The report counts a prompt-too-long refusal as a window-full stop and names it as its own stop kind, counts cap hits,
// leaves void runs out of every count but lists them, and reads §2's bar per arm.
func TestReport_CountsAndBar(t *testing.T) {
	capped := rec(2, "A1", false, "cancelled")
	capped.Run.CapHit = true
	void := rec(3, "A1", false, "done")
	void.Run.Void = "this Mac is on battery"
	verify := rec(3, "A1", false, "done")
	verify.TackRun.VerifyRefused, verify.TackRun.DoneRefused = 1, 1
	p := writeResults(t, []pilot.Record{
		rec(1, "A1", true, "done"),
		rec(1, "A1", true, "done"),
		rec(2, "A1", true, "done"),
		rec(2, "A1", true, "done"),
		rec(1, "A1", false, "error", "server: 400 invalid_request_error: prompt is too long: 21111 tokens > 16384 maximum"),
		rec(1, "A1", false, "window_full"),
		capped, void, verify,
	})
	var out strings.Builder
	if code := run([]string{p}, &out); code != 0 {
		t.Fatalf("exit %d, want 0:\n%s", code, out.String())
	}
	s := out.String()
	for _, want := range []string{
		"| 1 | 2/4 | 2 | 0 |",                                  // task 1: 2 passes of 4, 2 window-full stops (one a prompt-too-long refusal), no cap
		"| 2 | 2/3 | 0 | 1 |",                                  // task 2: the capped run
		"| **all** | 4/8 | 2 | 1 |",                            // the void run is in no count
		"| cancelled | done | prompt-too-long | window_full |", // the error stop is named for its refusal
		"| A1 | 1 | 5 | 1 | 1 |",
		"- void runs: 1",
		"task 3 A1 rep 0: this Mac is on battery",
		"| A1 | 1 | 1 | none ×5 |", // one run refused both ways; the synthetic runs carry no done answer
		"- A1: 4 passes on 2 tasks: at or above the bar",
		"2000, 2000, 2000 (8)", // one timed request per counted run; the empty one is left out
		"VERDICT: PASS — calreport: 8 counted runs, 1 void",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("output lacks %q:\n%s", want, s)
		}
	}
}

// A line that does not parse fails the report; a job with no counted run is inconclusive.
func TestReport_Verdicts(t *testing.T) {
	var out strings.Builder
	if code := run([]string{writeResults(t, []pilot.Record{rec(1, "A1", true, "done")}, "{not json")}, &out); code != 1 ||
		!strings.Contains(out.String(), "VERDICT: FAIL") {
		t.Fatalf("malformed line: exit %d\n%s", code, out.String())
	}
	void := rec(1, "A1", false, "done")
	void.Run.Void = "heavy load"
	out.Reset()
	if code := run([]string{writeResults(t, []pilot.Record{void})}, &out); code != 2 ||
		!strings.Contains(out.String(), "VERDICT: INCONCLUSIVE — calreport: no counted run") {
		t.Fatalf("all void: exit %d\n%s", code, out.String())
	}
}

// -compare sets another job's arms beside this one's, task by task, and a comma-separated list is one job.
func TestReport_Compare(t *testing.T) {
	this := writeResults(t, []pilot.Record{rec(1, "A1", true, "done"), rec(2, "A1", false, "done")})
	a := writeResults(t, []pilot.Record{rec(1, "B", false, "done")})
	b := writeResults(t, []pilot.Record{rec(2, "B", true, "done")})
	var out strings.Builder
	if code := run([]string{"-compare", a + "," + b, this}, &out); code != 0 {
		t.Fatalf("exit %d\n%s", code, out.String())
	}
	s := out.String()
	for _, want := range []string{
		"- column 2: " + a + "," + b + " B",
		"| 1 | 1/1 | 0 | 0 | 0/1 | 0 | 0 |",
		"| 2 | 0/1 | 0 | 0 | 1/1 | 0 | 0 |",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("output lacks %q:\n%s", want, s)
		}
	}
}

// guardEvents is an events.jsonl in which result_notes fires at request 1 (an empty grep) and is followed by an edit
// applied at request 3, fires again at request 4 with nothing applied by request 7, and the turn ends on repeat_loop.
const guardEvents = `{"type":"request.sent"}
{"type":"tool.call","payload":{"id":"c1","name":"grep","input":{"pattern":"x"}}}
{"type":"tool.finished","payload":{"id":"c1","outcome":"ok","result":"\n\n[tack: nothing matched. Search for a shorter part of the name, drop regex escapes, or widen the path.]"}}
{"type":"request.sent"}
{"type":"request.sent"}
{"type":"tool.call","payload":{"id":"c2","name":"edit","input":{"path":"a.go"}}}
{"type":"tool.finished","payload":{"id":"c2","outcome":"ok","result":"edited a.go at line 3"}}
{"type":"request.sent"}
{"type":"tool.call","payload":{"id":"c3","name":"glob","input":{"pattern":"*.go"}}}
{"type":"tool.finished","payload":{"id":"c3","outcome":"ok","result":"a.go\n\n[tack: unchanged: this call repeats an earlier one exactly and got the same result (repeat 1). Change the call, or finish.]"}}
{"type":"request.sent"}
{"type":"request.sent"}
{"type":"request.sent"}
{"type":"request.sent"}
{"type":"tool.call","payload":{"id":"c4","name":"edit","input":{"path":"a.go"}}}
{"type":"tool.finished","payload":{"id":"c4","outcome":"ok","result":"edited a.go at line 9"}}
{"type":"turn.end","payload":{"stop_reason":"repeat_loop"}}
`

// The guard table (§13.4) counts each setting's firings from the run's events, credits a firing with an edit applied
// within guardWindow requests after it and not later, counts stop_repeats from the turn's end, and marks a prompt-only
// setting as having no firing to count.
func TestReport_GuardFirings(t *testing.T) {
	r := rec(1, "B", false, "repeat_loop")
	r.Rep, r.Settings = 1, []string{"instructions_head", "result_notes", "stop_repeats"}
	p := writeResults(t, []pilot.Record{r})
	dir := filepath.Join(filepath.Dir(p), "task-01", "B-1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(guardEvents), 0o644); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if code := run([]string{p}, &out); code != 0 {
		t.Fatalf("exit %d:\n%s", code, out.String())
	}
	for _, want := range []string{
		"| B | result_notes | 1 | 1 | 2 | 2.00 | 1 (50%) | 0 (0%) | — | 1 (50%) |",
		"| B | stop_repeats | 1 | 1 | 1 | 1.00 | ends the turn | | | |",
		"| B | instructions_head | 1 | — | — | — | prompt only: no firing to count | | | |",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "could not be read") {
		t.Errorf("the run's events were not found:\n%s", out.String())
	}
}

type fakeSeer []int

func (f fakeSeer) seenByReq(pilot.Record, string) ([]int, error) { return f, nil }

// With a gold source, a firing is also followed when the gold lines seen rise within guardWindow requests after it:
// guardEvents' first firing (request 1) is followed by gold first seen at request 4, its second (request 4) by none.
func TestReport_GuardFiringsGold(t *testing.T) {
	r := rec(1, "B", false, "repeat_loop")
	r.Rep, r.Settings = 1, []string{"result_notes"}
	p := writeResults(t, []pilot.Record{r})
	dir := filepath.Join(filepath.Dir(p), "task-01", "B-1")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(guardEvents), 0o644)
	j, err := load(p)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	guards(&out, j, fakeSeer{0, 0, 0, 5, 5, 5, 5, 5})
	if want := "| B | result_notes | 1 | 1 | 2 | 2.00 | 1 (50%) | 0 (0%) | 1 (50%) | 1 (50%) |"; !strings.Contains(out.String(), want) {
		t.Errorf("missing %q in:\n%s", want, out.String())
	}
}

// night writes one night's results: per arm, n runs of task 1 with reps 1..n, the first passes of them passing, each
// with an events.jsonl in its run directory beside the file.
func night(t *testing.T, nA, passA, nB, passB int) string {
	t.Helper()
	var recs []pilot.Record
	for _, a := range []struct {
		arm     string
		n, pass int
	}{{"A", nA, passA}, {"B", nB, passB}} {
		for i := 1; i <= a.n; i++ {
			r := rec(1, a.arm, i <= a.pass, "done")
			r.Rep = i
			recs = append(recs, r)
		}
	}
	p := writeResults(t, recs)
	for _, r := range recs {
		d := runDirOf(p, r)
		os.MkdirAll(d, 0o755)
		os.WriteFile(filepath.Join(d, "events.jsonl"), []byte(`{"seq":1,"type":"user.message","payload":{}}`+"\n"), 0o644)
	}
	return p
}

// -rule prints d = B − A, each arm's n, and d's band at its edges (§12.25: sd 3.18, n 168, so +7 is shown, +4 to +6
// parked, +3 not shown). A job given as two night files keeps each record's run directory beside its own file, though
// both nights number their reps from 1.
func TestRuleBands(t *testing.T) {
	for _, c := range []struct {
		passA, passB int
		want         string
	}{
		{10, 17, "d = B − A = +7; edges 3.18 (1 sd) and 6.36 (2 sd), the registered sd\n- **verdict: shown: B beats A**"},
		{10, 16, "d = B − A = +6; edges 3.18 (1 sd) and 6.36 (2 sd), the registered sd\n- **verdict: parked"},
		{10, 14, "d = B − A = +4; edges 3.18 (1 sd) and 6.36 (2 sd), the registered sd\n- **verdict: parked"},
		{10, 13, "d = B − A = +3; edges 3.18 (1 sd) and 6.36 (2 sd), the registered sd\n- **verdict: not shown"},
		{14, 10, "d = B − A = -4; edges 3.18 (1 sd) and 6.36 (2 sd), the registered sd\n- **verdict: not shown"},
	} {
		n1 := night(t, 84, c.passA/2, 84, c.passB/2)
		n2 := night(t, 84, c.passA-c.passA/2, 84, c.passB-c.passB/2)
		var out strings.Builder
		if code := run([]string{"-rule", "sd=3.18,n=168", n1 + "," + n2}, &out); code != 0 {
			t.Fatalf("exit %d:\n%s", code, out.String())
		}
		s := out.String()
		if !strings.Contains(s, c.want) || !strings.Contains(s, "- A: ") || strings.Contains(s, "registered 168") {
			t.Errorf("A %d, B %d: want %q in\n%s", c.passA, c.passB, c.want, s[strings.Index(s, "## The registered rule"):])
		}
		if strings.Contains(s, "could not be read") {
			t.Errorf("the two nights' run directories were not found:\n%s", s)
		}
	}
	// The registered command's both.jsonl, the two nights concatenated in another directory, finds no run directory.
	n1, n2 := night(t, 2, 1, 2, 1), night(t, 2, 1, 2, 1)
	b1, _ := os.ReadFile(n1)
	b2, _ := os.ReadFile(n2)
	both := filepath.Join(t.TempDir(), "both.jsonl")
	os.WriteFile(both, append(b1, b2...), 0o644)
	var out strings.Builder
	run([]string{both}, &out)
	if !strings.Contains(out.String(), "8 runs' events could not be read") {
		t.Errorf("a concatenated file read its runs' events:\n%s", out.String())
	}
}

// An arm short of n is rate-scaled to n and judged on the sd edges, and an oracle error is a void run, in no count.
func TestRuleShortArmAndOracleError(t *testing.T) {
	recs := []pilot.Record{}
	for i := 1; i <= 168; i++ {
		a := rec(1, "A", i <= 10, "done")
		a.Rep = i
		recs = append(recs, a)
		if i <= 167 {
			b := rec(1, "B", i <= 16, "done")
			b.Rep = i
			recs = append(recs, b)
		}
	}
	bad := rec(1, "B", false, "done")
	bad.Pass = true // graded a pass, yet its oracle did not run: it must count in neither arm
	bad.Rep, bad.OracleErr = 168, "copy pkg/a_test.go: no such file"
	p := writeResults(t, append(recs, bad))
	var out strings.Builder
	if code := run([]string{"-rule", "sd=3.18,n=168", p}, &out); code != 0 {
		t.Fatalf("exit %d, want 0:\n%s", code, out.String())
	}
	s := out.String()
	for _, want := range []string{
		"- B: 16 passes of 167 counted runs (registered 168: -1)",
		"- void runs, in neither count: 1",
		"d = rate-scaled to 168: 168 · (16/167 − 10/168) = +6.0958",
		"the sd scaled for the short arm: 3.18 · √(168/2 · (1/168 + 1/167))",
		"edges 3.1847",
		"verdict: parked",
		"oracle error (a harness error, not a model failure): copy pkg/a_test.go",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("want %q in\n%s", want, s)
		}
	}
}

func TestParseRule(t *testing.T) {
	if r, err := parseRule("sd=3.18,n=168"); err != nil || r.sd != 3.18 || r.n != 168 {
		t.Fatalf("%+v %v", r, err)
	}
	for _, bad := range []string{"sd=3.18", "n=168", "sd=0,n=1", "sd=1,n=x", "sd=1,n=2,k=3", "3.18",
		"sd=NaN,n=168", "sd=+Inf,n=168", "sd=-Inf,n=168", "sd=1,sd=2,n=168", "sd=1,n=168,n=2"} {
		if _, err := parseRule(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

// band compares unrounded: d at exactly 2 sd is shown and a hair under is parked; d at exactly 1 sd is parked and a
// hair under is not shown.
func TestBandEdges(t *testing.T) {
	for _, c := range []struct {
		d    float64
		want string
	}{
		{6.36, "shown"}, {math.Nextafter(6.36, 0), "parked"}, {7, "shown"},
		{3.18, "parked"}, {math.Nextafter(3.18, 0), "not shown"}, {0, "not shown"},
	} {
		if got := band(c.d, 3.18); !strings.HasPrefix(got, c.want) {
			t.Errorf("band(%v, 3.18) = %q, want %s", c.d, got, c.want)
		}
	}
}

// A rate-scaled d just under 2 sd prints in full, so it never reads as the edge, and is judged on the sd scaled for
// the short arm: A 54 of 168, B 60 of 167 gives d = 6.3593…, parked, not "6.36" beside a parked verdict.
func TestRuleShortArmNearTheEdge(t *testing.T) {
	var recs []pilot.Record
	for i := 1; i <= 168; i++ {
		a := rec(1, "A", i <= 54, "done")
		a.Rep = i
		recs = append(recs, a)
		if i <= 167 {
			b := rec(1, "B", i <= 60, "done")
			b.Rep = i
			recs = append(recs, b)
		}
	}
	var out strings.Builder
	if code := run([]string{"-rule", "sd=3.18,n=168", writeResults(t, recs)}, &out); code != 0 {
		t.Fatalf("exit %d:\n%s", code, out.String())
	}
	s := out.String()
	if !strings.Contains(s, "= +6.3592") || strings.Contains(s, "= +6.36;") || !strings.Contains(s, "verdict: parked") {
		t.Errorf("want d in full and parked:\n%s", s[strings.Index(s, "## The registered rule"):])
	}
}

// TestReadStepsSkipsTheSummarizersRequests: a request.sent numbered 0 (compaction's summarizer) is not one of the run's
// requests, so a step's request number and the run's count are the conversation's.
func TestReadStepsSkipsTheSummarizersRequests(t *testing.T) {
	p := filepath.Join(t.TempDir(), "events.jsonl")
	os.WriteFile(p, []byte(strings.Join([]string{
		`{"seq":1,"type":"request.sent","payload":{"request":1}}`,
		`{"seq":2,"type":"request.sent","payload":{"request":0}}`,
		`{"seq":3,"type":"request.sent","payload":{"request":2}}`,
		`{"seq":4,"type":"tool.call","payload":{"id":"a","name":"read","input":{}}}`,
		`{"seq":5,"type":"tool.finished","payload":{"id":"a","outcome":"ok","result":"x"}}`,
		`{"seq":6,"type":"turn.end","payload":{"stop_reason":"end_turn"}}`,
	}, "\n")+"\n"), 0o644)
	steps, requests, _, err := readSteps(p)
	if err != nil || requests != 2 || len(steps) != 1 || steps[0].req != 2 {
		t.Fatalf("requests %d, steps %+v, %v; want 2 requests and the step at request 2", requests, steps, err)
	}
}
