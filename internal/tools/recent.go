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
// recent commits with the files each touched, from git run directly, never through a shell. git reads the repository's
// own config, so every run turns off what that config could make git execute: the fsmonitor hook, external diff
// drivers and textconv filters, and the pager. The environment's GIT_* variables are dropped, so the run sees only the
// workspace's repository.

const (
	recentDefault  = 10
	recentMax      = 30
	recentMaxLines = 200 // the status's lines, and then the log's, are cut here
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
	prefix, err := git(ctx, root, "rev-parse", "--show-prefix")
	if err != nil {
		return errorf("this workspace is not a git repository, so there is no history; use glob and grep instead"), nil
	}
	prefix = strings.TrimSpace(prefix)
	status, err := git(ctx, root, "status", "--porcelain=v1", "--untracked-files=normal", "--", ".")
	if err != nil {
		return Result{}, err
	}
	log, err := git(ctx, root, "log", "-n", fmt.Sprint(n), "--relative", "--date=short", "--format=%h %ad %s",
		"--name-status", "--no-ext-diff", "--no-textconv", "--no-color", "--", ".")
	if err != nil && !strings.Contains(err.Error(), "does not have any commits") {
		return Result{}, err
	}
	var b strings.Builder
	b.WriteString("Uncommitted changes (git status):\n")
	lines := 0
	if strings.TrimSpace(status) == "" {
		b.WriteString("(none)\n")
	}
	for l := range strings.SplitSeq(strings.TrimRight(status, "\n"), "\n") {
		if l == "" {
			continue
		}
		if lines++; lines > recentMaxLines {
			b.WriteString("[… more; narrow with git status in bash if you have it]\n")
			break
		}
		// Porcelain paths are from the repository's top; the tool's are from the workspace root.
		b.WriteString(l[:3] + strings.TrimPrefix(l[3:], prefix) + "\n")
	}
	fmt.Fprintf(&b, "\nLast %d commits (git log), newest first:\n", n)
	if strings.TrimSpace(log) == "" {
		b.WriteString("(none)\n")
	}
	for l := range strings.SplitSeq(strings.TrimRight(log, "\n"), "\n") {
		if l == "" {
			continue
		}
		if lines++; lines > 2*recentMaxLines {
			b.WriteString("[… more; ask for fewer commits]\n")
			break
		}
		if strings.Contains(l, "\t") { // a file line: status, tab, path
			l = "  " + l
		}
		b.WriteString(l + "\n")
	}
	return Result{Text: strings.TrimRight(b.String(), "\n")}, nil
}

// git runs one git command in root with the repository's executable config turned off, and returns its stdout.
func git(ctx context.Context, root string, args ...string) (string, error) {
	full := append([]string{"-C", root, "--no-pager", "--no-optional-locks", "-c", "core.fsmonitor=false",
		"-c", "core.untrackedCache=false", "-c", "diff.external=", "-c", "core.hooksPath=/dev/null"}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "GIT_") {
			cmd.Env = append(cmd.Env, e)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_TERMINAL_PROMPT=0")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %v: %s", args[0], err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}
