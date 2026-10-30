package main

import (
	"encoding/json"
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
