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
