package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHaltAndResume: tack halt writes the HALT file and sweeps; tack run refuses while it exists; tack resume lifts it.
func TestHaltAndResume(t *testing.T) {
	swept := 0
	sweep = func() (int, error) { swept++; return 2, nil }
	t.Cleanup(func() { sweep = sweepAll })
	cfg := writeConfig(t, "http://cassette.invalid")
	state := filepath.Join(filepath.Dir(cfg), "state")
	var out, errb bytes.Buffer
	if code := dispatch([]string{"halt", "-config", cfg, "-reason", "drill"}, &out, &errb); code != exitOK {
		t.Fatalf("tack halt: %d %s", code, errb.String())
	}
	if swept != 1 || !strings.Contains(out.String(), "tack is halted") || !strings.Contains(out.String(), "2 sandboxed process(es) killed") {
		t.Fatalf("tack halt said %q after %d sweeps", out.String(), swept)
	}
	if b, err := os.ReadFile(filepath.Join(state, "HALT")); err != nil || !strings.Contains(string(b), `"reason":"drill"`) {
		t.Fatalf("HALT: %q %v", b, err)
	}
	errb.Reset()
	if code := dispatch([]string{"run", "--config", cfg, "--workspace", t.TempDir(), "hello"}, &out, &errb); code != exitHalted {
		t.Fatalf("tack run while halted: exit %d, %s", code, errb.String())
	}
	out.Reset()
	if code := dispatch([]string{"resume", "-config", cfg}, &out, &errb); code != exitOK || out.String() != "tack resumed\n" {
		t.Fatalf("tack resume: %d %q", code, out.String())
	}
	if _, err := os.Stat(filepath.Join(state, "HALT")); !os.IsNotExist(err) {
		t.Fatalf("HALT still there: %v", err)
	}
}
