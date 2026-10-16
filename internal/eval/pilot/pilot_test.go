package pilot

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// With TACK_FAKE_OPENCODE set, this test binary is opencode: it records its args, cwd and XDG dirs, prints two JSON
// events, and then either exits or (TACK_FAKE_OPENCODE=hang) starts a child and sleeps, so the cap must kill a group.
func TestMain(m *testing.M) {
	switch os.Getenv("TACK_FAKE_OPENCODE") {
	case "":
		os.Exit(m.Run())
	case "child":
		time.Sleep(time.Hour)
		return
	}
	wd, _ := os.Getwd()
	rec, _ := json.Marshal(map[string]any{"args": os.Args[1:], "cwd": wd, "config": os.Getenv("XDG_CONFIG_HOME"),
		"data": os.Getenv("XDG_DATA_HOME")})
	fmt.Println(`{"type":"step_start"}`)
	fmt.Println(string(rec))
	if os.Getenv("TACK_FAKE_OPENCODE") == "hang" {
		child := exec.Command(os.Args[0])
		child.Env = append(os.Environ(), "TACK_FAKE_OPENCODE=child")
		_ = child.Start()
		_ = os.WriteFile(os.Getenv("TACK_FAKE_CHILD_PID"), []byte(fmt.Sprint(child.Process.Pid)), 0o644)
		time.Sleep(time.Hour)
	}
}

func TestConfig(t *testing.T) {
	s := Server{BaseURL: "http://127.0.0.1:1", APIKey: "k", Model: "qwen", Context: 16384}
	var a0, a0k map[string]any
	b, _ := Config(s, Arm{Name: "A0"}, "/r")
	json.Unmarshal(b, &a0)
	b, _ = Config(s, Arm{Name: "A0k", KenMCP: "/bin/ken-mcp", KenModel: "/m"}, "/r")
	json.Unmarshal(b, &a0k)
	if _, ok := a0["mcp"]; ok {
		t.Fatal("A0 has an MCP server")
	}
	ken := a0k["mcp"].(map[string]any)["ken"].(map[string]any)
	env := ken["environment"].(map[string]any)
	if env["KEN_MCP_ALLOWED_REPO_ROOTS"] != "/r" || env["KEN_MCP_DEFAULT_REPO"] != "/r" || env["KEN_MCP_AUTO_FETCH"] != "0" ||
		env["KEN_MCP_MODEL_DIR"] != "/m" {
		t.Fatalf("ken-mcp env %v", env)
	}
	prov := a0["provider"].(map[string]any)["goinfer"].(map[string]any)
	if prov["options"].(map[string]any)["baseURL"] != "http://127.0.0.1:1/v1" || a0["small_model"] != "goinfer/qwen-fast" {
		t.Fatalf("provider %v small %v", prov, a0["small_model"])
	}
	lim := prov["models"].(map[string]any)["qwen"].(map[string]any)["limit"].(map[string]any)
	if lim["context"] != float64(16384) {
		t.Fatalf("limit.context %v: without it opencode never compacts", lim["context"])
	}
}

// TestPrepareRepoReachesNoLaterCommit builds a three-commit repository and prepares a task repository at the middle
// commit: the base is checked out and nothing after it is reachable, git log --all included.
func TestPrepareRepoReachesNoLaterCommit(t *testing.T) {
	src := t.TempDir()
	gitDo := func(dir string, args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	gitDo(src, "init", "-q", "-b", "main")
	var shas []string
	for i := 0; i < 3; i++ {
		os.WriteFile(filepath.Join(src, "f.txt"), []byte(fmt.Sprint(i)), 0o644)
		gitDo(src, "add", "f.txt")
		gitDo(src, "commit", "-q", "-m", fmt.Sprint("c", i))
		shas = append(shas, gitDo(src, "rev-parse", "HEAD"))
	}
	repo := filepath.Join(t.TempDir(), "task")
	if err := PrepareRepo(src, shas[1], repo); err != nil {
		t.Fatal(err)
	}
	if got := gitDo(repo, "rev-parse", "HEAD"); got != shas[1] {
		t.Fatalf("HEAD %s, want base %s", got, shas[1])
	}
	all := gitDo(repo, "log", "--all", "--format=%H")
	if strings.Contains(all, shas[2]) || !strings.Contains(all, shas[0]) {
		t.Fatalf("git log --all:\n%s\nmust hold the base's ancestors and not %s", all, shas[2])
	}
}

func TestRunOpencode(t *testing.T) {
	repo, out := t.TempDir(), t.TempDir()
	t.Setenv("TACK_FAKE_OPENCODE", "ok")
	r := RunOpencode(context.Background(), os.Args[0], Server{BaseURL: "u", Model: "qwen", Context: 16384}, Arm{Name: "A0"}, repo, "do it", out, time.Minute)
	if r.Exit != 0 || r.CapHit || r.Err != "" {
		t.Fatalf("result %+v", r)
	}
	ev, _ := os.ReadFile(filepath.Join(out, "events.jsonl"))
	lines := strings.Split(strings.TrimSpace(string(ev)), "\n")
	var rec struct {
		Args         []string
		Cwd          string
		Config, Data string
	}
	json.Unmarshal([]byte(lines[1]), &rec)
	if strings.Join(rec.Args, " ") != "run --format json --auto -m goinfer/qwen --dir "+repo+" do it" {
		t.Fatalf("args %q", rec.Args)
	}
	if !strings.HasPrefix(rec.Config, out) || !strings.HasPrefix(rec.Data, out) {
		t.Fatalf("opencode's config and data are not isolated under the run: %+v", rec)
	}
	if _, err := os.Stat(filepath.Join(repo, "opencode.json")); err != nil {
		t.Fatal("no opencode.json in the repository")
	}
}

// TestCapKillsTheGroup: opencode that hangs, with a child, is killed at the cap, child included.
func TestCapKillsTheGroup(t *testing.T) {
	repo, out := t.TempDir(), t.TempDir()
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	t.Setenv("TACK_FAKE_OPENCODE", "hang")
	t.Setenv("TACK_FAKE_CHILD_PID", pidFile)
	r := RunOpencode(context.Background(), os.Args[0], Server{Model: "qwen", Context: 16384}, Arm{Name: "A0"}, repo, "x", out, 500*time.Millisecond)
	if !r.CapHit || r.Exit != -1 || r.WallSecs > 5 {
		t.Fatalf("result %+v", r)
	}
	var pid int
	b, _ := os.ReadFile(pidFile)
	fmt.Sscan(string(b), &pid)
	if pid == 0 {
		t.Fatal("the fake never started its child")
	}
	time.Sleep(100 * time.Millisecond)
	if err := syscall.Kill(pid, 0); err == nil {
		syscall.Kill(pid, syscall.SIGKILL)
		t.Fatalf("child %d survived the cap", pid)
	}
}
