package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/townsendmerino/tack/tools/gate"
)

func TestPinnedVersion(t *testing.T) {
	gomod, err := os.ReadFile(filepath.Join("..", "..", pinGoMod))
	if err != nil {
		t.Fatal(err)
	}
	v, err := pinnedVersion(string(gomod))
	if err != nil || v == "" {
		t.Fatalf("pinnedVersion = %q, %v", v, err)
	}
	if !versionMatches("staticcheck 2026.2.1 ("+v+")\n", v) {
		t.Errorf("versionMatches rejects the pin's own version string")
	}
	if versionMatches("staticcheck 2025.1.1 (0.6.1)\n", v) {
		t.Errorf("versionMatches accepts an older staticcheck")
	}
}

func TestCanaryFired(t *testing.T) {
	hit := "tools/lint/canary/canary.go:8:2: field deadField is unused (U1000)\n"
	if !canaryFired(hit) {
		t.Error("a real canary finding did not count as fired")
	}
	// The failure shapes: nothing printed (could not run), an import error (cannot read export data), and a finding
	// that is not the canary's.
	for _, out := range []string{
		"",
		"-: internal error in importing \"strings\" (unsupported version: 5)\n",
		"other/x.go:1:1: should omit type (ST1023)\n",
	} {
		if canaryFired(out) {
			t.Errorf("canaryFired(%q) = true", out)
		}
	}
}

// TestBrokenStaticcheckIsInconclusive runs the gate with a "staticcheck" that prints nothing and exits 0, the
// signature of a staticcheck that cannot read export data. No cell may pass after the version check.
func TestBrokenStaticcheckIsInconclusive(t *testing.T) {
	fake := filepath.Join(t.TempDir(), "staticcheck")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cells := checks(filepath.Join("..", ".."), fake)
	for _, c := range cells {
		if c.Outcome == gate.OK {
			t.Errorf("cell %s passed on a staticcheck that analyses nothing", c.Name)
		}
	}
	if r := gate.Reconcile(cells); r.Outcome != gate.Inconclusive {
		t.Errorf("run = %s, want INCONCLUSIVE", r.Outcome)
	}
}
