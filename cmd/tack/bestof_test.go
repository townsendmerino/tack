package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/core"
	"github.com/townsendmerino/tack/internal/sandbox"
	"github.com/townsendmerino/tack/internal/shell"
)

// verdictShell passes the verification when pkg/a.go holds "good", and records each command it was given.
type verdictShell struct {
	ws       string
	commands *[]string
}

func (s verdictShell) Run(_ context.Context, command string, _ time.Duration) (shell.Result, error) {
	*s.commands = append(*s.commands, command)
	b, _ := os.ReadFile(filepath.Join(s.ws, "pkg", "a.go"))
	if strings.Contains(string(b), "good") {
		return shell.Result{Exit: 0}, nil
	}
	return shell.Result{Exit: 1, Output: "FAIL"}, nil
}

func bestOfRepo(t *testing.T) string {
	t.Helper()
	ws := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", ws}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	os.MkdirAll(filepath.Join(ws, "pkg"), 0o755)
	os.WriteFile(filepath.Join(ws, "pkg", "a.go"), []byte("package pkg // base\n"), 0o644)
	run("init", "-q")
	run("add", "pkg/a.go")
	run("commit", "-q", "-m", "base")
	return ws
}

// bestOf (§15.3): a failed attempt's change, tracked and untracked, is undone before the next; the first verified
// attempt is kept and ends the attempts; the verifier builds and tests the changed packages; with no verified attempt
// the last change stands; a dirty workspace is refused before any attempt.
func TestBestOf(t *testing.T) {
	write := func(ws, body string) {
		os.WriteFile(filepath.Join(ws, "pkg", "a.go"), []byte("package pkg // "+body+"\n"), 0o644)
		os.WriteFile(filepath.Join(ws, "pkg", body+"_extra.go"), []byte("package pkg\n"), 0o644)
	}
	kit := func(ws string, cmds *[]string) func(string, sandbox.Target) (core.EditKit, error) {
		return func(string, sandbox.Target) (core.EditKit, error) {
			return core.EditKit{Shell: verdictShell{ws: ws, commands: cmds}}, nil
		}
	}

	t.Run("the second attempt is kept", func(t *testing.T) {
		ws := bestOfRepo(t)
		var cmds []string
		var n int
		code := bestOf(context.Background(), 3, ws, kit(ws, &cmds), func(i int) int {
			n = i
			write(ws, map[int]string{1: "bad", 2: "good"}[i])
			return 10 + i
		}, io.Discard)
		b, _ := os.ReadFile(filepath.Join(ws, "pkg", "a.go"))
		_, badExtra := os.Stat(filepath.Join(ws, "pkg", "bad_extra.go"))
		if n != 2 || code != 12 || !strings.Contains(string(b), "good") || badExtra == nil {
			t.Fatalf("attempts %d, code %d, a.go %q, bad_extra.go still there: %v", n, code, b, badExtra == nil)
		}
		if len(cmds) != 2 || cmds[0] != "go build ./... && go test -count=1 ./pkg" {
			t.Fatalf("verification commands %q", cmds)
		}
	})

	t.Run("no attempt passes: the last change stands", func(t *testing.T) {
		ws := bestOfRepo(t)
		var cmds []string
		var n int
		bestOf(context.Background(), 2, ws, kit(ws, &cmds), func(i int) int {
			n = i
			write(ws, map[int]string{1: "bad", 2: "worse"}[i])
			return 0
		}, io.Discard)
		b, _ := os.ReadFile(filepath.Join(ws, "pkg", "a.go"))
		if n != 2 || !strings.Contains(string(b), "worse") {
			t.Fatalf("attempts %d, a.go %q", n, b)
		}
	})

	t.Run("an attempt that changes nothing fails without running the verifier", func(t *testing.T) {
		ws := bestOfRepo(t)
		var cmds []string
		var n int
		bestOf(context.Background(), 2, ws, kit(ws, &cmds), func(i int) int { n = i; return 0 }, io.Discard)
		if n != 2 || len(cmds) != 0 {
			t.Fatalf("attempts %d, verifications %q", n, cmds)
		}
	})

	t.Run("a dirty workspace is refused", func(t *testing.T) {
		ws := bestOfRepo(t)
		write(ws, "owner")
		var cmds []string
		called := false
		code := bestOf(context.Background(), 3, ws, kit(ws, &cmds), func(int) int { called = true; return 0 }, io.Discard)
		if called || code != exitError {
			t.Fatalf("an attempt ran (%v) or the code is %d", called, code)
		}
	})
}
