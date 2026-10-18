package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	for _, v := range []string{"moe-cache-experts", "--moe-cache-experts"} {
		if err := opts.Set(v); err != nil {
			t.Fatalf("%s: %v", v, err)
		}
	}
	if got := strings.Join(opts.args(), " "); got != "-moe-cache-experts" {
		t.Errorf("goinfer-serve gets %q", got)
	}
	r := remote{host: "nobara", tack: "tack", serve: "serve", modelPath: "m", out: "o", rev: "r", ctxN: 32768, options: opts}
	if got := strings.Join(r.serveCmdArgs(), " "); !strings.HasSuffix(got, "-tack-rev r -serve-option moe-cache-experts") {
		t.Errorf("pilot-serve over ssh: %s", got)
	}
}
