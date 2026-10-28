package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/townsendmerino/tack/internal/eval/pilot"
)

// TestPilotRefusesWithoutGo: with no `go` on the PATH, every pilot job refuses before it starts, with an error that
// says how to fix it, and writes nothing. Without the check, the oracle would grade every run build-fail.
func TestPilotRefusesWithoutGo(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // a directory with no go in it
	dir := t.TempDir()
	for name, args := range map[string][]string{
		"oracle check": {"eval", "pilot", "-oracle-check", "-tasks", filepath.Join(dir, "pilot.jsonl"), "-mirrors", dir,
			"-out", filepath.Join(dir, "out-check")},
		"pilot run": {"eval", "pilot", "-tasks", filepath.Join(dir, "pilot.jsonl"), "-mirrors", dir, "-opencode", "x",
			"-ken-mcp", "x", "-ken-model", "x", "-goinfer-serve", "x", "-model-path", "x", "-out", filepath.Join(dir, "out-run")},
	} {
		var stdout, stderr bytes.Buffer
		code := dispatch(args, &stdout, &stderr)
		if code != exitError || !strings.Contains(stderr.String(), "`go` is not on this job's PATH") ||
			!strings.Contains(stderr.String(), "export PATH=/usr/local/go/bin") ||
			!strings.HasPrefix(stdout.String(), "VERDICT: INCONCLUSIVE") {
			t.Errorf("%s: exit %d\nstdout: %s\nstderr: %s", name, code, stdout.String(), stderr.String())
		}
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "out-") {
				t.Errorf("%s: wrote %s before refusing", name, e.Name())
			}
		}
	}
}

// TestPilotE0Gate: a gated job whose E0 arm did not meet the smoke rule does not run. It records why in NOT-RUN.txt,
// says so in its verdict and exits exitNotRun before anything else starts. One that met it records its score in
// GATE.txt and goes on (here to the next refusal, the tack revision, since this test binary has none).
func TestPilotE0Gate(t *testing.T) {
	dir := t.TempDir()
	results := filepath.Join(dir, "results.jsonl")
	run := func(summary string) (int, string, string, string) {
		os.WriteFile(results, []byte(summary+"\n"), 0o644)
		out := t.TempDir()
		var stdout, stderr bytes.Buffer
		code := dispatch([]string{"eval", "pilot", "-e0-results", results, "-e0-arm", "coder", "-tasks", filepath.Join(dir, "p.jsonl"),
			"-mirrors", dir, "-opencode", "x", "-ken-mcp", "x", "-ken-model", "x", "-goinfer-serve", "x", "-model-path", "x",
			"-out", out}, &stdout, &stderr)
		return code, out, stdout.String(), stderr.String()
	}
	code, out, stdout, stderr := run(`{"arm":"coder","phase":"smoke_summary","pass":[18,16],"malformed":0}`)
	notRun, _ := os.ReadFile(filepath.Join(out, "NOT-RUN.txt"))
	if code != exitNotRun || !strings.Contains(string(notRun), "smoke 18/16") || !strings.HasPrefix(stdout, "VERDICT: INCONCLUSIVE") ||
		!strings.Contains(stdout, "not run: E0 gate") {
		t.Fatalf("refused gate: exit %d, NOT-RUN.txt %q\nstdout %s\nstderr %s", code, notRun, stdout, stderr)
	}
	code, out, _, stderr = run(`{"arm":"coder","phase":"smoke_summary","pass":[18,17],"malformed":0}`)
	gateTxt, _ := os.ReadFile(filepath.Join(out, "GATE.txt"))
	if code == exitNotRun || !strings.Contains(string(gateTxt), "smoke 18/17 of 20, malformed 0") || !strings.Contains(stderr, "the E0 gate passed") {
		t.Fatalf("passed gate: exit %d, GATE.txt %q\nstderr %s", code, gateTxt, stderr)
	}
	if _, err := os.Stat(filepath.Join(out, "NOT-RUN.txt")); err == nil {
		t.Fatal("a passed gate wrote NOT-RUN.txt")
	}
	var stdout2, stderr2 bytes.Buffer
	if code := dispatch([]string{"eval", "pilot", "-e0-results", results, "-out", dir}, &stdout2, &stderr2); code != exitUsage {
		t.Fatalf("-e0-results without -e0-arm: exit %d", code)
	}
}

// TestForwardHasItsOwnConnection: the A1 port forward opts out of ssh connection sharing, so killing it closes the
// forward instead of leaving it to a shared master connection.
func TestForwardHasItsOwnConnection(t *testing.T) {
	args := strings.Join(forwardCmd("nobara", 50001, 40001).Args, " ")
	if !strings.Contains(args, "-o ControlPath=none") || !strings.HasSuffix(args, "-L 127.0.0.1:50001:127.0.0.1:40001 nobara") {
		t.Fatalf("forward: %s", args)
	}
}

// TestServeOptionsAreAnAllowlist: pilot-serve and eval pilot -a1 refuse a goinfer-serve option not on the allowlist
// before anything starts, and pass an allowed one through: to pilot-serve's command over ssh, and to goinfer-serve.
func TestServeOptionsAreAnAllowlist(t *testing.T) {
	dir := t.TempDir()
	for name, args := range map[string][]string{
		"pilot-serve": {"eval", "pilot-serve", "-goinfer-serve", "x", "-model-path", "x", "-out", dir,
			"-serve-option", "kv-sessions"},
		"pilot -a1": {"eval", "pilot", "-a1", "-tasks", "x", "-mirrors", dir, "-out", dir, "-remote-host", "h",
			"-remote-tack", "x", "-remote-out", "x", "-goinfer-serve", "x", "-model-path", "x", "-serve-option", "--ctx"},
	} {
		var stdout, stderr bytes.Buffer
		if code := dispatch(args, &stdout, &stderr); code != exitUsage || !strings.Contains(stderr.String(), "is not passed through") {
			t.Errorf("%s: exit %d, want %d refusing the option\nstderr: %s", name, code, exitUsage, stderr.String())
		}
	}
	var opts serveOptionList
	for _, v := range []string{"moe-cache-experts", "--moe-cache-experts", "thinking-off"} {
		if err := opts.Set(v); err != nil {
			t.Fatalf("%s: %v", v, err)
		}
	}
	if got := strings.Join(opts.args(), " "); got != "-moe-cache-experts -thinking off" {
		t.Errorf("goinfer-serve gets %q", got)
	}
	r := remote{host: "nobara", tack: "tack", serve: "serve", modelPath: "m", out: "o", rev: "r", ctxN: 32768, options: opts}
	if got := strings.Join(r.serveCmdArgs(), " "); !strings.HasSuffix(got, "-tack-rev r -serve-option moe-cache-experts -serve-option thinking-off") {
		t.Errorf("pilot-serve over ssh: %s", got)
	}
}

// TestOnlyTasks: -only keeps the named tasks in their order, keeps all with no list, and refuses a number that was not
// loaded or is not a number, so a typo cannot shrink a job silently.
func TestOnlyTasks(t *testing.T) {
	var ts []pilot.Task
	for n := 1; n <= 12; n++ {
		ts = append(ts, pilot.Task{N: n})
	}
	got, err := onlyTasks(ts, "9, 2,3,4,6,8")
	var ns []int
	for _, x := range got {
		ns = append(ns, x.N)
	}
	if err != nil || fmt.Sprint(ns) != "[2 3 4 6 8 9]" {
		t.Fatalf("got %v, %v", ns, err)
	}
	if all, err := onlyTasks(ts, ""); err != nil || len(all) != 12 {
		t.Fatalf("no list: %d tasks, %v", len(all), err)
	}
	for _, bad := range []string{"2,13", "2,x", "2,,3"} {
		if _, err := onlyTasks(ts, bad); err == nil {
			t.Errorf("-only %q was accepted", bad)
		}
	}
}

// TestA1Arms: -compare-setting makes arms A and B, B with every -compare-setting added to A's -setting list, so the bundle
// runs against A1 as registered; -setting or -reps alone makes one arm A1 with the settings, run
// -reps times; both check every cell. With neither, A1 runs as registered, unchecked. -reps with -compare-setting, a
// negative -reps, an unknown setting and a setting on in both arms are refused.
func TestA1Arms(t *testing.T) {
	adopted := settingList{"fit_window", "stop_repeats"}
	arms, reps, cells, err := remote{settings: adopted, compare: settingList{"verify_done"}}.arms()
	if err != nil || len(arms) != 2 || reps != 0 || !cells || fmt.Sprint(arms[0].Settings) != "[fit_window stop_repeats]" ||
		fmt.Sprint(arms[1].Settings) != "[fit_window stop_repeats verify_done]" {
		t.Errorf("compare: %+v %d %v %v", arms, reps, cells, err)
	}
	// The bundle against A1 as registered: several -compare-setting and no -setting.
	arms, _, cells, err = remote{compare: settingList{"fit_window", "stop_repeats", "verify_done"}}.arms()
	if err != nil || len(arms) != 2 || !cells || len(arms[0].Settings) != 0 ||
		fmt.Sprint(arms[1].Settings) != "[fit_window stop_repeats verify_done]" {
		t.Errorf("bundle: %+v %v %v", arms, cells, err)
	}
	arms, reps, cells, err = remote{settings: append(adopted, "verify_done"), reps: 2}.arms()
	if err != nil || len(arms) != 1 || arms[0].Name != "A1" || reps != 2 || !cells ||
		fmt.Sprint(arms[0].Settings) != "[fit_window stop_repeats verify_done]" {
		t.Errorf("one arm: %+v %d %v %v", arms, reps, cells, err)
	}
	if arms, _, cells, err = (remote{settings: adopted}).arms(); err != nil || len(arms) != 1 || !cells {
		t.Errorf("settings alone: %+v %v %v", arms, cells, err)
	}
	if arms, reps, cells, err = (remote{}).arms(); err != nil || arms != nil || reps != 0 || cells {
		t.Errorf("as registered: %+v %d %v %v", arms, reps, cells, err)
	}
	for name, r := range map[string]remote{
		"reps with compare": {compare: settingList{"verify_done"}, reps: 2},
		"negative reps":     {reps: -1},
		"unknown compare":   {compare: settingList{"verify_dne"}},
		"in both arms":      {settings: adopted, compare: settingList{"verify_done", "stop_repeats"}},
	} {
		if _, _, _, err := r.arms(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
