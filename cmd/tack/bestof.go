package main

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/townsendmerino/tack/internal/core"
	"github.com/townsendmerino/tack/internal/perms"
	"github.com/townsendmerino/tack/internal/sandbox"
	"github.com/townsendmerino/tack/internal/shell"
)

// verifyTimeout bounds best_of's verification command.
const verifyTimeout = 5 * time.Minute

// bestOf is [run] best_of (calibration §15.3): up to k attempts at an edit task in workspace ws, each attempt's own
// session. After each, the attempt is verified with what a user has at the base: its change must build (go build ./...)
// and pass the tests of the packages it changed (go test), run through an edit kit's sandboxed shell. The first
// verified attempt is kept; before another attempt, the tree is restored to where it started (git checkout, git
// clean). With no verified attempt, the last one's change is kept. The verifier never sees the task's grader. ws must
// be a clean git tree, so the restore loses nothing of the owner's. It returns the kept attempt's exit code.
func bestOf(ctx context.Context, k int, ws string, edit func(string, sandbox.Target) (core.EditKit, error),
	attempt func(n int) int, stderr io.Writer) int {
	if dirty, err := gitOut(ws, "status", "--porcelain"); err != nil || dirty != "" {
		fmt.Fprintf(stderr, "tack run: best_of needs a clean git workspace (git status: %q, %v)\n", dirty, err)
		return exitError
	}
	code := exitError
	for n := 1; n <= k; n++ {
		code = attempt(n)
		changed, err := changedGoFiles(ws)
		ok, why := false, ""
		switch {
		case err != nil:
			why = "reading the change: " + err.Error()
		case len(changed) == 0:
			why = "no Go file changed"
		default:
			ok, why = verifyChange(ctx, edit, ws, changed)
		}
		verdict := "fails"
		if ok {
			verdict = "passes"
		}
		fmt.Fprintf(stderr, "tack run: best_of attempt %d of %d %s verification: %s\n", n, k, verdict, why)
		if ok || n == k {
			return code
		}
		if err := restoreTree(ws); err != nil {
			fmt.Fprintln(stderr, "tack run: best_of: restoring the workspace:", err)
			return exitError
		}
	}
	return code
}

// changedGoFiles is the .go files the workspace changed against HEAD, untracked ones included, relative to ws.
func changedGoFiles(ws string) ([]string, error) {
	out, err := gitOut(ws, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	var files []string
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 4 {
			continue
		}
		f := strings.TrimSpace(line[3:])
		if i := strings.LastIndex(f, " -> "); i >= 0 {
			f = f[i+4:]
		}
		if strings.HasSuffix(f, ".go") {
			files = append(files, f)
		}
	}
	return files, nil
}

// verifyChange builds the module and tests the packages of changed, through a fresh edit kit's sandboxed shell.
func verifyChange(ctx context.Context, edit func(string, sandbox.Target) (core.EditKit, error), ws string, changed []string) (bool, string) {
	pkgs := map[string]bool{}
	for _, f := range changed {
		pkgs["./"+path.Dir(f)] = true
	}
	var list []string
	for p := range pkgs {
		list = append(list, p)
	}
	sort.Strings(list)
	command := "go build ./... && go test -count=1 " + strings.Join(list, " ")
	kit, err := edit(ws, sandbox.Target{Mark: sandbox.NewMark("v"), Since: time.Now()})
	if err != nil {
		return false, err.Error()
	}
	if kit.Close != nil {
		defer kit.Close()
	}
	res, err := kit.Shell.Run(shell.WithApproval(ctx, perms.ByMode), command, verifyTimeout)
	switch {
	case err != nil:
		return false, command + ": " + err.Error()
	case res.Stopped != "":
		return false, command + ": " + res.Stopped
	case res.Exit != 0:
		return false, fmt.Sprintf("%s: exit %d", command, res.Exit)
	}
	return true, command
}

// restoreTree puts ws back to HEAD: tracked files restored, untracked ones (not ignored ones) removed.
func restoreTree(ws string) error {
	if _, err := gitOut(ws, "checkout", "--", "."); err != nil {
		return err
	}
	_, err := gitOut(ws, "clean", "-fdq")
	return err
}

func gitOut(ws string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", ws}, args...)...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimRight(string(out), "\n"), nil
}
