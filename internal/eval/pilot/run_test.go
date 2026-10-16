package pilot

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/eval/mine"
)

// fixture is a source repository with a base commit and a fix commit that adds a function and its test, and the task
// that describes it, as the miner would record it.
type fixture struct {
	src      string
	task     Task
	fixedSrc string // the fix's version of calc/calc.go, for the fake opencode to copy in
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	src := t.TempDir()
	gitDo := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = src
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(rel, body string) {
		os.MkdirAll(filepath.Join(src, filepath.Dir(rel)), 0o755)
		os.WriteFile(filepath.Join(src, rel), []byte(body), 0o644)
	}
	gitDo("init", "-q", "-b", "main")
	write("go.mod", "module m\n\ngo 1.22\n")
	write("calc/calc.go", "package calc\n\nfunc Add(a, b int) int { return a - b }\n")
	write("AGENTS.md", "a b\n")           // opencode reads this one
	write("CLAUDE.md", "one two three\n") // and not this
	gitDo("add", "-A")
	gitDo("commit", "-q", "-m", "base")
	base := gitDo("rev-parse", "HEAD")
	fixed := "package calc\n\nfunc Add(a, b int) int { return a + b }\n"
	write("calc/calc.go", fixed)
	write("calc/calc_test.go", "package calc\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(2, 2) != 4 {\n\t\tt.Fatal(Add(2, 2))\n\t}\n}\n")
	gitDo("add", "-A")
	gitDo("commit", "-q", "-m", "fix")
	commit := gitDo("rev-parse", "HEAD")
	fixedSrc := filepath.Join(t.TempDir(), "calc.go")
	os.WriteFile(fixedSrc, []byte(fixed), 0o644)
	c := mine.Candidate{Repo: "m", Commit: commit, Base: base, Tests: []string{"TestAdd"}, Packages: []string{"./calc"},
		TestFiles: []string{"calc/calc_test.go"}, Statement: "Add subtracts; it should add.", ReviewedBy: "owner",
		Verified: &mine.Verified{OK: true, Oracle: []string{"TestAdd"}}}
	return fixture{src: src, task: Task{N: 1, Candidate: c}, fixedSrc: fixedSrc}
}

func TestOracle(t *testing.T) {
	t.Setenv("GOFLAGS", "")
	f := newFixture(t)
	ctx := context.Background()
	unfixed := filepath.Join(t.TempDir(), "r1")
	if err := PrepareRepo(f.src, f.task.Base, unfixed); err != nil {
		t.Fatal(err)
	}
	if pass, res, err := Oracle(ctx, f.src, f.task, unfixed); err != nil || pass || res["TestAdd"] != "fail" {
		t.Fatalf("unfixed: pass=%v %v %v", pass, res, err)
	}
	fixed := filepath.Join(t.TempDir(), "r2")
	PrepareRepo(f.src, f.task.Base, fixed)
	b, _ := os.ReadFile(f.fixedSrc)
	os.WriteFile(filepath.Join(fixed, "calc", "calc.go"), b, 0o644)
	if pass, res, err := Oracle(ctx, f.src, f.task, fixed); err != nil || !pass {
		t.Fatalf("fixed: pass=%v %v %v", pass, res, err)
	}
}

func TestLoadTasksRefusesDrafts(t *testing.T) {
	f := newFixture(t)
	p := filepath.Join(t.TempDir(), "pilot.jsonl")
	write := func(cs ...mine.Candidate) {
		var b strings.Builder
		for _, c := range cs {
			j, _ := json.Marshal(c)
			b.Write(append(j, '\n'))
		}
		os.WriteFile(p, []byte(b.String()), 0o644)
	}
	approved := f.task.Candidate
	draft := approved
	draft.ReviewedBy = ""
	write(approved, draft)
	if _, err := LoadTasks(p, 1, 2); err == nil || !strings.Contains(err.Error(), "no approved statement") {
		t.Fatalf("a draft statement was accepted: %v", err)
	}
	if ts, err := LoadTasks(p, 1, 1); err != nil || len(ts) != 1 || ts[0].N != 1 {
		t.Fatalf("the approved task: %v %v", ts, err)
	}
}

func TestArmOrderAlternates(t *testing.T) {
	a0, a0k := Arm{Name: "A0"}, Arm{Name: "A0k"}
	if ArmOrder(1, a0, a0k)[0].Name != "A0" || ArmOrder(2, a0, a0k)[0].Name != "A0k" {
		t.Fatal("A0 must go first on odd tasks and A0k on even ones")
	}
}

func TestRequests(t *testing.T) {
	log := filepath.Join(t.TempDir(), "serve.log")
	os.WriteFile(log, []byte("before\nrequest: /v1/chat/completions model=q status=200 prompt_tokens=900 completion_tokens=40 ttft=812ms total=2100ms reused=850 slot=0\nother\n"), 0o644)
	got := Requests(log, int64(len("before\n")), size(log))
	if len(got) != 1 || got[0].Prompt != 900 || got[0].Reused != 850 || got[0].TTFTms != 812 || got[0].Completion != 40 {
		t.Fatalf("requests %+v", got)
	}
	if got := Requests(log, 0, int64(len("before\n"))); len(got) != 0 {
		t.Fatal("a line outside the window was read")
	}
}

func TestSummarizeEvents(t *testing.T) {
	repo := t.TempDir()
	root := resolved(repo)
	ev := filepath.Join(t.TempDir(), "events.jsonl")
	lines := []string{
		`{"type":"tool_use","part":{"tool":"read","state":{"input":{"filePath":"` + root + `/calc/calc.go"}}}}`,
		`{"type":"tool_use","part":{"tool":"read","state":{"input":{"filePath":"` + root + `/.ken/snapshot.bin"}}}}`,
		`{"type":"tool_use","part":{"tool":"bash","state":{"input":{"command":"cat ../../other/secret.go"}}}}`,
		`{"type":"tool_use","part":{"tool":"glob","state":{"input":{"pattern":"**/*.go"}}}}`,
	}
	os.WriteFile(ev, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
	s := SummarizeEvents(ev, repo)
	if s.Events != 4 || s.ToolCalls["read"] != 2 || s.ToolCalls["bash"] != 1 || s.ToolCalls["glob"] != 1 {
		t.Fatalf("summary %+v", s)
	}
	if len(s.LeftRepo) != 1 || !strings.HasSuffix(s.LeftRepo[0], "other/secret.go") {
		t.Fatalf("left_repo %v: the climb out must be flagged, the repository and .ken must not", s.LeftRepo)
	}
}

// TestRunPilotEndToEnd runs one task through both arms with the fakes: A0's opencode fixes the bug, A0k's (which runs
// after ken's warm-up) does not and also reads outside the repository. The records carry the outcome, the oracle,
// capped time, the untimed warm-up and the leak flag.
func TestRunPilotEndToEnd(t *testing.T) {
	t.Setenv("GOFLAGS", "")
	f := newFixture(t)
	mirrors := t.TempDir()
	if out, err := exec.Command("git", "clone", "-q", "--mirror", f.src, filepath.Join(mirrors, "m.git")).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	t.Setenv("TACK_FAKE_OPENCODE", "ok")
	t.Setenv("TACK_FAKE_KENMCP", "1")
	t.Setenv("TACK_FAKE_OPENCODE_FIX", f.fixedSrc)
	t.Setenv("TACK_FAKE_OPENCODE_FIX_DEST", "calc/calc.go")
	t.Setenv("TACK_FAKE_OPENCODE_FIX_ARM_HAS_MCP", "false") // only A0 fixes it
	out := t.TempDir()
	serveLog := filepath.Join(out, "serve.log")
	os.WriteFile(serveLog, nil, 0o644)
	t.Setenv("TACK_FAKE_SERVE_LOG", serveLog)
	count := func(_ context.Context, text string) (int, error) { return len(strings.Fields(text)) + 20, nil } // 20: the template
	err := RunPilot(context.Background(), Options{Tasks: []Task{f.task}, Mirrors: mirrors, Opencode: os.Args[0],
		A0: Arm{Name: "A0"}, A0k: Arm{Name: "A0k", KenMCP: os.Args[0], KenModel: "/m"},
		Server: Server{BaseURL: "http://x", Model: "qwen", Context: 16384}, ServeLog: serveLog, Out: out,
		Cap: time.Minute, KenWarmup: 30 * time.Second, Log: io.Discard, CountTokens: count})
	if err != nil {
		t.Fatal(err)
	}
	rf, _ := os.Open(filepath.Join(out, "results.jsonl"))
	defer rf.Close()
	recs := map[string]Record{}
	sc := bufio.NewScanner(rf)
	for sc.Scan() {
		var r Record
		json.Unmarshal(sc.Bytes(), &r)
		recs[r.Run.Arm] = r
	}
	a0, a0k := recs["A0"], recs["A0k"]
	if !a0.Pass || a0.CappedSecs >= 60 || a0.KenIndexSecs != 0 || !strings.Contains(mustRead(t, filepath.Join(out, "task-01", "A0", "diff.patch")), "a + b") {
		t.Errorf("A0 %+v", a0)
	}
	if a0k.Pass || a0k.CappedSecs != 60 || a0k.KenIndexSecs <= 0 || a0k.Oracle["TestAdd"] != "fail" {
		t.Errorf("A0k %+v", a0k)
	}
	if strings.Contains(mustRead(t, filepath.Join(out, "task-01", "A0k", "diff.patch")), ".ken") {
		t.Error("ken's index reached A0k's graded diff")
	}
	// Each arm's fixed prompt, and the title call and re-prefill counts, come through runOne from the run's log window.
	for arm, fixed := range map[string]int{"A0": 1000, "A0k": 4500} {
		p := recs[arm].Prompt
		if p.Statement != 5 || p.FirstPrompt != fixed+5 || p.Fixed != fixed || p.Main != 4 || p.FastRequests != 1 ||
			p.Reprefills != 1 || p.SnapRestores != 1 || len(p.Instructions) != 1 || p.Instructions["AGENTS.md"] != 2 ||
			p.Own != fixed-2 {
			t.Errorf("%s prompt %+v, want fixed %d", arm, p, fixed)
		}
	}
}

func mustRead(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
