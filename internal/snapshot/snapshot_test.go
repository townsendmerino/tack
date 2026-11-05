package snapshot

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

// entry is one thing in a tree, as the tests compare it: type and permissions, and the bytes or the link's target.
type entry struct {
	mode os.FileMode
	data string
}

// capture is everything under root except .git, by relative path.
func capture(t *testing.T, root string) map[string]entry {
	t.Helper()
	out := map[string]entry{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if rel == "." {
			return nil
		}
		if rel == ".git" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		e := entry{mode: info.Mode()}
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			e.data, err = os.Readlink(p)
		case info.Mode().IsRegular():
			var b []byte
			b, err = os.ReadFile(p)
			e.data = string(b)
		}
		out[rel] = e
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func sameTree(t *testing.T, got, want map[string]entry) {
	t.Helper()
	for p, w := range want {
		g, ok := got[p]
		switch {
		case !ok:
			t.Errorf("%q is missing", p)
		case g.mode != w.mode:
			t.Errorf("%q: mode %v, want %v", p, g.mode, w.mode)
		case g.data != w.data:
			t.Errorf("%q: %d bytes differ from %d", p, len(g.data), len(w.data))
		}
	}
	for p := range got {
		if _, ok := want[p]; !ok {
			t.Errorf("%q should not be there", p)
		}
	}
}

func run(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	c := exec.Command(name, args...)
	c.Dir = dir
	c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
	return string(out)
}

func write(t *testing.T, root, rel, content string, mode os.FileMode) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
}

// repo is a git repository with one commit and a working tree holding what a snapshot must bring back: changed and
// untracked and ignored files, modes, a symlink, an empty directory, line endings a filter would touch, and names that
// need care.
func repo(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "w")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, root, "git", "init", "-q")
	write(t, root, ".gitignore", "*.log\n.env\nbuild/\n", 0o644)
	write(t, root, ".gitattributes", "*.txt text eol=crlf\n", 0o644)
	write(t, root, "src/a.go", "package a\n", 0o644)
	write(t, root, "run.sh", "#!/bin/sh\n", 0o755)
	write(t, root, "mixed.txt", "unix\nwindows\r\n", 0o644)
	run(t, root, "git", "add", "-A")
	run(t, root, "git", "commit", "-qm", "one")
	write(t, root, "src/a.go", "package a\n\nvar uncommitted = 1\n", 0o644)
	write(t, root, "src/staged.go", "package a // staged\n", 0o644)
	run(t, root, "git", "add", "src/staged.go")
	write(t, root, "untracked.go", "package a // new\n", 0o644)
	write(t, root, "day.log", "ignored\n", 0o644)
	write(t, root, ".env", "SECRET=1\n", 0o600)
	write(t, root, "build/out.bin", "\x00\x01\xff\xfe binary\r\n", 0o755)
	write(t, root, "name with space\nand newline.txt", "odd name\n", 0o644)
	write(t, root, "é-nfd-é.txt", "accents\n", 0o644)
	if err := os.Symlink("src/a.go", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/no/such/place", filepath.Join(root, "dangling")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "empty/inner"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func shadow(t *testing.T, root string) *Shadow {
	t.Helper()
	return New(t.TempDir(), root, 1<<30)
}

func take(t *testing.T, s *Shadow) string {
	t.Helper()
	id, why := s.Take(context.Background())
	if why != "" || id == "" {
		t.Fatalf("no snapshot: %q %q", id, why)
	}
	return id
}

// The destruction is real, and the restore is compared byte for byte, modes and symlinks and empty directories too.
func TestRewindAfterARealDestroy(t *testing.T) {
	for _, c := range []struct {
		name string
		do   [][]string
	}{
		{"git reset --hard", [][]string{{"git", "reset", "--hard", "-q"}}},
		{"rm -r", [][]string{{"rm", "-r", "src", "build", "empty"}}},
		{"git clean -fdx", [][]string{{"git", "clean", "-fdxq"}}},
		{"all three", [][]string{{"git", "reset", "--hard", "-q"}, {"git", "clean", "-fdxq"}, {"rm", "-r", "src"}}},
		{"overwritten", [][]string{{"sh", "-c", "echo gone > src/a.go; chmod 644 .env; chmod 644 run.sh; rm link; ln -s run.sh link; rm -r empty; mkdir -p new/dir; echo x > new/dir/f"}}},
		{"a file where a directory was and the reverse", [][]string{{"sh", "-c", "rm -r src; echo file > src; rm run.sh; mkdir run.sh; echo x > run.sh/f; rm -r empty; echo y > empty"}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := repo(t)
			s := shadow(t, root)
			before := capture(t, root)
			id := take(t, s)
			for _, do := range c.do {
				run(t, root, do[0], do[1:]...)
			}
			after := capture(t, root)
			undo, _, err := s.Restore(context.Background(), id, false)
			if err != nil {
				t.Fatal(err)
			}
			sameTree(t, capture(t, root), before)

			// The rewind can itself be undone.
			if undo == "" {
				t.Fatal("no snapshot of the tree it replaced")
			}
			if _, _, err := s.Restore(context.Background(), undo, false); err != nil {
				t.Fatal(err)
			}
			sameTree(t, capture(t, root), after)
		})
	}
}

// A file the restore didn't have to change keeps its timestamp, so a rewind doesn't rebuild the world.
func TestRestoreLeavesUnchangedFilesAlone(t *testing.T) {
	root := repo(t)
	s := shadow(t, root)
	id := take(t, s)
	old := time.Unix(1_000_000_000, 0)
	if err := os.Chtimes(filepath.Join(root, "run.sh"), old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "untracked.go")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Restore(context.Background(), id, false); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(root, "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !st.ModTime().Equal(old) {
		t.Errorf("run.sh was rewritten: %v", st.ModTime())
	}
}

// Changes says what a restore would do, without doing it.
func TestChanges(t *testing.T) {
	root := repo(t)
	s := shadow(t, root)
	id := take(t, s)
	write(t, root, "src/a.go", "changed\n", 0o644)
	write(t, root, "added.go", "x\n", 0o644)
	if err := os.Remove(filepath.Join(root, "day.log")); err != nil {
		t.Fatal(err)
	}
	before := capture(t, root)
	got, err := s.Changes(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	sameTree(t, capture(t, root), before)
	want := []Change{{"added.go", Remove}, {"day.log", Bring}, {"src/a.go", Bring}}
	if !slices.Equal(got, want) {
		t.Errorf("changes %v, want %v", got, want)
	}

	// What git's tree doesn't hold counts too: a changed mode, an empty directory gone, a new one.
	root = repo(t)
	s = shadow(t, root)
	id = take(t, s)
	if err := os.Chmod(filepath.Join(root, ".env"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, "empty")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "fresh"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(root, "day.log"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "build", "out.bin")); err != nil { // build is empty now, and that's not a change of its own
		t.Fatal(err)
	}
	got, err = s.Changes(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	want = []Change{{".env", Bring}, {"build/out.bin", Bring}, {"day.log", Bring}, {"empty/inner", Bring}, {"fresh", Remove}}
	if !slices.Equal(got, want) {
		t.Errorf("changes %v, want %v", got, want)
	}
}

// A read-only directory, in the checkpoint or made since, doesn't stop a restore, and comes back read-only.
func TestRestoreThroughReadOnlyDirectories(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes anywhere")
	}
	root := repo(t)
	write(t, root, "locked/kept.txt", "kept\n", 0o644)
	if err := os.Chmod(filepath.Join(root, "locked"), 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err == nil && d.IsDir() {
				os.Chmod(p, 0o755)
			}
			return nil
		})
	})
	s := shadow(t, root)
	before := capture(t, root)
	id := take(t, s)
	// A file changed inside it, and a new read-only directory holding a file that must go.
	if err := os.Chmod(filepath.Join(root, "locked"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, root, "locked/kept.txt", "changed\n", 0o644)
	if err := os.Chmod(filepath.Join(root, "locked"), 0o555); err != nil {
		t.Fatal(err)
	}
	write(t, root, "newlocked/gone.txt", "gone\n", 0o644)
	if err := os.Chmod(filepath.Join(root, "newlocked"), 0o555); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Restore(context.Background(), id, false); err != nil {
		t.Fatal(err)
	}
	sameTree(t, capture(t, root), before)
}

// A restore that fails partway says so and still hands back the way to undo it.
func TestAFailedRestoreKeepsItsUndo(t *testing.T) {
	root := repo(t)
	s := shadow(t, root)
	id := take(t, s)
	write(t, root, "untracked.go", "later\n", 0o644)
	if err := os.Remove(filepath.Join(root, "day.log")); err != nil {
		t.Fatal(err)
	}
	after := capture(t, root)
	realGit := s.Git
	failing := filepath.Join(t.TempDir(), "git")
	script := "#!/bin/sh\ncase \" $* \" in *\" checkout-index \"*) exit 1 ;; esac\nexec " + realGit + " \"$@\"\n"
	if err := os.WriteFile(failing, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	s.Git = failing
	undo, _, err := s.Restore(context.Background(), id, false)
	if err == nil || errors.Is(err, ErrUntouched) || undo == "" {
		t.Fatalf("undo %q, err %v", undo, err)
	}
	s.Git = realGit
	if _, _, err := s.Restore(context.Background(), undo, false); err != nil {
		t.Fatal(err)
	}
	sameTree(t, capture(t, root), after)
}

// digest is every file under the user's .git, by path.
func digest(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.WalkDir(filepath.Join(root, ".git"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(p)
		fmt.Fprintf(&b, "%s %x\n", p, sha256.Sum256(data))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// Neither taking nor restoring writes a byte under the user's .git, and the shadow is outside it.
func TestTheUsersGitIsUntouched(t *testing.T) {
	root := repo(t)
	s := shadow(t, root)
	before := digest(t, root)
	status := run(t, root, "git", "status", "--porcelain")
	id := take(t, s)
	s.Staged(context.Background())
	write(t, root, "src/a.go", "changed\n", 0o644)
	if _, _, err := s.Restore(context.Background(), id, false); err != nil {
		t.Fatal(err)
	}
	if got := digest(t, root); got != before {
		t.Errorf(".git changed:\n%s\nwant\n%s", got, before)
	}
	if got := run(t, root, "git", "status", "--porcelain"); got != status {
		t.Errorf("status %q, want %q", got, status)
	}
	if strings.HasPrefix(s.Dir, root) {
		t.Errorf("shadow %s is inside the workspace", s.Dir)
	}
}

// Nothing in the workspace runs, and no filter changes a byte: not the workspace's attributes and config, and not the
// owner's global config.
func TestNothingInTheWorkspaceRuns(t *testing.T) {
	root := repo(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	t.Setenv("GIT_DIR", filepath.Join(root, ".git")) // a caller's git variables reach nothing
	t.Setenv("GIT_INDEX_FILE", filepath.Join(root, ".git", "index"))
	pwned := filepath.Join(t.TempDir(), "pwned")
	write(t, filepath.Dir(pwned), "pwned.sh", "#!/bin/sh\ntouch "+pwned+"\n", 0o755) // so that fsmonitor would leave a mark if it ran
	cmd := "touch " + pwned + "; cat"
	write(t, root, ".gitattributes", "* filter=evil text eol=crlf ident\n", 0o644)
	config := "[filter \"evil\"]\n\tclean = " + cmd + "\n\tsmudge = " + cmd + "\n\trequired = true\n[core]\n\tfsmonitor = " + pwned + ".sh\n\thooksPath = hooks\n\tautocrlf = true\n"
	f, err := os.OpenFile(filepath.Join(root, ".git", "config"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(config)
	f.Close()
	write(t, home, ".gitconfig", config, 0o644)
	write(t, root, "hooks/post-index-change", "#!/bin/sh\ntouch "+pwned+"\n", 0o755)
	write(t, root, "ident.txt", "$Id$ unix\nwindows\r\n", 0o644)

	s := shadow(t, root)
	before := capture(t, root)
	id := take(t, s)
	s.Staged(context.Background())
	write(t, root, "ident.txt", "gone\n", 0o644)
	if _, _, err := s.Restore(context.Background(), id, false); err != nil {
		t.Fatal(err)
	}
	sameTree(t, capture(t, root), before)
	if _, err := os.Stat(pwned); err == nil {
		t.Error("a filter or hook from the workspace ran")
	}
}

// Each way to have no snapshot says why, and takes nothing.
func TestNoSnapshot(t *testing.T) {
	big := func(t *testing.T, root string) *Shadow {
		write(t, root, "day.log", strings.Repeat("x", 4096), 0o644) // ignored, and it counts
		return New(t.TempDir(), root, 1024)
	}
	for _, c := range []struct {
		name  string
		setup func(t *testing.T, root string) *Shadow
		want  string
	}{
		{"over the cap", big, "over"},
		{"not a repository", func(t *testing.T, root string) *Shadow {
			if err := os.RemoveAll(filepath.Join(root, ".git")); err != nil {
				t.Fatal(err)
			}
			return shadow(t, root)
		}, "not the top of a git repository"},
		{"inside a repository", func(t *testing.T, root string) *Shadow {
			return shadow(t, filepath.Join(root, "src"))
		}, "not the top of a git repository"},
		{"a nested repository", func(t *testing.T, root string) *Shadow {
			run(t, filepath.Join(root, "src"), "git", "init", "-q")
			return shadow(t, root)
		}, "nested"},
		{"a nested worktree's .git file", func(t *testing.T, root string) *Shadow {
			write(t, root, "src/sub/.git", "gitdir: /elsewhere\n", 0o644)
			return shadow(t, root)
		}, "nested"},
		{"no git", func(t *testing.T, root string) *Shadow {
			s := shadow(t, root)
			s.Git = filepath.Join(root, "no-such-git")
			return s
		}, "git"},
		{"a file git can't read", func(t *testing.T, root string) *Shadow {
			write(t, root, "locked.go", "x", 0o000)
			t.Cleanup(func() { os.Chmod(filepath.Join(root, "locked.go"), 0o644) })
			return shadow(t, root)
		}, "git"},
		{"no workspace", func(t *testing.T, root string) *Shadow {
			return shadow(t, filepath.Join(root, "no-such-dir"))
		}, "not the top of a git repository"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.name == "a file git can't read" && os.Geteuid() == 0 {
				t.Skip("root reads anything")
			}
			s := c.setup(t, repo(t))
			id, why := s.Take(context.Background())
			if id != "" || !strings.Contains(why, c.want) {
				t.Errorf("id %q, why %q, want %q", id, why, c.want)
			}
		})
	}
}

// At the cap is under it.
func TestCapIsInclusive(t *testing.T) {
	root := t.TempDir()
	run(t, root, "git", "init", "-q")
	write(t, root, "a", "12345", 0o644)
	write(t, root, "b", "67890", 0o644)
	if id, why := New(t.TempDir(), root, 10).Take(context.Background()); id == "" {
		t.Errorf("at the cap: %q", why)
	}
	if id, _ := New(t.TempDir(), root, 9).Take(context.Background()); id != "" {
		t.Errorf("over the cap: %q", id)
	}
}

// A tree that hasn't changed is the same snapshot; a changed one isn't, in its bytes, its mode, its empty directories.
func TestSnapshotIdentity(t *testing.T) {
	root := repo(t)
	s := shadow(t, root)
	now := time.Unix(2_000_000_000, 0)
	s.Now = func() time.Time { now = now.Add(time.Minute); return now } // or an unchanged tree's commit would be the same anyway
	a := take(t, s)
	if b := take(t, s); b != a {
		t.Errorf("unchanged tree: %s, then %s", a, b)
	}
	for _, change := range []func(){
		func() { write(t, root, "untracked.go", "changed", 0o644) },
		func() { os.Chmod(filepath.Join(root, ".env"), 0o640) },
		func() { os.MkdirAll(filepath.Join(root, "another/empty"), 0o755) },
		func() { os.Chmod(filepath.Join(root, "run.sh"), 0o644) },
	} {
		change()
		b := take(t, s)
		if b == a {
			t.Errorf("a changed tree kept %s", a)
		}
		a = b
	}
}

// A lock left by a git that died doesn't stop snapshots for good, and a fresh one isn't taken from whoever holds it.
func TestAStaleLockGoes(t *testing.T) {
	root := repo(t)
	s := shadow(t, root)
	take(t, s)
	lock := filepath.Join(s.Dir, "index.lock")
	write(t, s.Dir, "index.lock", "", 0o644)
	if id, _ := s.Take(context.Background()); id != "" {
		t.Errorf("took a snapshot over a fresh lock: %s", id)
	}
	old := time.Now().Add(-2 * Timeout)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	take(t, s)
}

// One shadow per workspace, in the state directory.
func TestShadowPlace(t *testing.T) {
	state := t.TempDir()
	a, b := New(state, "/w/a", 1), New(state, "/w/b", 1)
	if a.Dir == b.Dir || !strings.HasPrefix(a.Dir, state) || New(state, "/w/a/", 1).Dir != a.Dir {
		t.Errorf("%s %s", a.Dir, b.Dir)
	}
}

// Old checkpoints go, and the oldest go when the shadow is over twice the cap; a rewind to one says it's gone, and a kept one still works.
func TestPrune(t *testing.T) {
	root := repo(t)
	s := shadow(t, root)
	now := time.Unix(2_000_000_000, 0)
	s.Now = func() time.Time { return now }
	old := take(t, s)
	write(t, root, "untracked.go", "second\n", 0o644)
	now = now.Add(MaxAge + time.Hour)
	kept := take(t, s)
	want := capture(t, root)
	if err := s.Prune(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Restore(context.Background(), old, false); err == nil || !strings.Contains(err.Error(), "gone") {
		t.Errorf("an old checkpoint: %v", err)
	}
	if _, err := s.Changes(context.Background(), old); err == nil || !strings.Contains(err.Error(), "gone") {
		t.Errorf("an old checkpoint's changes: %v", err)
	}
	write(t, root, "untracked.go", "third\n", 0o644)
	if _, _, err := s.Restore(context.Background(), kept, false); err != nil {
		t.Fatal(err)
	}
	sameTree(t, capture(t, root), want)

	// Over twice the cap: only the newest stays.
	s = New(t.TempDir(), root, 1)
	s.MaxBytes = 1 << 30
	first := take(t, s)
	write(t, root, "untracked.go", strings.Repeat("incompressible? ", 1000), 0o644)
	now2 := time.Now().Add(time.Second)
	s.Now = func() time.Time { return now2 }
	second := take(t, s)
	s.MaxBytes = 1
	if err := s.Prune(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.MaxBytes = 1 << 30
	if _, err := s.Changes(context.Background(), first); err == nil {
		t.Error("the oldest stayed")
	}
	if _, err := s.Changes(context.Background(), second); err != nil {
		t.Errorf("the newest went: %v", err)
	}
	// What was dropped is off the disk, not just out of the list.
	if _, err := s.git(context.Background(), nil, nil, "cat-file", "-e", first); err == nil {
		t.Error("the dropped checkpoint's commit is still there")
	}

	// Newest means by date, not by where its id sorts.
	s = shadow(t, root)
	var ids []string
	when := time.Unix(2_000_000_000, 0)
	s.Now = func() time.Time { return when }
	for i := range 8 {
		write(t, root, "untracked.go", fmt.Sprintf("version %d\n", i), 0o644)
		when = when.Add(time.Minute)
		ids = append(ids, take(t, s))
	}
	when = when.Add(MaxAge + time.Hour)
	if err := s.Prune(context.Background()); err != nil {
		t.Fatal(err)
	}
	for i, id := range ids {
		if _, err := s.Changes(context.Background(), id); (err == nil) != (i == len(ids)-1) {
			t.Errorf("checkpoint %d of %d: %v", i, len(ids), err)
		}
	}
}

// The owner's global config doesn't reach the shadow: not as a filter only, but at all.
func TestTheOwnersConfigStaysOut(t *testing.T) {
	root := repo(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	write(t, home, ".gitconfig", "[tack]\n\tmarker = 1\n", 0o644)
	s := shadow(t, root)
	take(t, s)
	if out, err := s.git(context.Background(), nil, nil, "config", "tack.marker"); err == nil {
		t.Errorf("the shadow read the owner's config: %s", out)
	}
}

// A commit that isn't a checkpoint, and a name that isn't one, are refused without touching the tree.
func TestRestoreOfNothing(t *testing.T) {
	root := repo(t)
	s := shadow(t, root)
	take(t, s)
	before := capture(t, root)
	for _, id := range []string{"", "HEAD", "--all", strings.Repeat("0", 40), "../../etc"} {
		if _, _, err := s.Restore(context.Background(), id, false); !errors.Is(err, ErrUntouched) {
			t.Errorf("%q: %v", id, err)
		}
	}
	sameTree(t, capture(t, root), before)

	// A commit still in the shadow whose checkpoint was dropped is gone too.
	id := take(t, s)
	if _, err := s.git(context.Background(), nil, nil, "update-ref", "-d", "refs/tack/"+id); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Restore(context.Background(), id, false); err == nil || !strings.Contains(err.Error(), "gone") {
		t.Errorf("a dropped checkpoint: %v", err)
	}
}

// A symlink in the tree is a link: the walk doesn't follow it out of the workspace, for the cap or for a restore.
func TestSymlinksStayLinks(t *testing.T) {
	root := repo(t)
	outside := t.TempDir()
	write(t, outside, "huge", strings.Repeat("x", 8192), 0o644)
	if err := os.Symlink(outside, filepath.Join(root, "out")); err != nil {
		t.Fatal(err)
	}
	s := New(t.TempDir(), root, 1<<20)
	id := take(t, s)
	before := capture(t, root)
	// The link is replaced by a directory, and restored over it, with nothing written outside.
	if err := os.Remove(filepath.Join(root, "out")); err != nil {
		t.Fatal(err)
	}
	write(t, root, "out/huge", "inside", 0o644)
	if _, _, err := s.Restore(context.Background(), id, false); err != nil {
		t.Fatal(err)
	}
	sameTree(t, capture(t, root), before)
	if b, _ := os.ReadFile(filepath.Join(outside, "huge")); len(b) != 8192 {
		t.Errorf("the file outside changed: %d bytes", len(b))
	}
}

// A Take that ran out of its own time isn't tried again before every later command, at the same cost each time. The caller's
// deadline or cancel says nothing about the next Take.
func TestATakeThatRanOutOfTimeStaysOff(t *testing.T) {
	root := repo(t)
	s := shadow(t, root)
	s.Limit = time.Nanosecond
	id, why := s.Take(context.Background())
	if id != "" || !strings.Contains(why, "longer than") {
		t.Errorf("id %q why %q", id, why)
	}
	s.Limit = time.Minute
	if id, again := s.Take(context.Background()); id != "" || again != why {
		t.Errorf("then id %q why %q", id, again)
	}

	s = shadow(t, root)
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if id, why := s.Take(ctx); id != "" || strings.Contains(why, "longer than") {
		t.Errorf("the caller's deadline: id %q why %q", id, why)
	}
	take(t, s)

	s = shadow(t, root)
	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	if id, _ := s.Take(ctx); id != "" {
		t.Errorf("cancelled: %q", id)
	}
	take(t, s)
}

// Prune keeps the newest checkpoint, which may share its second with another whose id sorts first.
func TestPruneKeepsTheNewestOfOneSecond(t *testing.T) {
	root := repo(t)
	when := time.Unix(2_000_000_000, 0)
	for i := range 12 {
		s := shadow(t, root)
		s.Now = func() time.Time { return when }
		write(t, root, "untracked.go", fmt.Sprintf("a %d\n", i), 0o644)
		take(t, s)
		write(t, root, "untracked.go", fmt.Sprintf("b %d\n", i), 0o644)
		newest := take(t, s)
		s.Now = func() time.Time { return when.Add(MaxAge + time.Hour) }
		if err := s.Prune(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := s.known(context.Background(), newest); err != nil {
			t.Fatalf("round %d: %v", i, err)
		}
	}
}

// A .git in any case, which a case-insensitive filesystem takes for the real one, is never in a snapshot or touched by a restore.
func TestDotGitInAnyCase(t *testing.T) {
	root := repo(t)
	write(t, root, "src/.GIT/config", "x", 0o644)
	if id, why := shadow(t, root).Take(context.Background()); id != "" || !strings.Contains(why, "nested") {
		t.Errorf("id %q why %q", id, why)
	}
}

// The walk of a large tree stops when its time is up, rather than running on after the caller has gone.
func TestSurveyStopsWithItsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := shadow(t, repo(t)).survey(ctx); err == nil {
		t.Error("it walked on")
	}
}

// A checkpoint ages from when a command last relied on it, not from when it was made: a tree unchanged for longer than MaxAge
// gets its old checkpoint back, and the command after it can still be rewound.
func TestAReusedCheckpointIsKept(t *testing.T) {
	root := repo(t)
	s := shadow(t, root)
	when := time.Unix(2_000_000_000, 0)
	s.Now = func() time.Time { return when }
	old := take(t, s)
	before := capture(t, root)

	when = when.Add(MaxAge + time.Hour) // a new session on the unchanged tree
	if err := s.Prune(context.Background()); err != nil {
		t.Fatal(err)
	}
	if id := take(t, s); id != old {
		t.Fatalf("unchanged tree: %s, then %s", old, id)
	}
	run(t, root, "git", "reset", "--hard", "-q") // the command it was allowed on
	take(t, s)                                   // and the one after

	when = when.Add(time.Hour) // the next session
	if err := s.Prune(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Restore(context.Background(), old, false); err != nil {
		t.Fatal(err)
	}
	sameTree(t, capture(t, root), before)

	// Last used longer than MaxAge ago, it goes as any other does.
	write(t, root, "untracked.go", "changed\n", 0o644)
	take(t, s)
	when = when.Add(MaxAge + time.Hour)
	if err := s.Prune(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.known(context.Background(), old); err == nil {
		t.Error("an old checkpoint stayed")
	}
}

// What stops a new snapshot doesn't stop looking at or going back to an old one. There's just nothing to undo the rewind with.
func TestRewindAfterWhatStopsASnapshot(t *testing.T) {
	for _, c := range []struct {
		name string
		do   func(t *testing.T, root string, s *Shadow)
		want []Change
	}{
		{"over the cap", func(t *testing.T, root string, s *Shadow) {
			write(t, root, "huge.bin", strings.Repeat("x", 4096), 0o644)
			s.MaxBytes = 1024
		}, []Change{{"huge.bin", Remove}}},
		{"a nested repository", func(t *testing.T, root string, s *Shadow) {
			if err := os.Mkdir(filepath.Join(root, "clone"), 0o755); err != nil {
				t.Fatal(err)
			}
			run(t, filepath.Join(root, "clone"), "git", "init", "-q")
		}, []Change{{"clone", Remove}}},
		{"a nested repository in a directory that stays", func(t *testing.T, root string, s *Shadow) {
			run(t, filepath.Join(root, "src"), "git", "init", "-q")
		}, []Change{{"src/.git", Remove}}},
		{".git gone", func(t *testing.T, root string, s *Shadow) {
			if err := os.RemoveAll(filepath.Join(root, ".git")); err != nil {
				t.Fatal(err)
			}
			write(t, root, "new.go", "x\n", 0o644)
		}, []Change{{"new.go", Remove}}},
		{"a directory no one can read", func(t *testing.T, root string, s *Shadow) {
			write(t, root, "locked/x", "x\n", 0o644)
			if err := os.Chmod(filepath.Join(root, "locked"), 0o000); err != nil {
				t.Fatal(err)
			}
		}, []Change{{"locked", Remove}}},
		{"a directory that stays that no one can read", func(t *testing.T, root string, s *Shadow) {
			write(t, root, "src/later.go", "x\n", 0o644)
			if err := os.Chmod(filepath.Join(root, "src"), 0o000); err != nil {
				t.Fatal(err)
			}
		}, []Change{{"src", Bring}, {"src/later.go", Remove}}},
		{"a file no one can read", func(t *testing.T, root string, s *Shadow) {
			write(t, root, "untracked.go", "x\n", 0o000)
		}, []Change{{"untracked.go", Bring}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if os.Geteuid() == 0 {
				t.Skip("root reads anything")
			}
			root := repo(t)
			t.Cleanup(func() {
				os.Chmod(filepath.Join(root, "locked"), 0o755)
				os.Chmod(filepath.Join(root, "src"), 0o755)
			})
			s := shadow(t, root)
			before := capture(t, root)
			id := take(t, s)
			c.do(t, root, s)
			refs, _ := s.git(context.Background(), nil, nil, "for-each-ref", "refs/tack/")

			got, err := s.Changes(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, c.want) {
				t.Errorf("changes %v, want %v", got, c.want)
			}
			if after, _ := s.git(context.Background(), nil, nil, "for-each-ref", "refs/tack/"); string(after) != string(refs) {
				t.Errorf("looking took a snapshot:\n%s\nthen\n%s", refs, after)
			}
			if st, err := os.Lstat(filepath.Join(root, "src")); c.name == "a directory that stays that no one can read" && (err != nil || st.Mode().Perm() != 0) {
				t.Errorf("looking left src as %v %v", st, err)
			}
			if why := s.NoUndo(context.Background()); why == "" {
				t.Error("it says the rewind can be undone")
			}

			// Without being told it's on purpose, it refuses and changes nothing.
			if _, _, err := s.Restore(context.Background(), id, false); !errors.Is(err, ErrUntouched) || !errors.Is(err, ErrNoUndo) {
				t.Errorf("no undo: %v", err)
			}
			again, err := s.Changes(context.Background(), id)
			if err != nil || !slices.Equal(again, got) {
				t.Errorf("after the refusal: %v %v, want %v", again, err, got)
			}

			undo, why, err := s.Restore(context.Background(), id, true)
			if err != nil {
				t.Fatal(err)
			}
			if undo != "" || why == "" {
				t.Errorf("undo %q why %q", undo, why)
			}
			sameTree(t, capture(t, root), before)
		})
	}
}

// Changes says which way each path goes, whatever git's tree doesn't hold.
func TestChangesOfWhatTheTreeDoesntHold(t *testing.T) {
	root := repo(t)
	write(t, root, "tiny", "t", 0o644)
	s := shadow(t, root)
	id := take(t, s)
	// A new directory with its own mode and nothing in it but another: it goes, as one thing.
	if err := os.Mkdir(filepath.Join(root, "a"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	// An empty directory replaced by a file: the file goes and the directory comes back.
	if err := os.Remove(filepath.Join(root, "empty", "inner")); err != nil {
		t.Fatal(err)
	}
	write(t, root, "empty/inner", "file\n", 0o644)
	// A directory's own mode.
	if err := os.Chmod(filepath.Join(root, "src"), 0o700); err != nil {
		t.Fatal(err)
	}
	// A link holding what a file held is still a change.
	if err := os.Remove(filepath.Join(root, "tiny")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("t", filepath.Join(root, "tiny")); err != nil {
		t.Fatal(err)
	}
	got, err := s.Changes(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	want := []Change{{"a", Remove}, {"empty/inner", Bring}, {"empty/inner", Remove}, {"src", Bring}, {"tiny", Bring}}
	if !slices.Equal(got, want) {
		t.Errorf("changes %v, want %v", got, want)
	}
	if _, _, err := s.Restore(context.Background(), id, false); err != nil {
		t.Fatal(err)
	}
	for p, wantMode := range map[string]os.FileMode{"src": 0o755, "empty/inner": 0o755} {
		st, err := os.Stat(filepath.Join(root, p))
		if err != nil || !st.IsDir() || st.Mode().Perm() != wantMode {
			t.Errorf("%s: %v %v", p, st, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "a")); err == nil {
		t.Error("a stayed")
	}
}

// Staged is the index against HEAD: not the working tree, and what it can't tell counts as yes.
func TestStaged(t *testing.T) {
	ctx := context.Background()
	root := repo(t) // repo stages src/staged.go
	s := shadow(t, root)
	if !s.Staged(ctx) {
		t.Error("a staged file isn't staged")
	}
	run(t, root, "git", "reset", "-q", "--", "src/staged.go")
	if s.Staged(ctx) { // src/a.go has changes too, unstaged, and they don't count
		t.Error("nothing staged, and it says something is")
	}
	write(t, root, "src/a.go", "staged edit\n", 0o644)
	run(t, root, "git", "add", "src/a.go")
	write(t, root, "src/a.go", "further edit\n", 0o644)
	if !s.Staged(ctx) {
		t.Error("staged and then edited further isn't staged")
	}
	// Nothing committed yet.
	unborn := t.TempDir()
	run(t, unborn, "git", "init", "-q")
	if !shadow(t, unborn).Staged(ctx) {
		t.Error("a repository with no HEAD: it can't tell, and says no")
	}
	// No repository, no git.
	if !shadow(t, t.TempDir()).Staged(ctx) {
		t.Error("no repository: it can't tell, and says no")
	}
	s = shadow(t, root)
	s.Git = filepath.Join(root, "no-such-git")
	if !s.Staged(ctx) {
		t.Error("no git: it can't tell, and says no")
	}
	// The caller's git variables reach nothing.
	clean := repo(t)
	run(t, clean, "git", "reset", "-q")
	t.Setenv("GIT_INDEX_FILE", filepath.Join(root, ".git", "index"))
	if shadow(t, clean).Staged(ctx) {
		t.Error("it read another index")
	}
}

// A file or directory no one can read, with what it holds unchanged, isn't rewritten: only its mode goes back.
func TestUnreadableButUnchanged(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads anything")
	}
	root := repo(t)
	s := shadow(t, root)
	before := capture(t, root)
	id := take(t, s)
	old := time.Unix(1_000_000_000, 0)
	for _, p := range []string{"day.log", "src/a.go"} {
		if err := os.Chtimes(filepath.Join(root, p), old, old); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(filepath.Join(root, "day.log"), 0o000); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(root, "src"), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(filepath.Join(root, "src"), 0o755) })
	got, err := s.Changes(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if want := []Change{{"day.log", Bring}, {"src", Bring}}; !slices.Equal(got, want) {
		t.Errorf("changes %v, want %v", got, want)
	}
	if st, err := os.Lstat(filepath.Join(root, "day.log")); err != nil || st.Mode().Perm() != 0 {
		t.Errorf("looking left day.log as %v %v", st, err)
	}
	if _, _, err := s.Restore(context.Background(), id, true); err != nil {
		t.Fatal(err)
	}
	sameTree(t, capture(t, root), before)
	for _, p := range []string{"day.log", "src/a.go"} {
		if st, err := os.Stat(filepath.Join(root, p)); err != nil || !st.ModTime().Equal(old) {
			t.Errorf("%s was rewritten: %v %v", p, st, err)
		}
	}
}

// Going back without a way to undo it is a choice when a rule stops the undo snapshot. Running out of time isn't: it refuses, with
// the tree untouched.
func TestRestoreRefusesWhenTheUndoRanOutOfTime(t *testing.T) {
	root := repo(t)
	s := shadow(t, root)
	id := take(t, s)
	write(t, root, "untracked.go", "later\n", 0o644)
	after := capture(t, root)
	s.gaveUp = "a snapshot took longer than 1m0s"
	if _, _, err := s.Restore(context.Background(), id, false); !errors.Is(err, ErrUntouched) || !errors.Is(err, ErrNoUndo) {
		t.Errorf("err %v", err)
	}
	sameTree(t, capture(t, root), after)
	// On purpose, a tree too big to snapshot can still go back.
	if undo, why, err := s.Restore(context.Background(), id, true); err != nil || undo != "" || why == "" {
		t.Errorf("on purpose: %q %q %v", undo, why, err)
	}
}

// git failing on the undo snapshot is no different from a rule stopping it.
func TestRestoreRefusesWhenGitFailsTheUndo(t *testing.T) {
	root := repo(t)
	s := shadow(t, root)
	id := take(t, s)
	write(t, root, "untracked.go", "later\n", 0o644)
	after := capture(t, root)
	write(t, s.Dir, "index.lock", "", 0o644) // a fresh lock, so git add fails
	if _, _, err := s.Restore(context.Background(), id, false); !errors.Is(err, ErrUntouched) || !errors.Is(err, ErrNoUndo) {
		t.Errorf("err %v", err)
	}
	sameTree(t, capture(t, root), after)
}

// Going back leaves the restored checkpoint as the one Take remembers: an unchanged tree gets it again and it counts as used.
func TestRestoreRemembersItsCheckpoint(t *testing.T) {
	root := repo(t)
	s := shadow(t, root)
	now := time.Unix(2_000_000_000, 0)
	s.Now = func() time.Time { now = now.Add(time.Second); return now }
	a := take(t, s)
	write(t, root, "untracked.go", "later\n", 0o644)
	take(t, s)
	if _, _, err := s.Restore(context.Background(), a, false); err != nil {
		t.Fatal(err)
	}
	refs, _ := s.git(context.Background(), nil, nil, "for-each-ref", "refs/tack/")
	if id := take(t, s); id != a {
		t.Errorf("the restored tree: %s, want %s", id, a)
	}
	if after, _ := s.git(context.Background(), nil, nil, "for-each-ref", "refs/tack/"); string(after) != string(refs) {
		t.Errorf("a duplicate checkpoint:\n%s\nthen\n%s", refs, after)
	}
}

// Least recently used goes first, whatever the order they were made in.
func TestPruneOrdersByLastUse(t *testing.T) {
	root := repo(t)
	s := shadow(t, root)
	now := time.Unix(2_000_000_000, 0)
	s.Now = func() time.Time { now = now.Add(time.Second); return now }
	var ids []string
	for _, v := range []string{"a", "b", "c", "d"} {
		write(t, root, "untracked.go", v+"\n", 0o644)
		ids = append(ids, take(t, s))
	}
	if _, _, err := s.Restore(context.Background(), ids[0], false); err != nil { // uses a, and d as its undo
		t.Fatal(err)
	}
	write(t, root, "untracked.go", "e\n", 0o644)
	e := take(t, s)
	got, err := s.byLastUse(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{e, ids[0], ids[3], ids[2], ids[1]}
	for i := range want {
		want[i] = "refs/tack/" + want[i]
	}
	if !slices.Equal(got, want) {
		t.Errorf("order %q, want %q", got, want)
	}
	// Past the size limit, what's left is what was used last.
	s.MaxBytes = 1
	if err := s.Prune(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.MaxBytes = 1 << 30
	if err := s.known(context.Background(), e); err != nil {
		t.Errorf("the last used went: %v", err)
	}
	if err := s.known(context.Background(), ids[1]); err == nil {
		t.Error("the least recently used stayed")
	}
}

// A failure the tree isn't ready for shows up before anything is touched: not a read-only directory opened, not an empty one gone.
func TestRestoreIndexFailsBeforeTouchingAnything(t *testing.T) {
	root := repo(t)
	write(t, root, "locked/kept.txt", "kept\n", 0o644)
	if err := os.Chmod(filepath.Join(root, "locked"), 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(filepath.Join(root, "locked"), 0o755) })
	s := shadow(t, root)
	id := take(t, s)
	write(t, root, "untracked.go", "later\n", 0o644)
	if err := os.Remove(filepath.Join(root, "day.log")); err != nil {
		t.Fatal(err)
	}
	after := capture(t, root)
	if err := os.Mkdir(filepath.Join(s.Dir, "restore-index"), 0o755); err != nil { // read-tree can't write its index there
		t.Fatal(err)
	}
	if _, _, err := s.Restore(context.Background(), id, false); !errors.Is(err, ErrUntouched) {
		t.Errorf("err %v", err)
	}
	sameTree(t, capture(t, root), after)
}

// Each stage has its own time: a restore whose comparison and undo took all but the limit still finishes the rewrite. The caller's
// cancel stops it before anything changes.
func TestRestoreStopsWithItsCaller(t *testing.T) {
	root := repo(t)
	s := shadow(t, root)
	id := take(t, s)
	write(t, root, "untracked.go", "later\n", 0o644)
	after := capture(t, root)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := s.Restore(ctx, id, true); !errors.Is(err, ErrUntouched) || strings.Contains(err.Error(), "gone") {
		t.Errorf("err %v", err)
	}
	sameTree(t, capture(t, root), after)
}

// A checkpoint that can't be marked used isn't one to rely on: Take says there's none rather than handing it back.
func TestATakeThatCantMarkItsCheckpoint(t *testing.T) {
	root := repo(t)
	s := shadow(t, root)
	take(t, s)
	if err := os.RemoveAll(filepath.Join(s.Dir, "used")); err != nil {
		t.Fatal(err)
	}
	write(t, s.Dir, "used", "in the way", 0o644) // used/ can't be made
	if id, why := s.Take(context.Background()); id != "" || why == "" {
		t.Errorf("id %q why %q", id, why)
	}
}

// Two sessions on one workspace: while one is in the shadow, the other goes without a snapshot and doesn't prune under it.
func TestOneSessionInTheShadowAtATime(t *testing.T) {
	ctx := context.Background()
	root := repo(t)
	state := t.TempDir()
	a, b := New(state, root, 1<<30), New(state, root, 1<<30)
	id := take(t, a)
	release, why := a.lock()
	if why != "" {
		t.Fatal(why)
	}
	got, why := b.Take(ctx)
	if got != "" || !strings.Contains(why, "another") {
		t.Errorf("Take: %q %q", got, why)
	}
	future := time.Now().Add(2 * MaxAge)
	write(t, root, "untracked.go", "second\n", 0o644)
	release()
	second := take(t, a)
	if release, why = a.lock(); why != "" {
		t.Fatal(why)
	}
	b.Now = func() time.Time { return future }
	if err := b.Prune(ctx); err != nil {
		t.Errorf("Prune: %v", err)
	}
	if err := b.known(ctx, id); err != nil {
		t.Errorf("it pruned under the holder: %v", err)
	}
	if _, _, err := b.Restore(ctx, id, true); !errors.Is(err, ErrUntouched) || !strings.Contains(err.Error(), "another") {
		t.Errorf("Restore: %v", err)
	}
	release()
	if got, why := b.Take(ctx); got != second {
		t.Errorf("after: %q %q", got, why)
	}
}

// A workspace that isn't a repository gets no shadow at all, not an empty one per directory it's run in.
func TestNoShadowForNoRepository(t *testing.T) {
	s := shadow(t, t.TempDir())
	if id, _ := s.Take(context.Background()); id != "" {
		t.Fatalf("id %q", id)
	}
	if _, err := os.Stat(s.Dir); err == nil {
		t.Error("it made a shadow")
	}
}

// The caller going away during the undo snapshot stops the restore before it touches anything.
func TestRestoreStopsWhenTheCallerGoesDuringTheUndo(t *testing.T) {
	root := repo(t)
	s := shadow(t, root)
	id := take(t, s)
	write(t, root, "untracked.go", "later\n", 0o644)
	after := capture(t, root)
	slow := filepath.Join(t.TempDir(), "git")
	script := "#!/bin/sh\ncase \" $* \" in *\" add \"*) sleep 5 ;; esac\nexec " + s.Git + " \"$@\"\n"
	if err := os.WriteFile(slow, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	s.Git = slow
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, _, err := s.Restore(ctx, id, true); !errors.Is(err, ErrUntouched) {
		t.Errorf("err %v", err)
	}
	sameTree(t, capture(t, root), after)

	// With nothing to write back, only something to remove, the same: no read-tree is there to stop it.
	root = repo(t)
	s = shadow(t, root)
	id = take(t, s)
	write(t, root, "new.go", "later\n", 0o644)
	after = capture(t, root)
	if err := os.WriteFile(slow, []byte("#!/bin/sh\ncase \" $* \" in *\" add \"*) sleep 5 ;; esac\nexec "+New(t.TempDir(), root, 1).Git+" \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	s.Git = slow
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, _, err := s.Restore(ctx, id, true); !errors.Is(err, ErrUntouched) {
		t.Errorf("only removals: %v", err)
	}
	sameTree(t, capture(t, root), after)
}

// With no git there's no undo snapshot, and the preview says so.
func TestNoUndoWithoutGit(t *testing.T) {
	s := shadow(t, repo(t))
	s.Git = ""
	if why := s.NoUndo(context.Background()); !strings.Contains(why, "git") {
		t.Errorf("why %q", why)
	}
}

// In a linked worktree .git is a file, and every path after it still gets its mode back.
func TestRestoreInALinkedWorktree(t *testing.T) {
	main := repo(t)
	root := filepath.Join(t.TempDir(), "linked")
	run(t, main, "git", "worktree", "add", "-q", "-b", "other", root)
	write(t, root, "secret.txt", "s\n", 0o600)
	s := shadow(t, root)
	before := capture(t, root)
	id := take(t, s)
	if err := os.Chmod(filepath.Join(root, "secret.txt"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(root, "src"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Restore(context.Background(), id, false); err != nil {
		t.Fatal(err)
	}
	sameTree(t, capture(t, root), before)
	if st, err := os.Lstat(filepath.Join(root, ".git")); err != nil || st.IsDir() {
		t.Errorf(".git: %v %v", st, err)
	}
}

// A checkpoint whose ref has gone isn't handed back, whatever Take remembers.
func TestTakeChecksTheCheckpointItRemembers(t *testing.T) {
	root := repo(t)
	s := shadow(t, root)
	now := time.Unix(2_000_000_000, 0)
	s.Now = func() time.Time { now = now.Add(time.Minute); return now }
	id := take(t, s)
	if _, err := s.git(context.Background(), nil, nil, "update-ref", "-d", "refs/tack/"+id); err != nil {
		t.Fatal(err)
	}
	if err := s.collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := take(t, s)
	if err := s.known(context.Background(), got); err != nil {
		t.Errorf("it handed back %s: %v", got, err)
	}
}

func mkfifo(t *testing.T, root, rel string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(p, 0o644); err != nil {
		t.Fatal(err)
	}
}

// Sockets and FIFOs are left alone: a directory holding only them isn't removed, one that also holds a new file loses only the
// file, and the snapshot and the preview neither hang on them nor count them.
func TestSpecialFilesAreLeftAlone(t *testing.T) {
	root := repo(t)
	mkfifo(t, root, "run/app.fifo")
	s := shadow(t, root)
	before := capture(t, root)
	id := take(t, s)
	if why := s.NoUndo(context.Background()); why != "" {
		t.Errorf("NoUndo: %q", why)
	}
	if got, err := s.Changes(context.Background(), id); err != nil || len(got) != 0 {
		t.Errorf("unchanged: %v %v", got, err)
	}
	write(t, root, "run/later.txt", "x\n", 0o644)
	mkfifo(t, root, "fresh/only.fifo")
	want := []Change{{"run/later.txt", Remove}}
	if got, err := s.Changes(context.Background(), id); err != nil || !slices.Equal(got, want) {
		t.Errorf("changes %v %v, want %v", got, err, want)
	}
	if _, _, err := s.Restore(context.Background(), id, false); err != nil {
		t.Fatal(err)
	}
	want2 := capture(t, root)
	if _, ok := want2["fresh/only.fifo"]; !ok {
		t.Error("the new FIFO went")
	}
	delete(want2, "fresh/only.fifo")
	delete(want2, "fresh")
	sameTree(t, want2, before)
}

// A socket or FIFO standing where the checkpoint holds something can't be left alone and restored too: it refuses, and says which.
func TestASpecialFileInTheWay(t *testing.T) {
	for _, c := range []struct {
		name, at string
		clear    func(root string) error
	}{
		{"a file", "untracked.go", func(root string) error { return os.Remove(filepath.Join(root, "untracked.go")) }},
		{"inside a directory where a file was", "untracked.go/inner/app.fifo", func(root string) error { return os.Remove(filepath.Join(root, "untracked.go")) }},
		{"inside a directory where a link was", "link/app.fifo", func(root string) error { return os.Remove(filepath.Join(root, "link")) }},
		{"a directory", "empty/inner", func(root string) error { return os.Remove(filepath.Join(root, "empty/inner")) }},
		{"a directory with files", "src", func(root string) error { return os.RemoveAll(filepath.Join(root, "src")) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := repo(t)
			s := shadow(t, root)
			id := take(t, s)
			if err := c.clear(root); err != nil {
				t.Fatal(err)
			}
			mkfifo(t, root, c.at)
			after := capture(t, root)
			name, _, _ := strings.Cut(c.at, "/") // what stands in the way is the directory, when the FIFO is inside one
			if c.at == "empty/inner" {
				name = c.at
			}
			if _, err := s.Changes(context.Background(), id); err == nil || !strings.Contains(err.Error(), name) {
				t.Errorf("Changes: %v", err)
			}
			if _, _, err := s.Restore(context.Background(), id, true); !errors.Is(err, ErrUntouched) || !strings.Contains(err.Error(), name) {
				t.Errorf("Restore: %v", err)
			}
			sameTree(t, capture(t, root), after)
		})
	}
}

// A restore touches only what differs: an unchanged empty directory is the same directory after it, one kept for a FIFO keeps its
// mode, and a read-only directory with nothing changed under it isn't opened.
func TestRestoreTouchesOnlyWhatDiffers(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes anywhere")
	}
	root := repo(t)
	write(t, root, "locked/kept.txt", "kept\n", 0o644)
	if err := os.Mkdir(filepath.Join(root, "locked", "emptysub"), 0o755); err != nil { // unchanged, and no reason to open locked
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(root, "locked"), 0o555); err != nil {
		t.Fatal(err)
	}
	write(t, root, "open/kept.txt", "kept\n", 0o644)
	if err := os.Chmod(filepath.Join(root, "open"), 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		os.Chmod(filepath.Join(root, "locked"), 0o755)
		os.Chmod(filepath.Join(root, "open"), 0o755)
	})
	mkfifo(t, root, "private/app.fifo")
	if err := os.Chmod(filepath.Join(root, "private"), 0o700); err != nil {
		t.Fatal(err)
	}
	s := shadow(t, root)
	id := take(t, s)
	emptyBefore, err := os.Stat(filepath.Join(root, "empty/inner"))
	if err != nil {
		t.Fatal(err)
	}
	mkfifo(t, root, "later/app.fifo")
	if err := os.Chmod(filepath.Join(root, "later"), 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, root, "untracked.go", "later\n", 0o644)
	if err := os.Chmod(filepath.Join(root, "open"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, root, "open/new.txt", "new\n", 0o644)
	if err := os.Chmod(filepath.Join(root, "open"), 0o555); err != nil {
		t.Fatal(err)
	}
	// Which directories it opens: those a change is under, and no others.
	d, err := s.differences(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := s.openDirs(d)
	if err != nil {
		t.Fatal(err)
	}
	if len(opened) != 1 || opened["open"] != 0o555 {
		t.Errorf("it opened %v, want only open", opened)
	}
	for dir, perm := range opened {
		if err := os.Chmod(filepath.Join(root, dir), perm); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := s.Restore(context.Background(), id, false); err != nil {
		t.Fatal(err)
	}
	emptyAfter, err := os.Stat(filepath.Join(root, "empty/inner"))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(emptyBefore, emptyAfter) {
		t.Error("the empty directory was removed and made again")
	}
	for p, want := range map[string]os.FileMode{"later": 0o700, "private": 0o700, "locked": 0o555, "open": 0o555} {
		st, err := os.Stat(filepath.Join(root, p))
		if err != nil || st.Mode().Perm() != want {
			t.Errorf("%s: %v %v, want %o", p, st, err, want)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "open/new.txt")); err == nil {
		t.Error("the new file in the read-only directory stayed")
	}
}

// A directory's own mode shows in Changes even if all it holds is a FIFO.
func TestModeDriftOnADirectoryHoldingOnlyAFIFO(t *testing.T) {
	root := repo(t)
	mkfifo(t, root, "private/app.fifo")
	if err := os.Chmod(filepath.Join(root, "private"), 0o700); err != nil {
		t.Fatal(err)
	}
	s := shadow(t, root)
	id := take(t, s)
	if err := os.Chmod(filepath.Join(root, "private"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Changes(context.Background(), id); err != nil || !slices.Equal(got, []Change{{"private", Bring}}) {
		t.Errorf("changes %v %v", got, err)
	}
	if _, _, err := s.Restore(context.Background(), id, false); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(filepath.Join(root, "private")); err != nil || st.Mode().Perm() != 0o700 {
		t.Errorf("private: %v %v", st, err)
	}
}

// A directory that can't be read, even opened, isn't taken for an empty one: both refuse. chflags is how to make chmod fail without
// being root, so this runs on the Mac.
func TestADirectoryThatCantBeReadRefuses(t *testing.T) {
	if runtime.GOOS != "darwin" || os.Geteuid() == 0 {
		t.Skip("needs chflags and a user chmod can fail for")
	}
	root := repo(t)
	s := shadow(t, root)
	id := take(t, s)
	write(t, root, "src/later.go", "x\n", 0o644)
	if err := os.Chmod(filepath.Join(root, "src"), 0o000); err != nil {
		t.Fatal(err)
	}
	run(t, root, "chflags", "uchg", "src")
	t.Cleanup(func() {
		exec.Command("chflags", "nouchg", filepath.Join(root, "src")).Run()
		os.Chmod(filepath.Join(root, "src"), 0o755)
	})
	if _, err := s.Changes(context.Background(), id); err == nil {
		t.Error("Changes read it as empty")
	}
	if _, _, err := s.Restore(context.Background(), id, true); !errors.Is(err, ErrUntouched) {
		t.Errorf("Restore: %v", err)
	}
}

// What a command removed comes back with the modes it had: a directory with its own mode above a file, an empty one, and an empty
// one inside a read-only directory that must be opened to make it.
func TestRemovedDirectoriesGetTheirModesBack(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes anywhere")
	}
	root := repo(t)
	write(t, root, "priv/secret.txt", "s\n", 0o644)
	if err := os.Chmod(filepath.Join(root, "priv"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "bare"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "ro", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(root, "ro"), 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(filepath.Join(root, "ro"), 0o755) })
	s := shadow(t, root)
	before := capture(t, root)
	id := take(t, s)
	for _, p := range []string{"priv", "bare"} {
		if err := os.RemoveAll(filepath.Join(root, p)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(filepath.Join(root, "ro"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "ro", "sub")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(root, "ro"), 0o555); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Restore(context.Background(), id, false); err != nil {
		t.Fatal(err)
	}
	sameTree(t, capture(t, root), before)
}

// A link standing where the checkpoint holds a directory isn't followed: what's on the other side keeps its mode.
func TestRestoreDoesntFollowALinkOutOfTheTree(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes anywhere")
	}
	root := repo(t)
	write(t, root, "dir/sub/file.txt", "kept\n", 0o644)
	write(t, root, "dir/fixed.txt", "kept\n", 0o600)
	s := shadow(t, root)
	before := capture(t, root)
	id := take(t, s)
	outside := t.TempDir()
	write(t, outside, "sub/theirs.txt", "theirs\n", 0o644)
	if err := os.Chmod(filepath.Join(outside, "sub"), 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(filepath.Join(outside, "sub"), 0o755) })
	if err := os.RemoveAll(filepath.Join(root, "dir")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "dir")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.openDirs(mustDifferences(t, s, id)); err != nil {
		t.Fatal(err)
	}
	st, err := os.Lstat(filepath.Join(outside, "sub"))
	if err != nil || st.Mode().Perm() != 0o555 {
		t.Fatalf("looking through the link: %v %v", st, err)
	}
	if _, _, err := s.Restore(context.Background(), id, false); err != nil {
		t.Fatal(err)
	}
	sameTree(t, capture(t, root), before)
	st, err = os.Lstat(filepath.Join(outside, "sub"))
	if err != nil || st.Mode().Perm() != 0o555 {
		t.Errorf("the directory outside: %v %v", st, err)
	}
	if b, err := os.ReadFile(filepath.Join(outside, "sub", "theirs.txt")); err != nil || string(b) != "theirs\n" {
		t.Errorf("the file outside: %q %v", b, err)
	}
}

func mustDifferences(t *testing.T, s *Shadow, id string) difference {
	t.Helper()
	d, err := s.differences(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// A directory made unreadable after the checkpoint: what's under it is restored while it's open, and it closes last.
func TestRestoreUnderADirectoryMadeUnreadable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads anything")
	}
	for _, c := range []struct {
		name string
		do   func(t *testing.T, root string)
	}{
		{"a changed file under it", func(t *testing.T, root string) { write(t, root, "top/mid/file.txt", "changed\n", 0o644) }},
		{"a mode alone under it", func(t *testing.T, root string) {
			if err := os.Chmod(filepath.Join(root, "top/mid/file.txt"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := repo(t)
			write(t, root, "top/mid/file.txt", "kept\n", 0o644)
			s := shadow(t, root)
			before := capture(t, root)
			id := take(t, s)
			c.do(t, root)
			for _, p := range []string{"top/mid", "top"} {
				if err := os.Chmod(filepath.Join(root, p), 0o000); err != nil {
					t.Fatal(err)
				}
			}
			t.Cleanup(func() {
				os.Chmod(filepath.Join(root, "top"), 0o755)
				os.Chmod(filepath.Join(root, "top/mid"), 0o755)
			})
			if _, _, err := s.Restore(context.Background(), id, true); err != nil {
				t.Fatal(err)
			}
			sameTree(t, capture(t, root), before)
		})
	}
}

// Failing to open a directory comes before anything has changed, and what was opened goes back.
func TestOpeningFailsBeforeAnythingChanges(t *testing.T) {
	if runtime.GOOS != "darwin" || os.Geteuid() == 0 {
		t.Skip("needs chflags and a user chmod can fail for")
	}
	root := repo(t)
	write(t, root, "stuck/inner/file.txt", "kept\n", 0o644)
	s := shadow(t, root)
	id := take(t, s)
	write(t, root, "stuck/inner/file.txt", "changed\n", 0o644)
	if err := os.Chmod(filepath.Join(root, "stuck/inner"), 0o555); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(root, "stuck"), 0o555); err != nil {
		t.Fatal(err)
	}
	run(t, root, "chflags", "uchg", "stuck/inner")
	t.Cleanup(func() {
		os.Chmod(filepath.Join(root, "stuck"), 0o755)
		exec.Command("chflags", "nouchg", filepath.Join(root, "stuck/inner")).Run()
		os.Chmod(filepath.Join(root, "stuck/inner"), 0o755)
	})
	if _, _, err := s.Restore(context.Background(), id, false); !errors.Is(err, ErrUntouched) {
		t.Errorf("err %v", err)
	}
	for _, p := range []string{"stuck", "stuck/inner"} {
		if st, err := os.Stat(filepath.Join(root, p)); err != nil || st.Mode().Perm() != 0o555 {
			t.Errorf("%s left as %v %v", p, st, err)
		}
	}
}

// From the default too: a directory holding only a FIFO has a mode a checkpoint keeps.
func TestModeDriftFromTheDefaultOnADirectoryHoldingOnlyAFIFO(t *testing.T) {
	root := repo(t)
	mkfifo(t, root, "plain/app.fifo")
	s := shadow(t, root)
	id := take(t, s)
	if err := os.Chmod(filepath.Join(root, "plain"), 0o700); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Changes(context.Background(), id); err != nil || !slices.Equal(got, []Change{{"plain", Bring}}) {
		t.Errorf("changes %v %v", got, err)
	}
	if _, _, err := s.Restore(context.Background(), id, false); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(filepath.Join(root, "plain")); err != nil || st.Mode().Perm() != 0o755 {
		t.Errorf("plain: %v %v", st, err)
	}
}

// NoUndo keeps to the limit the others do.
func TestNoUndoKeepsToItsLimit(t *testing.T) {
	s := shadow(t, repo(t))
	s.Limit = time.Nanosecond
	if why := s.NoUndo(context.Background()); why == "" {
		t.Error("it walked on past its limit")
	}
}

// NoUndo says so when snapshots are off for the session.
func TestNoUndoSaysWhenSnapshotsAreOff(t *testing.T) {
	s := shadow(t, repo(t))
	s.gaveUp = "a snapshot took longer than 1m0s"
	if why := s.NoUndo(context.Background()); why != s.gaveUp {
		t.Errorf("why %q", why)
	}
}

// A failed opening puts back what it opened, deepest first, so none is left closed with one above it open.
func TestOpenRollbackOrder(t *testing.T) {
	if got := deepestFirst(map[string]fs.FileMode{"a": 0o555, "a/b": 0o555, "a/b/c": 0o500, "z": 0o555}); got[0] != "a/b/c" || got[1] != "a/b" {
		t.Errorf("order %v", got)
	}
}
