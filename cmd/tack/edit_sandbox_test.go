//go:build darwin && sandboxtest

package main

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
)

// TestEditRunInTheSandbox: tack run --edit, end to end on macOS. A scripted model writes a file, runs go test through
// bash (the real tack shell under the real Seatbelt profile), then calls done. The run exits 0 with stop reason done,
// the file is written, the test passed inside the sandbox, and a command writing outside the workspace was refused.
func TestEditRunInTheSandbox(t *testing.T) {
	ws, _ := filepath.EvalSymlinks(t.TempDir())
	outside, _ := filepath.EvalSymlinks(t.TempDir())
	os.WriteFile(filepath.Join(ws, "go.mod"), []byte("module x\n\ngo 1.27\n"), 0o644)
	os.WriteFile(filepath.Join(ws, "x.go"), []byte("package x\n\nfunc Add(a, b int) int { return a - b }\n"), 0o644)
	os.WriteFile(filepath.Join(ws, "x_test.go"), []byte("package x\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(2, 2) != 4 {\n\t\tt.Fatal(Add(2, 2))\n\t}\n}\n"), 0o644)
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"commit", "-q", "-m", "base"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = ws
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	script := goinfertest.NewScripted("m", 32768,
		goinfertest.Calls("", "r1", "read", `{"path":"x.go"}`),
		goinfertest.Calls("", "w1", "write", `{"path":"x.go","content":"package x\n\nfunc Add(a, b int) int { return a + b }\n"}`),
		goinfertest.Calls("", "b1", "bash", `{"command":"go test ./..."}`),
		goinfertest.Calls("", "b2", "bash", `{"command":"/usr/bin/touch `+filepath.Join(outside, "planted")+`"}`),
		goinfertest.Calls("", "d1", "done", `{"reason":"Add adds"}`))
	ts := httptest.NewServer(script)
	defer ts.Close()
	var stdout, stderr bytes.Buffer
	code := dispatch([]string{"run", "--config", writeConfig(t, ts.URL), "--workspace", ws, "--edit", "--json", "Make Add add."}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("exit %d\nstderr: %s\nstdout: %s", code, stderr.String(), stdout.String())
	}
	if b, _ := os.ReadFile(filepath.Join(ws, "x.go")); !strings.Contains(string(b), "a + b") {
		t.Fatalf("x.go: %s", b)
	}
	if _, err := os.Stat(filepath.Join(outside, "planted")); err == nil {
		t.Fatal("a command wrote outside the workspace")
	}
	results := map[string]proto.ToolFinished{}
	stop := ""
	for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		var ev proto.Event
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		v, _ := ev.Decode()
		switch p := v.(type) {
		case proto.ToolFinished:
			results[p.ID] = p
		case proto.TurnEnd:
			stop = p.StopReason
		}
	}
	if stop != proto.StopDone {
		t.Errorf("stop reason %q", stop)
	}
	if r := results["b1"]; r.Outcome != proto.OutcomeOK || !strings.Contains(r.Result, "ok") {
		t.Errorf("go test in the sandbox: %+v", r)
	}
	if r := results["b2"]; !strings.Contains(r.Result, "Operation not permitted") {
		t.Errorf("writing outside the workspace: %+v", r)
	}
}
