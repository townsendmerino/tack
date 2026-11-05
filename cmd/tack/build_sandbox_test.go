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

// TestBuildAfterEditInTheSandbox: tack run --edit with [run] build_after_edit, end to end on macOS. The build runs in the
// session's real shell under the real Seatbelt profile: a write that breaks the package gets go's error in its result,
// the write that fixes it gets nothing, and the session's own bash still runs from where it was.
func TestBuildAfterEditInTheSandbox(t *testing.T) {
	ws, _ := filepath.EvalSymlinks(t.TempDir())
	os.WriteFile(filepath.Join(ws, "go.mod"), []byte("module x\n\ngo 1.27\n"), 0o644)
	os.MkdirAll(filepath.Join(ws, "p"), 0o755)
	os.WriteFile(filepath.Join(ws, "p", "x.go"), []byte("package p\n\nfunc Add(a, b int) int { return a - b }\n"), 0o644)
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"commit", "-q", "-m", "base"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = ws
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	script := goinfertest.NewScripted("m", 32768,
		goinfertest.Calls("", "c1", "bash", `{"command":"cd p"}`),
		goinfertest.Calls("", "r1", "read", `{"path":"p/x.go"}`),
		goinfertest.Calls("", "w1", "write", `{"path":"p/x.go","content":"package p\n\nfunc Add(a, b int) int { return a + c }\n"}`),
		goinfertest.Calls("", "w2", "write", `{"path":"p/x.go","content":"package p\n\nfunc Add(a, b int) int { return a + b }\n"}`),
		goinfertest.Calls("", "b1", "bash", `{"command":"pwd"}`),
		goinfertest.Calls("", "d1", "done", `{"reason":"Add adds"}`))
	ts := httptest.NewServer(script)
	defer ts.Close()
	cfg := writeConfig(t, ts.URL)
	f, err := os.OpenFile(cfg, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("[run]\nbuild_after_edit = true\n")
	f.Close()
	var stdout, stderr bytes.Buffer
	code := dispatch([]string{"run", "--config", cfg, "--workspace", ws, "--edit", "--json", "Make Add add."}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("exit %d\nstderr: %s\nstdout: %s", code, stderr.String(), stdout.String())
	}
	results := map[string]proto.ToolFinished{}
	for line := range strings.SplitSeq(strings.TrimSpace(stdout.String()), "\n") {
		var ev proto.Event
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		if v, _ := ev.Decode(); v != nil {
			if p, ok := v.(proto.ToolFinished); ok {
				results[p.ID] = p
			}
		}
	}
	if r := results["w1"].Result; !strings.Contains(r, "[tack: this change does not build (go build ./p):\np/x.go:3:37: undefined: c]") {
		t.Errorf("the breaking write: %q", r)
	}
	if r := results["w2"].Result; strings.Contains(r, "[tack:") {
		t.Errorf("the fixing write: %q", r)
	}
	if r := results["b1"].Result; !strings.Contains(r, filepath.Join(ws, "p")) {
		t.Errorf("the build moved the session's working directory: pwd = %q", r)
	}
}
