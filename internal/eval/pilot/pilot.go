// Package pilot runs E1's A0-against-A0k pilot (docs/measurements/e1-pilot-preregistration-2026-10-16.md): opencode,
// without and with ken-mcp, against one goinfer-serve, on task repositories that hold only a base commit and its
// ancestors.
package pilot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// PrepareRepo makes dir a fresh repository holding only base and its ancestors, fetched from mirror, with a main
// branch at base. No later commit is reachable from its refs.
func PrepareRepo(mirror, base, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"fetch", "-q", "--no-tags", mirror, base},
		{"checkout", "-q", "-b", "main", "FETCH_HEAD"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %v: %v: %s", args, err, out)
		}
	}
	return nil
}

// Server is the goinfer-serve every run talks to.
type Server struct {
	BaseURL string // e.g. http://127.0.0.1:41234
	APIKey  string
	Model   string // the served model name
	Context int    // the served window, opencode's limit.context
	// Direct is goinfer's own URL when BaseURL is the recording proxy: tack's count_tokens calls use it, so they are
	// neither recorded nor counted as the run's requests.
	Direct string
	// Fit is goinfer's fit line for the load ("fit: N-token cap needs X GB KV (weights Y GB, …)"), printed by -cell-check.
	Fit string
}

// Arm is one configuration of opencode.
type Arm struct {
	Name   string // "A0" or "A0k"
	KenMCP string // ken-mcp's path for A0k; empty for A0
	// KenModel is ken's embedding model directory, passed to ken-mcp explicitly.
	KenModel string
	// NoTitle turns off opencode's title agent, so opencode makes no title request on small_model; the pilot sets it the same on
	// both arms (pre-registration, amendment 3).
	NoTitle bool
	// Settings are A1's: the [run] settings switched on in this arm's config.toml, each one of RunSettings.
	Settings []string
}

// replyLimit is opencode's limit.output: the tokens a reply may take, and so what goinfer reserves beyond the prompt
// (docs/measurements/e1-pilot-preregistration-2026-10-16.md, amendment 2).
const replyLimit = 2048

// Config is the opencode.json the run's repository gets: goinfer's `connect opencode` shape (the
// @ai-sdk/openai-compatible provider at the server, limit.context the served window, small_model the -fast alias),
// each model's options the registered Sampling, plus ken-mcp as a local MCP server confined to the repository for A0k.
func Config(s Server, a Arm, repo string) ([]byte, error) {
	model := map[string]any{"limit": map[string]any{"context": s.Context, "output": replyLimit}, "options": Sampling}
	cfg := map[string]any{
		"$schema": "https://opencode.ai/config.json",
		"provider": map[string]any{"goinfer": map[string]any{
			"npm":     "@ai-sdk/openai-compatible",
			"options": map[string]any{"baseURL": s.BaseURL + "/v1", "apiKey": s.APIKey},
			"models":  map[string]any{s.Model: model, s.Model + "-fast": model},
		}},
		"small_model": "goinfer/" + s.Model + "-fast",
	}
	if a.NoTitle {
		cfg["agent"] = map[string]any{"title": map[string]any{"disable": true}}
	}
	if a.KenMCP != "" {
		cfg["mcp"] = map[string]any{"ken": map[string]any{
			"type":        "local",
			"command":     []string{a.KenMCP},
			"enabled":     true,
			"environment": KenEnv(repo, a.KenModel),
		}}
	}
	return json.MarshalIndent(cfg, "", "  ")
}

// KenEnv is ken-mcp's environment for a task repository, shared by the warm-up and by the ken-mcp opencode starts so
// both see the same index: confined to the repository, the model directory explicit, no auto-fetch, no usage
// records, and the snapshot persisted under the repository's .ken/ (ken-mcp's default, stated).
func KenEnv(repo, model string) map[string]string {
	repo = resolved(repo)
	return map[string]string{
		"KEN_MCP_DEFAULT_REPO":       repo,
		"KEN_MCP_ALLOWED_REPO_ROOTS": repo,
		"KEN_MCP_AUTO_FETCH":         "0",
		"KEN_MCP_MODEL_DIR":          model,
		"KEN_NO_USAGE_STATS":         "1",
		"KEN_MCP_SNAPSHOT":           "1",
	}
}

// Result is one run of opencode.
type Result struct {
	Arm      string  `json:"arm"`
	WallSecs float64 `json:"wall_secs"`
	CapHit   bool    `json:"cap_hit"`
	Exit     int     `json:"exit"` // opencode's exit status; -1 when killed at the cap
	Err      string  `json:"err,omitempty"`
	// Void says why the run measured nothing: the setup failed before the model started (A1's preflight, the disk
	// floor), or the host slept through part of it (Capture.Slept). A void run is neither a pass nor a fail: every
	// count leaves it out, its Pass is false and its capped time 0, and an opencode run voided after it ran keeps its
	// oracle results only as evidence.
	Void string `json:"void,omitempty"`
}

// RunOpencode runs `opencode run --format json --auto` once in repo with the statement, under cap. opencode's config,
// data and state live in out/xdg, fresh per run, so no setting of the owner's leaks in; its JSON events go to
// out/events.jsonl and its stderr to out/stderr.log. At the cap its whole process group is killed.
func RunOpencode(ctx context.Context, opencode string, s Server, a Arm, repo, statement, out string, cap time.Duration) Result {
	r := Result{Arm: a.Name, Exit: -1}
	cfg, err := Config(s, a, repo)
	if err == nil {
		err = os.WriteFile(filepath.Join(repo, "opencode.json"), cfg, 0o600)
	}
	if err == nil {
		err = os.MkdirAll(out, 0o755)
	}
	if err != nil {
		r.Err = err.Error()
		return r
	}
	_ = os.WriteFile(filepath.Join(out, "opencode.json"), cfg, 0o600)
	xdg := filepath.Join(out, "xdg")
	for _, d := range []string{"config", "data", "state"} {
		_ = os.MkdirAll(filepath.Join(xdg, d), 0o755)
	}
	events, err1 := os.Create(filepath.Join(out, "events.jsonl"))
	stderr, err2 := os.Create(filepath.Join(out, "stderr.log"))
	if err := errors.Join(err1, err2); err != nil {
		r.Err = err.Error()
		return r
	}
	defer events.Close()
	defer stderr.Close()
	cmd := exec.Command(opencode, "run", "--format", "json", "--auto", "-m", "goinfer/"+s.Model, "--dir", repo, statement)
	cmd.Dir = repo
	cmd.Stdin = nil
	cmd.Stdout, cmd.Stderr = events, stderr
	cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+filepath.Join(xdg, "config"),
		"XDG_DATA_HOME="+filepath.Join(xdg, "data"), "XDG_STATE_HOME="+filepath.Join(xdg, "state"))
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	start := time.Now()
	if err := cmd.Start(); err != nil {
		r.Err = err.Error()
		return r
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(cap)
	defer timer.Stop()
	select {
	case err = <-done:
	case <-timer.C:
		r.CapHit = true
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		err = <-done
	case <-ctx.Done():
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		err = <-done
		r.Err = ctx.Err().Error()
	}
	r.WallSecs = time.Since(start).Seconds()
	var ee *exec.ExitError
	switch {
	case r.CapHit:
	case err == nil:
		r.Exit = 0
	case errors.As(err, &ee):
		r.Exit = ee.ExitCode()
	default:
		r.Err = err.Error()
	}
	return r
}

// resolved is path with symlinks resolved, or path itself if that fails: ken-mcp is confined to the repository by
// path, and the directory it runs in reports the resolved form (macOS's /var is /private/var).
func resolved(path string) string {
	if r, err := filepath.EvalSymlinks(path); err == nil {
		return r
	}
	return path
}

// notGraded is what the graded diff leaves out: ken's index and opencode's config are the harness's files, not the
// agent's change (pilot pre-registration, amendment).
var notGraded = []string{":(exclude).ken", ":(exclude)opencode.json"}

// Diff is the repository's change against base (a commit, not HEAD: the agent may commit its own work), untracked
// files included, without the harness's files even if the agent staged or committed them: the diff the run record
// grades.
func Diff(repo, base string) string {
	_ = exec.Command("git", append([]string{"-C", repo, "add", "-A", "--", "."}, notGraded...)...).Run()
	out, _ := exec.Command("git", append([]string{"-C", repo, "diff", "--cached", base, "--", "."}, notGraded...)...).Output()
	return string(out)
}
