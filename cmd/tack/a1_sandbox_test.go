//go:build darwin && sandboxtest

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/eval/mine"
	"github.com/townsendmerino/tack/internal/eval/pilot"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
)

// TestA1RunsAndGrades drives the A1 arm below its ssh layer: RunPilot with tack's own binary (this test binary,
// standing in) runs `tack run --edit` in the sandbox against a scripted model through the capture. The model reads,
// edits, runs go build and calls done; the run is graded by the oracle at the task's fix commit. The record carries
// tack's summary, in which the bash call succeeded (a sandbox denying the repository fails it), the sampling check
// passes on the first model request, and the oracle's output is kept.
func TestA1RunsAndGrades(t *testing.T) {
	task, mirrors := a1Task(t)
	script := goinfertest.NewScripted("qwen", 32768,
		goinfertest.Calls("", "r1", "read", `{"path":"calc/calc.go"}`),
		goinfertest.Calls("", "e1", "edit", `{"path":"calc/calc.go","old_string":"a - b","new_string":"a + b"}`),
		goinfertest.Calls("", "b1", "bash", `{"command":"go build ./..."}`),
		goinfertest.Calls("", "d1", "done", `{"reason":"Add adds"}`))
	rec, out := a1Run(t, task, mirrors, script)
	stderrLog, _ := os.ReadFile(filepath.Join(out, "task-01", "A1", "stderr.log"))
	if !rec.Pass || rec.Run.Arm != "A1" || rec.Run.Exit != 0 || rec.Run.Void != "" || rec.TackRun == nil ||
		rec.TackRun.StopReason != "done" || rec.TackRun.ToolCalls["edit"] != 1 || rec.TackRun.ToolCalls["bash"] != 1 ||
		rec.TackRun.Failed["bash"] != 0 || rec.TackRun.DoneRefused != 0 {
		t.Fatalf("record %+v\ntack %+v\nstderr %s", rec, rec.TackRun, stderrLog)
	}
	if !strings.Contains(string(stderrLog), `tack run: preflight "go version": ok: go version go1.`) {
		t.Errorf("no passing preflight in tack's stderr:\n%s", stderrLog)
	}
	if b, _ := os.ReadFile(filepath.Join(out, "task-01", "A1", "oracle.log")); !strings.Contains(string(b), `"Action":"pass"`) {
		t.Errorf("oracle.log: %s", b)
	}
	if err := pilot.CheckSampling(filepath.Join(out, "task-01", "A1", "requests")); err != nil {
		t.Errorf("sampling: %v", err)
	}
}

// TestA1PreflightFailureIsVoid: when the preflight cannot run go in the sandbox (here, no go on tack's PATH), the task
// is recorded void with tack's reason, not graded, and the model is never asked anything.
func TestA1PreflightFailureIsVoid(t *testing.T) {
	task, mirrors := a1Task(t)
	t.Setenv("PATH", "/usr/bin:/bin")
	script := goinfertest.NewScripted("qwen", 32768, goinfertest.Calls("", "d1", "done", `{"reason":"x"}`))
	rec, _ := a1Run(t, task, mirrors, script)
	if !strings.Contains(rec.Run.Void, `tack run: preflight "go version" failed`) || rec.Pass || rec.Run.Exit != 8 ||
		rec.TackRun != nil || len(rec.Oracle) != 0 {
		t.Fatalf("record %+v", rec)
	}
	if script.Left() != 1 {
		t.Errorf("the model was asked %d time(s); want none", 1-script.Left())
	}
}

// a1Task is a one-task pilot over a fresh repository where Add subtracts, and the mirror directory holding it.
func a1Task(t *testing.T) (pilot.Task, string) {
	t.Helper()
	src := t.TempDir()
	git := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = src
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(rel, body string) {
		os.MkdirAll(filepath.Join(src, filepath.Dir(rel)), 0o755)
		os.WriteFile(filepath.Join(src, rel), []byte(body), 0o644)
	}
	git("init", "-q", "-b", "main")
	write("go.mod", "module m\n\ngo 1.27\n")
	write("calc/calc.go", "package calc\n\nfunc Add(a, b int) int { return a - b }\n")
	git("add", "-A")
	git("commit", "-q", "-m", "base")
	base := git("rev-parse", "HEAD")
	write("calc/calc.go", "package calc\n\nfunc Add(a, b int) int { return a + b }\n")
	write("calc/calc_test.go", "package calc\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(2, 2) != 4 {\n\t\tt.Fatal(Add(2, 2))\n\t}\n}\n")
	git("add", "-A")
	git("commit", "-q", "-m", "fix")
	commit := git("rev-parse", "HEAD")
	mirrors := t.TempDir()
	if out, err := exec.Command("git", "clone", "-q", "--mirror", src, filepath.Join(mirrors, "m.git")).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	task := pilot.Task{N: 1, Candidate: mine.Candidate{Repo: "m", Commit: commit, Base: base, Tests: []string{"TestAdd"},
		Packages: []string{"./calc"}, TestFiles: []string{"calc/calc_test.go"}, Statement: "Add subtracts; it should add.",
		ReviewedBy: "owner", Verified: &mine.Verified{OK: true, Oracle: []string{"TestAdd"}}}}
	return task, mirrors
}

// a1Run runs the A1 arm of a pilot over task with this test binary as tack, against script through the capture, and
// returns its one record and the job's output directory.
func a1Run(t *testing.T, task pilot.Task, mirrors string, script *goinfertest.Scripted) (pilot.Record, string) {
	t.Helper()
	t.Setenv("GOFLAGS", "")
	t.Setenv("TACK_TEST_AS_TACK", "1")
	ts := httptest.NewServer(script)
	defer ts.Close()
	capture, err := pilot.StartCapture(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer capture.Close()
	out := t.TempDir()
	serveLog := filepath.Join(out, "serve.log")
	os.WriteFile(serveLog, nil, 0o644)
	err = pilot.RunPilot(context.Background(), pilot.Options{Tasks: []pilot.Task{task}, Mirrors: mirrors, A1: os.Args[0],
		Server:   pilot.Server{BaseURL: capture.URL, APIKey: "k", Model: "qwen", Context: 32768, Direct: ts.URL},
		ServeLog: serveLog, Out: out, Cap: 2 * time.Minute, Log: io.Discard, Capture: capture})
	if err != nil {
		t.Fatal(err)
	}
	f, _ := os.Open(filepath.Join(out, "results.jsonl"))
	defer f.Close()
	var rec pilot.Record
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	if !sc.Scan() || json.Unmarshal(sc.Bytes(), &rec) != nil {
		t.Fatal("no record")
	}
	return rec, out
}
