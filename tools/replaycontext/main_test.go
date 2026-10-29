package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/eval/pilot"
	"github.com/townsendmerino/tack/internal/proto"
)

func writeEventsFile(t *testing.T, path string, evs []any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	ts := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	for i, p := range evs {
		var typ proto.EventType
		for k, spec := range proto.EventTypes {
			if spec.Payload == reflect.TypeOf(p) {
				typ = k
				break
			}
		}
		ev, err := proto.NewEvent(int64(i+1), ts.Add(time.Duration(i)*time.Second), typ, 0, p)
		if err != nil {
			t.Fatalf("NewEvent(%d): %v", i, err)
		}
		line, err := json.Marshal(ev)
		if err != nil {
			t.Fatalf("Marshal(%d): %v", i, err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReplayContext_EndToEndAndPrecisionOverSeenRuns(t *testing.T) {
	tmp := t.TempDir()
	work := filepath.Join(tmp, "work")
	mirrors := filepath.Join(tmp, "mirrors")
	mirror := filepath.Join(mirrors, "demo.git")

	runGit := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return strings.TrimSpace(string(out))
	}

	if err := os.MkdirAll(filepath.Join(work, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(work, "init", "-q")

	baseGo := "package pkg\n\nfunc Double(x int) int {\n\treturn x + x\n}\n"
	if err := os.WriteFile(filepath.Join(work, "pkg", "math.go"), []byte(baseGo), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(work, "add", ".")
	runGit(work, "commit", "-q", "-m", "base")
	baseSHA := runGit(work, "rev-parse", "HEAD")

	fixGo := "package pkg\n\nfunc Double(x int) int {\n\treturn x * 2\n}\n"
	if err := os.WriteFile(filepath.Join(work, "pkg", "math.go"), []byte(fixGo), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(work, "add", ".")
	runGit(work, "commit", "-q", "-m", "fix")
	fixSHA := runGit(work, "rev-parse", "HEAD")

	if err := os.MkdirAll(mirror, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(tmp, "clone", "--bare", "-q", work, mirror)

	jobDir := filepath.Join(tmp, "c6-demo")
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Run 1 (B, rep 1, fail): sees 0 lines (SeenLines == 0). Must not dilute Precision denominator.
	writeEventsFile(t, filepath.Join(jobDir, "task-01", "B-1", "events.jsonl"), []any{
		proto.RequestSent{Request: 1},
		proto.ToolCall{ID: "c1", Name: "bash", Input: json.RawMessage(`{"command":"go test ./..."}`)},
		proto.ToolFinished{ID: "c1", Outcome: proto.OutcomeOK, Result: "FAIL\n"},
	})
	// Run 2 (B, rep 2, fail): reads lines 1..4 of pkg/math.go (3 of 4 lines are in FixGold 3..5 -> Precision = 0.75).
	writeEventsFile(t, filepath.Join(jobDir, "task-01", "B-2", "events.jsonl"), []any{
		proto.RequestSent{Request: 1},
		proto.ToolCall{ID: "c1", Name: "read", Input: json.RawMessage(`{"path":"pkg/math.go","offset":1,"limit":4}`)},
		proto.ToolFinished{ID: "c1", Outcome: proto.OutcomeOK, Result: "1\n2\n3\n4\n"},
	})
	// Run 3 (A, rep 1, pass): reads and edits lines 3..5 of pkg/math.go (Precision = 1.0, FinalCov = 1.0).
	writeEventsFile(t, filepath.Join(jobDir, "task-01", "A-1", "events.jsonl"), []any{
		proto.RequestSent{Request: 1},
		proto.ToolCall{ID: "c1", Name: "read", Input: json.RawMessage(`{"path":"pkg/math.go","offset":3,"limit":3}`)},
		proto.ToolFinished{ID: "c1", Outcome: proto.OutcomeOK, Result: "3\n4\n5\n"},
		proto.ToolCall{ID: "c2", Name: "edit", Input: json.RawMessage(`{"path":"pkg/math.go","old_string":"\treturn x + x\n","new_string":"\treturn x * 2\n"}`)},
		proto.ToolFinished{ID: "c2", Outcome: proto.OutcomeOK, Result: "edited pkg/math.go at line 4"},
	})

	recs := []pilot.Record{
		{Task: 1, Repo: "demo", Base: baseSHA, Commit: fixSHA, Rep: 1, Pass: false, Run: pilot.Result{Arm: "B"}},
		{Task: 1, Repo: "demo", Base: baseSHA, Commit: fixSHA, Rep: 2, Pass: false, Run: pilot.Result{Arm: "B"}},
		{Task: 1, Repo: "demo", Base: baseSHA, Commit: fixSHA, Rep: 1, Pass: true, Run: pilot.Result{Arm: "A"}},
		{Task: 1, Repo: "demo", Base: baseSHA, Commit: fixSHA, Rep: 3, Pass: false, Run: pilot.Result{Arm: "B", Void: "infra"}},
	}
	var resBuf strings.Builder
	for _, r := range recs {
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		resBuf.Write(b)
		resBuf.WriteByte('\n')
	}
	resPath := filepath.Join(jobDir, "results.jsonl")
	if err := os.WriteFile(resPath, []byte(resBuf.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	dumpPath := filepath.Join(tmp, "dump.jsonl")
	var stdout bytes.Buffer
	code := run([]string{"-mirrors", mirrors, "-dump", dumpPath, resPath}, &stdout)
	if code != 0 {
		t.Fatalf("run exit=%d, output:\n%s", code, stdout.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "VERDICT: PASS — replaycontext: scored 3 of 3 runs across 1 tasks (1 pass, 2 fail;") {
		t.Fatalf("unexpected verdict in output:\n%s", out)
	}
	// Arm B has 2 failing runs (1 with SeenLines==0, 1 with Precision==2/4=0.500); aggregate Precision over SeenLines>0 must be 0.500 (not 0.250).
	if !strings.Contains(out, "/c6-demo | B | 2 | 0 | 1/2 | 0/2 | 0.333 | 0.500 | 0.500 | 0.333 | 0.333 | 0.500 |") {
		t.Fatalf("arm B row did not report Precision=0.500 over SeenLines>0 runs:\n%s", out)
	}
	dumpBytes, err := os.ReadFile(dumpPath)
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Split(strings.TrimSpace(string(dumpBytes)), "\n"); len(lines) != 3 {
		t.Fatalf("dump lines = %d, want 3", len(lines))
	}
}

func TestScanExclusionsAndJobLabels(t *testing.T) {
	excluded := []string{
		"/runs/e1/a1-2026-10-18/results.jsonl",
		"/runs/e1/a1-2026-10-18-day/results.jsonl",
		"/runs/e1/a1-smoke-2026-10-18-043528/results.jsonl",
		"/runs/e1/cal-c4c5-2026-10-18/c4/smoke-t1/results.jsonl",
		"/runs/e1/cal-c4c5-2026-10-18/c4/pass-1-aborted-0654/results.jsonl",
		"/runs/e1/c6-42-2026-10-20/runs/results.jsonl",
		"/runs/e1/c6-bundle-2026-10-28/results.jsonl",
	}
	for _, p := range excluded {
		if !isExcludedResultFile(p, false) {
			t.Errorf("isExcludedResultFile(%q, false) = false, want true", p)
		}
	}
	included := []string{
		"/runs/e1/a1-2026-10-18-day2/results.jsonl",
		"/runs/e1/cal-baseline-2026-10-18/pass-1/results.jsonl",
		"/runs/e1/c6-41-2026-10-20/runs/results.jsonl",
		"/runs/e1/c6-42-2026-10-20/runs-restart/results.jsonl",
	}
	for _, p := range included {
		if isExcludedResultFile(p, false) {
			t.Errorf("isExcludedResultFile(%q, false) = true, want false", p)
		}
	}

	if !shouldSkipRecord("cal-baseline-2026-10-18/pass-3/results.jsonl", pilot.Record{Task: 5}, false) {
		t.Error("expected pass-3 task 5 to be skipped")
	}
	if !shouldSkipRecord("c6-41-2026-10-20/runs/results.jsonl", pilot.Record{Task: 4}, false) {
		t.Error("expected c6-41/runs task 4 to be skipped")
	}
	if !shouldSkipRecord("c6-41-2026-10-20/runs-resume/results.jsonl", pilot.Record{Task: 6}, false) {
		t.Error("expected c6-41/runs-resume task 6 to be skipped")
	}
	if shouldSkipRecord("c6-41-2026-10-20/runs-resume/results.jsonl", pilot.Record{Task: 5}, false) {
		t.Error("expected c6-41/runs-resume task 5 to be kept")
	}
}
