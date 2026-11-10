//go:build darwin && sandboxtest

package main

import (
	"archive/zip"
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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
	task, mirrors := a1Task(t, false)
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
	if rec.Power != "ac" && rec.Power != "battery" && !strings.Contains(rec.Power, "→") {
		t.Errorf("the record's power source is %q", rec.Power)
	}
	if strings.Join(rec.ServerOptions, ",") != "moe-cache-experts" {
		t.Errorf("the record lists server options %q", rec.ServerOptions)
	}
	if !strings.Contains(string(stderrLog), fmt.Sprintf("tack run: preflight %q: ok: ", pilot.PreflightCommand)) {
		t.Errorf("no passing preflight in tack's stderr:\n%s", stderrLog)
	}
	run := filepath.Join(out, "task-01", "A1")
	if _, err := os.Stat(filepath.Join(run, "repo")); !os.IsNotExist(err) {
		t.Errorf("the repository copy is still there after grading: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(run, "diff.patch")); err != nil || !strings.Contains(string(b), "+func Add(a, b int) int { return a + b }") {
		t.Errorf("diff.patch: %v %s", err, b)
	}
	if b, _ := os.ReadFile(filepath.Join(out, "task-01", "A1", "oracle.log")); !strings.Contains(string(b), `"Action":"pass"`) {
		t.Errorf("oracle.log: %s", b)
	}
	if err := pilot.CheckSampling(filepath.Join(out, "task-01", "A1", "requests")); err != nil {
		t.Errorf("sampling: %v", err)
	}
}

// TestA1PreflightFailureIsVoid: when the preflight cannot run go in the sandbox (here, no go on tack's PATH, though the
// pilot has go), the task is recorded void with tack's reason, not graded, and the model is never asked anything.
func TestA1PreflightFailureIsVoid(t *testing.T) {
	task, mirrors := a1Task(t, false)
	t.Setenv("TACK_TEST_PATH", "/usr/bin:/bin")
	script := goinfertest.NewScripted("qwen", 32768, goinfertest.Calls("", "d1", "done", `{"reason":"x"}`))
	rec, _ := a1Run(t, task, mirrors, script)
	if !strings.Contains(rec.Run.Void, fmt.Sprintf("tack run: preflight %q failed", pilot.PreflightCommand)) || rec.Pass || rec.Run.Exit != 8 ||
		rec.TackRun != nil || len(rec.Oracle) != 0 {
		t.Fatalf("record %+v", rec)
	}
	if script.Left() != 1 {
		t.Errorf("the model was asked %d time(s); want none", 1-script.Left())
	}
}

// TestA1LowDiskIsVoid: a run whose output volume has less free space than it needs is void before anything starts:
// the record says why, no repository is made, and the model is never asked.
func TestA1LowDiskIsVoid(t *testing.T) {
	task, mirrors := a1Task(t, false)
	script := goinfertest.NewScripted("qwen", 32768, goinfertest.Calls("", "d1", "done", `{"reason":"x"}`))
	rec, out := a1Run(t, task, mirrors, script, func(o *pilot.Options) { o.MinFree = 1 << 62 })
	if !strings.HasPrefix(rec.Run.Void, "disk: ") || !strings.Contains(rec.Run.Void, "GB a run needs") || rec.Pass || rec.TackRun != nil {
		t.Fatalf("record %+v", rec)
	}
	if script.Left() != 1 {
		t.Errorf("the model was asked %d time(s); want none", 1-script.Left())
	}
	if _, err := os.Stat(filepath.Join(out, "task-01", "A1", "repo")); !os.IsNotExist(err) {
		t.Errorf("a repository was made for a void run: %v", err)
	}
}

// TestA1HostSleepIsVoid: a run during which the host slept more than MaxSleepGap is void, naming the request the
// sleep hit, not graded as the model's.
func TestA1HostSleepIsVoid(t *testing.T) {
	task, mirrors := a1Task(t, false)
	script := goinfertest.NewScripted("qwen", 32768,
		goinfertest.Calls("", "r1", "read", `{"path":"calc/calc.go"}`),
		goinfertest.Calls("", "d1", "done", `{"reason":"x"}`),
		goinfertest.Calls("", "d2", "done", `{"reason":"x"}`))
	rec, _ := a1Run(t, task, mirrors, script, func(o *pilot.Options) {
		o.Capture.SleepGap = func(start, end time.Time, where string) time.Duration {
			if where == "during request 2" {
				return 45 * time.Second
			}
			return 0
		}
	})
	if rec.Run.Void != "host slept 45 s during request 2" || rec.Pass || rec.TackRun != nil {
		t.Fatalf("record %+v", rec)
	}
}

// a1Task is a one-task pilot over a fresh repository where Add subtracts, and the mirror directory holding it. With
// dep, the repository needs a module, example.com/dep, that only a local proxy serves (a1Proxy), and the module cache
// starts empty.
func a1Task(t *testing.T, dep bool) (pilot.Task, string) {
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
	if dep {
		a1Proxy(t)
		write("calc/calc.go", "package calc\n\nimport \"example.com/dep\"\n\nfunc Add(a, b int) int { return a - b + dep.Zero }\n")
		tidy := exec.Command("go", "mod", "tidy")
		tidy.Dir = src
		tidy.Env = append(os.Environ(), "GOFLAGS=-modcacherw", "GOMODCACHE="+t.TempDir()) // the run's cache stays empty
		if out, err := tidy.CombinedOutput(); err != nil {
			t.Fatalf("go mod tidy: %v %s", err, out)
		}
	}
	git("add", "-A")
	git("commit", "-q", "-m", "base")
	base := git("rev-parse", "HEAD")
	if dep {
		write("calc/calc.go", "package calc\n\nimport \"example.com/dep\"\n\nfunc Add(a, b int) int { return a + b + dep.Zero }\n")
	} else {
		write("calc/calc.go", "package calc\n\nfunc Add(a, b int) int { return a + b }\n")
	}
	write("calc/calc_test.go", "package calc\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(2, 2) != 4 {\n\t\tt.Fatal(Add(2, 2))\n\t}\n}\n")
	git("add", "-A")
	git("commit", "-q", "-m", "fix")
	commit := git("rev-parse", "HEAD")
	mirrors := t.TempDir()
	if out, err := exec.Command("git", "clone", "-q", "--mirror", src, filepath.Join(mirrors, "m.git")).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	task := pilot.Task{N: 1, Repo: "m", Commit: commit, Base: base, Tests: []string{"TestAdd"},
		Packages: []string{"./calc"}, TestFiles: []string{"calc/calc_test.go"}, Statement: "Add subtracts; it should add.",
		ReviewedBy: "owner", Verified: &mine.Verified{OK: true, Oracle: []string{"TestAdd"}}}
	return task, mirrors
}

// a1Proxy serves example.com/dep v1.0.0 from a file proxy, with an empty module cache and no checksum database.
func a1Proxy(t *testing.T) {
	t.Helper()
	proxy := t.TempDir()
	v := filepath.Join(proxy, "example.com", "dep", "@v")
	os.MkdirAll(v, 0o755)
	mod := "module example.com/dep\n\ngo 1.26\n"
	os.WriteFile(filepath.Join(v, "list"), []byte("v1.0.0\n"), 0o644)
	os.WriteFile(filepath.Join(v, "v1.0.0.info"), []byte(`{"Version":"v1.0.0"}`), 0o644)
	os.WriteFile(filepath.Join(v, "v1.0.0.mod"), []byte(mod), 0o644)
	f, err := os.Create(filepath.Join(v, "v1.0.0.zip"))
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	for name, body := range map[string]string{"go.mod": mod, "dep.go": "package dep\n\nconst Zero = 0\n"} {
		w, _ := z.Create("example.com/dep@v1.0.0/" + name)
		w.Write([]byte(body))
	}
	if err := errors.Join(z.Close(), f.Close()); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOPROXY", "file://"+proxy)
	t.Setenv("GOSUMDB", "off")
	cache := t.TempDir()
	t.Setenv("GOMODCACHE", cache)
	t.Cleanup(func() { // the cache's files are read-only; this runs before TempDir's removal
		filepath.WalkDir(cache, func(p string, d os.DirEntry, err error) error {
			if err == nil && d.IsDir() {
				os.Chmod(p, 0o755)
			}
			return nil
		})
	})
}

// TestA1DownloadsModulesBeforeTheSandbox: a task whose repository needs a module missing from the cache runs, because
// the pilot downloads it outside the sandbox before tack starts. The sandbox can neither fetch nor write the cache, so
// without the download the preflight fails and the task is void.
func TestA1DownloadsModulesBeforeTheSandbox(t *testing.T) {
	task, mirrors := a1Task(t, true)
	script := goinfertest.NewScripted("qwen", 32768,
		goinfertest.Calls("", "r1", "read", `{"path":"calc/calc.go"}`),
		goinfertest.Calls("", "e1", "edit", `{"path":"calc/calc.go","old_string":"a - b","new_string":"a + b"}`),
		goinfertest.Calls("", "b1", "bash", `{"command":"go build ./..."}`),
		goinfertest.Calls("", "d1", "done", `{"reason":"Add adds"}`))
	rec, out := a1Run(t, task, mirrors, script)
	stderrLog, _ := os.ReadFile(filepath.Join(out, "task-01", "A1", "stderr.log"))
	if !rec.Pass || rec.Run.Void != "" || rec.TackRun == nil || rec.TackRun.ToolCalls["bash"] != 1 || rec.TackRun.Failed["bash"] != 0 {
		t.Fatalf("record %+v\ntack %+v\nstderr %s", rec, rec.TackRun, stderrLog)
	}
}

// a1Run runs the A1 arm of a pilot over task with this test binary as tack, against script through the capture, and
// returns its one record and the job's output directory.
func a1Run(t *testing.T, task pilot.Task, mirrors string, script *goinfertest.Scripted, set ...func(*pilot.Options)) (pilot.Record, string) {
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
	o := pilot.Options{Tasks: []pilot.Task{task}, Mirrors: mirrors, A1: os.Args[0],
		Server:   pilot.Server{BaseURL: capture.URL, APIKey: "k", Model: "qwen", Context: 32768, Direct: ts.URL},
		ServeLog: serveLog, Out: out, Cap: 2 * time.Minute, Log: io.Discard, Capture: capture, ServerOptions: []string{"moe-cache-experts"},
		MinFree: 1} // the test machine's free space is not under test; TestA1LowDiskIsVoid sets it
	for _, f := range set {
		f(&o)
	}
	err = pilot.RunPilot(context.Background(), o)
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

// TestA1CompareInterleavesAndChecksCells drives an A/B job through RunPilot with tack's own binary: a cell the check
// voids is listed and re-run in its place, the six runs go A B B A A B, each records its cell state and settings, and
// only arm B's tack gets fit_window in its config.
func TestA1CompareInterleavesAndChecksCells(t *testing.T) {
	task, mirrors := a1Task(t, false)
	var replies []goinfertest.Reply
	for range 6 { // done with nothing changed is refused once, then accepted
		replies = append(replies, goinfertest.Calls("", "d1", "done", `{"reason":"x"}`), goinfertest.Calls("", "d2", "done", `{"reason":"x"}`))
	}
	script := goinfertest.NewScripted("qwen", 32768, replies...)
	checks := 0
	_, out := a1Run(t, task, mirrors, script, func(o *pilot.Options) {
		o.A1Arms = []pilot.Arm{{Name: "A"}, {Name: "B", Settings: []string{"fit_window"}}}
		o.CellWait = 10 * time.Millisecond
		o.Cell = func(context.Context) (string, string) {
			if checks++; checks == 1 {
				return "heavy state", "test: heavy load"
			}
			return "light state", ""
		}
	})
	f, _ := os.Open(filepath.Join(out, "results.jsonl"))
	defer f.Close()
	var got []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		var r pilot.Record
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		if r.Run.Void != "" {
			if r.Run.Void != "cell: test: heavy load" || r.Cell != "heavy state" {
				t.Errorf("void record %+v", r)
			}
			got = append(got, "void")
			continue
		}
		if r.Cell != "light state" || (r.Run.Arm == "B") != (strings.Join(r.Settings, ",") == "fit_window") {
			t.Errorf("run %s-%d: cell %q, settings %v", r.Run.Arm, r.Rep, r.Cell, r.Settings)
		}
		got = append(got, fmt.Sprintf("%s%d", r.Run.Arm, r.Rep))
		cfg, err := os.ReadFile(filepath.Join(pilot.TackDir(filepath.Join(out, "task-01", fmt.Sprintf("%s-%d", r.Run.Arm, r.Rep))), "config.toml"))
		if err != nil || strings.Contains(string(cfg), "fit_window = true") != (r.Run.Arm == "B") {
			t.Errorf("run %s-%d's config (%v):\n%s", r.Run.Arm, r.Rep, err, cfg)
		}
	}
	if strings.Join(got, " ") != "void A1 B1 B2 A2 A3 B3" {
		t.Fatalf("records %v, want the void cell then A B B A A B", got)
	}
}

// TestA1CompareRepsRunsEachArmNTimes: two arms with A1Reps run each task that many times per arm, in A B B A order
// repeated, through RunPilot, each run after its own cell check. interleave's own test covers longer orders.
func TestA1CompareRepsRunsEachArmNTimes(t *testing.T) {
	task, mirrors := a1Task(t, false)
	var replies []goinfertest.Reply
	for range 4 { // done with nothing changed is refused once, then accepted
		replies = append(replies, goinfertest.Calls("", "d1", "done", `{"reason":"x"}`), goinfertest.Calls("", "d2", "done", `{"reason":"x"}`))
	}
	script := goinfertest.NewScripted("qwen", 32768, replies...)
	checks := 0
	_, out := a1Run(t, task, mirrors, script, func(o *pilot.Options) {
		o.A1Arms, o.A1Reps = []pilot.Arm{{Name: "A"}, {Name: "B", Settings: []string{"fit_window"}}}, 2
		o.Cell = func(context.Context) (string, string) { checks++; return "light state", "" }
	})
	f, _ := os.Open(filepath.Join(out, "results.jsonl"))
	defer f.Close()
	var got []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		var r pilot.Record
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		if r.Run.Void != "" || (r.Run.Arm == "B") != (strings.Join(r.Settings, ",") == "fit_window") {
			t.Errorf("run %s-%d: void %q, settings %v", r.Run.Arm, r.Rep, r.Run.Void, r.Settings)
		}
		got = append(got, fmt.Sprintf("%s%d", r.Run.Arm, r.Rep))
	}
	if strings.Join(got, " ") != "A1 B1 B2 A2" || checks != 4 {
		t.Fatalf("records %v after %d cell checks, want A B B A, 2 per arm (not the default 3), after 4", got, checks)
	}
}

// TestA1LoneArmRepeatsWithSettings: one arm with settings and A1Reps runs each task that many times, as <arm>-1,
// <arm>-2, each after its own cell check, each record carrying the settings and its tack config switching them on.
func TestA1LoneArmRepeatsWithSettings(t *testing.T) {
	task, mirrors := a1Task(t, false)
	var replies []goinfertest.Reply
	for range 2 {
		replies = append(replies, goinfertest.Calls("", "d1", "done", `{"reason":"x"}`), goinfertest.Calls("", "d2", "done", `{"reason":"x"}`))
	}
	script := goinfertest.NewScripted("qwen", 32768, replies...)
	checks := 0
	_, out := a1Run(t, task, mirrors, script, func(o *pilot.Options) {
		o.A1Arms, o.A1Reps = []pilot.Arm{{Name: "A1", Settings: []string{"verify_done", "stop_repeats"}}}, 2
		o.Cell = func(context.Context) (string, string) { checks++; return "light state", "" }
	})
	f, _ := os.Open(filepath.Join(out, "results.jsonl"))
	defer f.Close()
	var got []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		var r pilot.Record
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		name := fmt.Sprintf("%s-%d", r.Run.Arm, r.Rep)
		got = append(got, name)
		cfg, err := os.ReadFile(filepath.Join(pilot.TackDir(filepath.Join(out, "task-01", name)), "config.toml"))
		if r.Run.Void != "" || r.Cell != "light state" || strings.Join(r.Settings, ",") != "verify_done,stop_repeats" || err != nil ||
			!strings.Contains(string(cfg), "verify_done = true") || !strings.Contains(string(cfg), "stop_repeats = true") {
			t.Errorf("run %s: void %q, cell %q, settings %v; config (%v):\n%s", name, r.Run.Void, r.Cell, r.Settings, err, cfg)
		}
	}
	if strings.Join(got, " ") != "A1-1 A1-2" || checks != 2 {
		t.Fatalf("records %v after %d cell checks, want A1-1 A1-2 after 2", got, checks)
	}
}

// TestA1CannotReadTheMirror is the answer-leak escape test, run as an E1 run is: tack's own binary through RunPilot,
// in the sandbox. The model's bash asks the mirror for the fix commit's test, and the workspace's git for FETCH_HEAD,
// which would name the mirror. The mirror cannot be read, FETCH_HEAD is gone, and the record's left_repo names the
// mirror the call reached for.
func TestA1CannotReadTheMirror(t *testing.T) {
	task, mirrors := a1Task(t, false)
	mirror := filepath.Join(mirrors, "m.git")
	show, _ := json.Marshal(map[string]string{"command": "git -C " + mirror + " show " + task.Commit + ":calc/calc_test.go"})
	script := goinfertest.NewScripted("qwen", 32768,
		goinfertest.Calls("", "b1", "bash", string(show)),
		goinfertest.Calls("", "b2", "bash", `{"command":"cat .git/FETCH_HEAD"}`),
		goinfertest.Calls("", "d1", "done", `{"reason":"x"}`), goinfertest.Calls("", "d2", "done", `{"reason":"x"}`))
	rec, out := a1Run(t, task, mirrors, script)
	evs, _ := os.ReadFile(filepath.Join(out, "task-01", "A1", "events.jsonl"))
	results := map[string]string{}
	for line := range strings.SplitSeq(string(evs), "\n") {
		var ev struct {
			Type    string `json:"type"`
			Payload struct {
				ID     string `json:"id"`
				Result string `json:"result"`
			} `json:"payload"`
		}
		if json.Unmarshal([]byte(line), &ev) == nil && ev.Type == "tool.finished" {
			results[ev.Payload.ID] = ev.Payload.Result
		}
	}
	if r := results["b1"]; strings.Contains(r, "func TestAdd") || !strings.Contains(r, "Operation not permitted") {
		t.Errorf("the mirror's fix test reached the model, or git failed for a reason other than the sandbox: %q", r)
	}
	if r := results["b2"]; r == "" || strings.Contains(r, mirror) || !strings.Contains(r, "No such file") {
		t.Errorf("FETCH_HEAD: %q", r)
	}
	if !slices.Contains(rec.Events.LeftRepo, mirror) {
		t.Errorf("left_repo %q does not name the mirror %s", rec.Events.LeftRepo, mirror)
	}
}
