package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/townsendmerino/tack/internal/server"
)

// recently_changed (DESIGN §6.5; docs/design/p1-notes.md, "Decisions"): the workspace's uncommitted changes and its
// recent commits with the files each touched, from git run directly, never through a shell.
//
// git reads the repository's own config, which the workspace's content controls, so every run turns off what that
// config could make git execute:
//   - the fsmonitor hook;
//   - external diff drivers and textconv;
//   - hooks and the pager;
//   - signature checks, which run gpg.program;
//   - the clean, smudge and process commands of every filter driver the config names. git status hashes a stat-dirty
//     file through its clean filter. The cost: a stat-dirty file under a filter (git-lfs's, for one) can show as
//     modified when it is not.
//
// The environment's GIT_* variables are dropped, so the run sees only the workspace's repository, and LC_ALL=C keeps
// git's messages in English.

const (
	recentDefault  = 10
	recentMax      = 30
	recentMaxLines = 200 // the status's lines are cut here, and the log's lines too, each on its own count
)

type recentTool struct{}

func (recentTool) ReadOnly() bool { return true }

func (recentTool) Spec() server.Tool {
	return server.Tool{Name: "recently_changed",
		Description: "Uncommitted changes and the last commits (default 10, max 30), each with the files it changed.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"commits":{"type":"integer"}}}`)}
}

func (recentTool) Run(ctx context.Context, ws *Workspace, input json.RawMessage) (Result, error) {
	var in struct {
		Commits *int `json:"commits"`
	}
	if len(input) > 0 {
		if err := json.Unmarshal(input, &in); err != nil {
			return errorf("bad input: %v", err), nil
		}
	}
	n := recentDefault
	if in.Commits != nil {
		n = *in.Commits
	}
	if n < 1 || n > recentMax {
		return errorf("commits must be 1 to %d", recentMax), nil
	}
	root := ws.Root()
	g, err := newGit(ctx, root)
	if err != nil {
		return errorf("this workspace is not a git repository, so there is no history; use glob and grep instead"), nil
	}
	prefix, err := g.run(ctx, "rev-parse", "--show-prefix")
	if err != nil {
		return errorf("this workspace is not a git repository, so there is no history; use glob and grep instead"), nil
	}
	prefix = strings.TrimSpace(prefix)
	status, err := g.run(ctx, "status", "--porcelain=v1", "-z", "--untracked-files=normal", "--", ".")
	if err != nil {
		return Result{}, err
	}
	var b strings.Builder
	b.WriteString("Uncommitted changes (git status):\n")
	entries := statusEntries(status, prefix)
	if len(entries) == 0 {
		b.WriteString("(none)\n")
	}
	for i, e := range entries {
		if i == recentMaxLines {
			b.WriteString("[… more; narrow with git status in bash if you have it]\n")
			break
		}
		b.WriteString(e + "\n")
	}
	fmt.Fprintf(&b, "\nLast %d commits (git log), newest first:\n", n)
	log := ""
	if _, err := g.run(ctx, "rev-parse", "--verify", "--quiet", "HEAD"); err == nil { // a repository with no commits has no HEAD yet
		if log, err = g.run(ctx, "log", "-n", fmt.Sprint(n), "--relative", "--date=short", "--format=%h %ad %s",
			"--name-status", "--no-ext-diff", "--no-textconv", "--no-show-signature", "--no-color", "--", "."); err != nil {
			return Result{}, err
		}
	}
	if strings.TrimSpace(log) == "" {
		b.WriteString("(none)\n")
	}
	lines := 0
	for l := range strings.SplitSeq(strings.TrimRight(log, "\n"), "\n") {
		if l == "" {
			continue
		}
		if lines == recentMaxLines {
			b.WriteString("[… more; ask for fewer commits]\n")
			break
		}
		lines++
		if strings.Contains(l, "\t") { // a file line: status, tab, path
			l = "  " + l
		}
		b.WriteString(l + "\n")
	}
	return Result{Text: strings.TrimRight(b.String(), "\n")}, nil
}

// statusEntries is git status --porcelain=v1 -z's entries, one line each, with paths from the workspace root (prefix
// stripped from every path a rename or copy names) and a rename shown as "old -> new".
func statusEntries(z, prefix string) []string {
	parts := strings.Split(strings.TrimSuffix(z, "\x00"), "\x00")
	var out []string
	for i := 0; i < len(parts); i++ {
		p := parts[i]
		if len(p) < 4 {
			continue
		}
		code, path := p[:3], strings.TrimPrefix(p[3:], prefix)
		if (p[0] == 'R' || p[0] == 'C') && i+1 < len(parts) { // -z gives a rename's new path, then its old one
			i++
			path = strings.TrimPrefix(parts[i], prefix) + " -> " + path
		}
		out = append(out, code+path)
	}
	return out
}

// gitRun runs git in one workspace with the repository's executable config turned off (the package comment's list).
type gitRun struct {
	root string
	args []string // the -c overrides, the filters' included
}

// newGit reads the names of the filter drivers git's config defines, so each run can blank their commands.
func newGit(ctx context.Context, root string) (*gitRun, error) {
	g := &gitRun{root: root, args: []string{"-c", "core.fsmonitor=false", "-c", "core.untrackedCache=false", "-c", "diff.external=",
		"-c", "core.hooksPath=/dev/null", "-c", "log.showSignature=false"}}
	list, err := g.exec(ctx, "config", "--null", "--get-regexp", `^filter\.`)
	if err != nil && !strings.Contains(err.Error(), "exit status 1") { // 1: no such keys
		return nil, err
	}
	seen := map[string]bool{}
	for kv := range strings.SplitSeq(list, "\x00") {
		key, _, _ := strings.Cut(kv, "\n")
		name, ok := strings.CutPrefix(key, "filter.")
		if !ok {
			continue
		}
		if dot := strings.LastIndex(name, "."); dot > 0 {
			name = name[:dot]
		}
		if !seen[name] {
			seen[name] = true
			g.args = append(g.args, "-c", "filter."+name+".clean=", "-c", "filter."+name+".smudge=",
				"-c", "filter."+name+".process=", "-c", "filter."+name+".required=false")
		}
	}
	return g, nil
}

func (g *gitRun) run(ctx context.Context, args ...string) (string, error) {
	return g.exec(ctx, append(append([]string(nil), g.args...), args...)...)
}

// exec runs git in the root with args, the environment's GIT_* variables dropped and the C locale, and returns stdout.
func (g *gitRun) exec(ctx context.Context, args ...string) (string, error) {
	full := append([]string{"-C", g.root, "--no-pager", "--no-optional-locks"}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "GIT_") && !strings.HasPrefix(e, "LC_") && !strings.HasPrefix(e, "LANG") {
			cmd.Env = append(cmd.Env, e)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}
