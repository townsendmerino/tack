package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
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
// session, writing its output to the writer it is given. After each, the attempt is verified with what a user has at
// the base: its change must build and pass the tests of the packages it changed (verifyChange), and it must not have
// moved HEAD or deleted a test file. The verifier never sees the task's grader.
//
// The first verified attempt is kept. Before another attempt the tree is reset to the commit it started at, index
// included. With no verified attempt, the last attempt that changed anything is kept, restored if a later attempt
// left the tree empty. Only the kept attempt's output reaches stdout; the others' sessions stay in tack's store. ws must
// be a clean git tree, so the resets lose nothing of the owner's. It returns the kept attempt's exit code.
func bestOf(ctx context.Context, k int, ws string, edit func(string, sandbox.Target) (core.EditKit, error),
	attempt func(n int, out io.Writer) int, stdout, stderr io.Writer) int {
	if dirty, err := gitOut(ws, "status", "--porcelain"); err != nil || dirty != "" {
		fmt.Fprintf(stderr, "tack run: best_of needs a clean git workspace (git status: %q, %v)\n", dirty, err)
		return exitError
	}
	base, err := gitOut(ws, "rev-parse", "HEAD")
	if err != nil {
		fmt.Fprintln(stderr, "tack run: best_of:", err)
		return exitError
	}
	type kept struct {
		out  *bytes.Buffer
		code int
		snap snapshot
	}
	var last, lastChanged *kept
	for n := 1; n <= k; n++ {
		out := &bytes.Buffer{}
		code := attempt(n, out)
		ch, err := changes(ws)
		ok, why := false, ""
		switch head, herr := gitOut(ws, "rev-parse", "HEAD"); {
		case herr != nil || head != base:
			why = "the attempt moved HEAD"
		case err != nil:
			why = "reading the change: " + err.Error()
		case len(goChanges(ch)) == 0:
			why = "no Go file changed"
		default:
			ok, why = verifyChange(ctx, edit, ws, goChanges(ch))
		}
		verdict := "fails"
		if ok {
			verdict = "passes"
		}
		fmt.Fprintf(stderr, "tack run: best_of attempt %d of %d %s verification: %s\n", n, k, verdict, why)
		if ok {
			stdout.Write(out.Bytes())
			return code
		}
		last = &kept{out: out, code: code}
		if len(ch) > 0 && why != "the attempt moved HEAD" {
			snap, err := takeSnapshot(ws, ch)
			if err != nil {
				fmt.Fprintln(stderr, "tack run: best_of: keeping the attempt's change:", err)
				return exitError
			}
			lastChanged = &kept{out: out, code: code, snap: snap}
		}
		if err := resetTree(ws, base); err != nil {
			fmt.Fprintln(stderr, "tack run: best_of: resetting the workspace:", err)
			return exitError
		}
	}
	// No attempt verified: keep the last one that changed something.
	k0 := last
	if lastChanged != nil {
		k0 = lastChanged
		if err := lastChanged.snap.apply(ws); err != nil {
			fmt.Fprintln(stderr, "tack run: best_of: restoring the kept attempt's change:", err)
			return exitError
		}
	}
	stdout.Write(k0.out.Bytes())
	return k0.code
}

// change is one path git status reports changed against HEAD, untracked ones included, relative to ws.
type change struct {
	path    string
	deleted bool
}

// changes is the workspace's changes against HEAD, from git status -z: a rename is its new path, changed, and its old
// path, deleted.
func changes(ws string) ([]change, error) {
	out, err := exec.Command("git", "-C", ws, "status", "--porcelain=v1", "-z", "--untracked-files=all").Output()
	if err != nil {
		return nil, fmt.Errorf("git status: %w", err)
	}
	var cs []change
	fields := strings.Split(string(out), "\x00")
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if len(f) < 4 {
			continue
		}
		xy, p := f[:2], f[3:]
		cs = append(cs, change{path: p, deleted: strings.Contains(xy, "D")})
		if xy[0] == 'R' || xy[0] == 'C' { // the original path follows
			i++
			if xy[0] == 'R' && i < len(fields) {
				cs = append(cs, change{path: fields[i], deleted: true})
			}
		}
	}
	return cs, nil
}

func goChanges(cs []change) []change {
	var out []change
	for _, c := range cs {
		if strings.HasSuffix(c.path, ".go") {
			out = append(out, c)
		}
	}
	return out
}

// verifyChange is best_of's verifier, through a fresh edit kit's sandboxed shell: the module builds, and the tests of
// the packages changed (those still present) pass. Deleting a test file never verifies.
func verifyChange(ctx context.Context, edit func(string, sandbox.Target) (core.EditKit, error), ws string, changed []change) (bool, string) {
	pkgs := map[string]bool{}
	for _, c := range changed {
		if c.deleted && strings.HasSuffix(c.path, "_test.go") {
			return false, "the attempt deletes the test file " + c.path
		}
		dir := path.Dir(c.path)
		if st, err := os.Stat(filepath.Join(ws, filepath.FromSlash(dir))); err == nil && st.IsDir() {
			pkgs["./"+dir] = true
		}
	}
	command := "go build -o /dev/null ./..."
	if len(pkgs) > 0 {
		var list []string
		for p := range pkgs {
			list = append(list, p)
		}
		sort.Strings(list)
		command += " && go test -count=1 " + strings.Join(list, " ")
	}
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
		return false, command + ": stopped: " + res.Stopped
	case res.Exit != 0:
		return false, fmt.Sprintf("%s: exit %d", command, res.Exit)
	}
	return true, command
}

// snapshot is an attempt's changed files: each path's content, or nil for a deletion.
type snapshot map[string][]byte

func takeSnapshot(ws string, cs []change) (snapshot, error) {
	s := snapshot{}
	for _, c := range cs {
		if c.deleted {
			s[c.path] = nil
			continue
		}
		b, err := os.ReadFile(filepath.Join(ws, filepath.FromSlash(c.path)))
		if err != nil {
			return nil, err
		}
		s[c.path] = b
	}
	return s, nil
}

// apply writes the snapshot over a tree reset to its base.
func (s snapshot) apply(ws string) error {
	for p, b := range s {
		abs := filepath.Join(ws, filepath.FromSlash(p))
		if b == nil {
			if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(abs, b, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// resetTree puts ws back to commit base: HEAD, index and tracked files reset, untracked files (not ignored ones) removed.
func resetTree(ws, base string) error {
	if _, err := gitOut(ws, "reset", "-q", "--hard", base); err != nil {
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
