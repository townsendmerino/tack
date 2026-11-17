//go:build darwin && sandboxtest

package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/townsendmerino/tack/internal/sandbox"
)

// TestTrustYesChild is the child of TestTrustYesIsRefusedInsideTheSandbox: with TACK_TRUST_CHILD set to a directory it runs
// tack trust -yes there, as it is, and prints what became of it. Without it, it does nothing.
func TestTrustYesChild(t *testing.T) {
	ws := os.Getenv("TACK_TRUST_CHILD")
	if ws == "" {
		t.Skip("run by TestTrustYesIsRefusedInsideTheSandbox")
	}
	var out, errb bytes.Buffer
	code := dispatch([]string{"trust", "-yes", "none", ws}, &out, &errb)
	fmt.Printf("TRUSTCHILD code=%d err=%q\n", code, errb.String())
}

// TestTrustYesIsRefusedInsideTheSandbox: tack trust -yes run by a process inside the real Seatbelt profile (what a command
// a model ran would be) is refused for the sandbox, with nothing recorded; the same process outside it is stopped by
// the terminal check instead, so the sandbox is what the first refusal is about.
func TestTrustYesIsRefusedInsideTheSandbox(t *testing.T) {
	dir := func() string { d, _ := filepath.EvalSymlinks(t.TempDir()); return d }
	ws, home, tmp, cache := dir(), dir(), dir(), dir()
	run := func(wrap bool) string {
		cmd := exec.Command(os.Args[0], "-test.run=^TestTrustYesChild$", "-test.v")
		cmd.Env = append(os.Environ(), "TACK_TRUST_CHILD="+ws, "HOME="+home, "TMPDIR="+tmp, "GOCACHE="+cache)
		if wrap {
			cfg := sandbox.Config{Workspace: ws, Temp: tmp, GoCache: cache, Home: home, Group: sandbox.NewMark("g"), Mark: sandbox.NewMark("c")}
			cmd = sandbox.Wrap(cfg, cmd)
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("child (sandboxed %v): %v\n%s", wrap, err, out)
		}
		for line := range strings.SplitSeq(string(out), "\n") {
			if strings.HasPrefix(line, "TRUSTCHILD ") {
				return line
			}
		}
		t.Fatalf("child (sandboxed %v) printed no result:\n%s", wrap, out)
		return ""
	}
	if got := run(true); !strings.Contains(got, "code=1") || !strings.Contains(got, "sandbox") {
		t.Errorf("inside the sandbox: %s; want a refusal for the sandbox", got)
	}
	if got := run(false); !strings.Contains(got, "code=1") || !strings.Contains(got, "terminal") || strings.Contains(got, "inside a sandbox") {
		t.Errorf("outside the sandbox: %s; want the terminal check to be what stops it", got)
	}
}
