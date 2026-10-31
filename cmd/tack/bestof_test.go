package main

import (
	"bytes"
	"context"
	"fmt"
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

// verdictShell passes the verification when pkg/a.go holds "good" (or stops it when it holds "hang"), and records each
// command it was given.
type verdictShell struct {
	ws       string
	commands *[]string
}

func (s verdictShell) Run(_ context.Context, command string, _ time.Duration) (shell.Result, error) {
	*s.commands = append(*s.commands, command)
	b, _ := os.ReadFile(filepath.Join(s.ws, "pkg", "a.go"))
	switch {
	case strings.Contains(string(b), "hang"):
		return shell.Result{Exit: -1, Stopped: "timeout"}, nil
	case strings.Contains(string(b), "good"):
		return shell.Result{Exit: 0}, nil
	}
	return shell.Result{Exit: 1, Output: "FAIL"}, nil
}

func gitIn(t *testing.T, ws string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", ws}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func bestOfRepo(t *testing.T) string {
	t.Helper()
	ws := t.TempDir()
	os.MkdirAll(filepath.Join(ws, "pkg"), 0o755)
	os.MkdirAll(filepath.Join(ws, "pkg2"), 0o755)
	os.WriteFile(filepath.Join(ws, "pkg", "a.go"), []byte("package pkg // base\n"), 0o644)
	os.WriteFile(filepath.Join(ws, "pkg", "a_test.go"), []byte("package pkg\n"), 0o644)
	os.WriteFile(filepath.Join(ws, "pkg2", "b.go"), []byte("package pkg2\n"), 0o644)
	gitIn(t, ws, "init", "-q")
	gitIn(t, ws, "add", "-A")
	gitIn(t, ws, "commit", "-q", "-m", "base")
	return ws
}

func setA(ws, body string) {
	os.WriteFile(filepath.Join(ws, "pkg", "a.go"), []byte("package pkg // "+body+"\n"), 0o644)
}

func readA(ws string) string {
	b, _ := os.ReadFile(filepath.Join(ws, "pkg", "a.go"))
	return string(b)
}

func shellKit(ws string, cmds *[]string) func(string, sandbox.Target) (core.EditKit, error) {
	return func(string, sandbox.Target) (core.EditKit, error) {
		return core.EditKit{Shell: verdictShell{ws: ws, commands: cmds}}, nil
	}
}

// bestOf (§15.3). Each case names the behavior it checks.
func TestBestOf(t *testing.T) {
	t.Run("the first verified attempt is kept, and only its output reaches stdout", func(t *testing.T) {
		ws := bestOfRepo(t)
		var cmds []string
		var stdout bytes.Buffer
		n := 0
		code := bestOf(context.Background(), 3, ws, shellKit(ws, &cmds), func(i int, out io.Writer) int {
			n = i
			fmt.Fprintf(out, "attempt %d\n", i)
			setA(ws, map[int]string{1: "bad", 2: "good"}[i])
			os.WriteFile(filepath.Join(ws, "pkg", fmt.Sprintf("extra%d.go", i)), []byte("package pkg\n"), 0o644)
			return 10 + i
		}, &stdout, io.Discard)
		_, extra1 := os.Stat(filepath.Join(ws, "pkg", "extra1.go"))
		if n != 2 || code != 12 || !strings.Contains(readA(ws), "good") || extra1 == nil || stdout.String() != "attempt 2\n" {
			t.Fatalf("attempts %d, code %d, a.go %q, extra1.go kept %v, stdout %q", n, code, readA(ws), extra1 == nil, stdout.String())
		}
		if len(cmds) != 2 || cmds[0] != "go build -o /dev/null ./... && go test -count=1 ./pkg" {
			t.Fatalf("verification commands %q", cmds)
		}
	})

	t.Run("with none verified, an earlier change is restored when a later attempt changes nothing", func(t *testing.T) {
		ws := bestOfRepo(t)
		var cmds []string
		var stdout bytes.Buffer
		code := bestOf(context.Background(), 3, ws, shellKit(ws, &cmds), func(i int, out io.Writer) int {
			fmt.Fprintf(out, "attempt %d\n", i)
			if i == 2 {
				setA(ws, "worse")
			}
			return 20 + i
		}, &stdout, io.Discard)
		if !strings.Contains(readA(ws), "worse") || code != 22 || stdout.String() != "attempt 2\n" {
			t.Fatalf("a.go %q, code %d, stdout %q: want attempt 2's change and output", readA(ws), code, stdout.String())
		}
	})

	t.Run("the reset clears a staged change, and an attempt that commits fails and is undone", func(t *testing.T) {
		ws := bestOfRepo(t)
		base := gitIn(t, ws, "rev-parse", "HEAD")
		var cmds []string
		var staged, committedHead string
		bestOf(context.Background(), 3, ws, shellKit(ws, &cmds), func(i int, out io.Writer) int {
			switch i {
			case 1:
				setA(ws, "staged")
				gitIn(t, ws, "add", "pkg/a.go")
			case 2:
				staged = gitIn(t, ws, "diff", "--cached", "--name-only") + "|" + readA(ws)
				setA(ws, "good")
				gitIn(t, ws, "commit", "-q", "-am", "the model commits")
				committedHead = gitIn(t, ws, "rev-parse", "HEAD")
				setA(ws, "good, uncommitted") // would verify, but HEAD moved
			}
			return 0
		}, io.Discard, io.Discard)
		if staged != "|package pkg // base\n" {
			t.Fatalf("attempt 2 started from %q, want an empty index and the base file", staged)
		}
		if committedHead == base || gitIn(t, ws, "rev-parse", "HEAD") != base {
			t.Fatalf("HEAD is %s after the attempts, want the base %s", gitIn(t, ws, "rev-parse", "HEAD"), base)
		}
		if len(cmds) != 1 { // attempt 1 verified (and failed); attempt 2 moved HEAD, so it never verified; attempt 3 changed nothing
			t.Fatalf("verifications %q", cmds)
		}
	})

	t.Run("deleting a test file never verifies", func(t *testing.T) {
		ws := bestOfRepo(t)
		var cmds []string
		code := bestOf(context.Background(), 1+1, ws, shellKit(ws, &cmds), func(i int, out io.Writer) int {
			setA(ws, "good")
			os.Remove(filepath.Join(ws, "pkg", "a_test.go"))
			return 30 + i
		}, io.Discard, io.Discard)
		if len(cmds) != 0 || code != 32 {
			t.Fatalf("verifications %q, code %d: deleting a_test.go must fail without the verifier", cmds, code)
		}
	})

	t.Run("a stopped verifier fails the attempt", func(t *testing.T) {
		ws := bestOfRepo(t)
		var cmds []string
		n := 0
		bestOf(context.Background(), 2, ws, shellKit(ws, &cmds), func(i int, out io.Writer) int {
			n = i
			setA(ws, "hang")
			return 0
		}, io.Discard, io.Discard)
		if n != 2 {
			t.Fatalf("a stopped verification counted as a pass: %d attempts", n)
		}
	})

	t.Run("a dirty workspace is refused and left as it was", func(t *testing.T) {
		ws := bestOfRepo(t)
		setA(ws, "owner")
		var cmds []string
		called := false
		code := bestOf(context.Background(), 3, ws, shellKit(ws, &cmds), func(int, io.Writer) int { called = true; return 0 }, io.Discard, io.Discard)
		if called || code != exitError || !strings.Contains(readA(ws), "owner") {
			t.Fatalf("an attempt ran (%v), code %d, a.go %q", called, code, readA(ws))
		}
	})
}

// changes reads git status: a modified and an untracked file are changed, a deletion is deleted, and a rename is its
// new path changed and its old path deleted.
func TestBestOfChanges(t *testing.T) {
	ws := bestOfRepo(t)
	setA(ws, "x")
	os.WriteFile(filepath.Join(ws, "pkg2", "new.go"), []byte("package pkg2\n"), 0o644)
	gitIn(t, ws, "mv", "pkg2/b.go", "pkg2/c.go")
	os.Remove(filepath.Join(ws, "pkg", "a_test.go"))
	cs, err := changes(ws)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, c := range cs {
		got[c.path] = c.deleted
	}
	want := map[string]bool{"pkg/a.go": false, "pkg/a_test.go": true, "pkg2/c.go": false, "pkg2/b.go": true, "pkg2/new.go": false}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("changes %v, want %v", got, want)
	}
}

// verifyChange tests every changed package still present, once, in order, and builds without writing a binary.
func TestBestOfVerifiesEachChangedPackage(t *testing.T) {
	ws := bestOfRepo(t)
	setA(ws, "good")
	var cmds []string
	ok, _ := verifyChange(context.Background(), shellKit(ws, &cmds), ws,
		[]change{{path: "pkg2/b.go"}, {path: "pkg/a.go"}, {path: "pkg/z.go"}, {path: "gone/x.go", deleted: true}})
	if !ok || len(cmds) != 1 || cmds[0] != "go build -o /dev/null ./... && go test -count=1 ./pkg ./pkg2" {
		t.Fatalf("ok %v, commands %q", ok, cmds)
	}
}
