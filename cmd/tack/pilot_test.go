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
