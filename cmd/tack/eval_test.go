package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// TestMeasurementsRefuseAnUnknownRevision runs both measurement commands the way a night job does, from a binary
// that records no VCS revision: a go test binary is one. Each must refuse before reading its inputs, so the paths
// here need not exist, and end with an INCONCLUSIVE verdict naming the reason.
func TestMeasurementsRefuseAnUnknownRevision(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out")
	cases := map[string][]string{
		"eval models": {"models", "-manifest", "/nonexistent/m.toml", "-out", out, "-tack-rev", "b10d501"},
		"eval pilot": {"pilot", "-smoke", "-mirrors", "/x", "-opencode", "/x", "-ken-mcp", "/x", "-ken-model", "/x",
			"-goinfer-serve", "/x", "-model-path", "/x", "-out", out, "-tack-rev", "b10d501"},
	}
	for name, args := range cases {
		var stdout, stderr bytes.Buffer
		code := evalCmd(args, &stdout, &stderr)
		if code == exitOK || !strings.Contains(stderr.String(), "records no VCS revision") ||
			!strings.HasPrefix(stdout.String(), "VERDICT: INCONCLUSIVE — "+name+": not started") {
			t.Errorf("%s: exit %d\nstdout: %s\nstderr: %s", name, code, stdout.String(), stderr.String())
		}
	}
}
