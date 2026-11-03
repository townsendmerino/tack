package snapshot

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
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
			return filepath.SkipDir
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
			undo, err := s.Restore(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			sameTree(t, capture(t, root), before)

			// The rewind can itself be undone.
			if undo == "" {
				t.Fatal("no snapshot of the tree it replaced")
			}
			if _, err := s.Restore(context.Background(), undo); err != nil {
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
	if _, err := s.Restore(context.Background(), id); err != nil {
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
	if _, err := s.Restore(context.Background(), id); err != nil {
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
	if err := os.Mkdir(filepath.Join(s.Dir, "restore-index"), 0o755); err != nil { // read-tree can't write its index there
		t.Fatal(err)
	}
	undo, err := s.Restore(context.Background(), id)
	if err == nil || undo == "" {
		t.Fatalf("undo %q, err %v", undo, err)
	}
	if err := os.Remove(filepath.Join(s.Dir, "restore-index")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Restore(context.Background(), undo); err != nil {
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
	write(t, root, "src/a.go", "changed\n", 0o644)
	if _, err := s.Restore(context.Background(), id); err != nil {
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
	write(t, root, "ident.txt", "gone\n", 0o644)
	if _, err := s.Restore(context.Background(), id); err != nil {
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
	if _, err := s.Restore(context.Background(), old); err == nil || !strings.Contains(err.Error(), "gone") {
		t.Errorf("an old checkpoint: %v", err)
	}
	if _, err := s.Changes(context.Background(), old); err == nil || !strings.Contains(err.Error(), "gone") {
		t.Errorf("an old checkpoint's changes: %v", err)
	}
	write(t, root, "untracked.go", "third\n", 0o644)
	if _, err := s.Restore(context.Background(), kept); err != nil {
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
		if _, err := s.Restore(context.Background(), id); err == nil {
			t.Errorf("%q restored", id)
		}
	}
	sameTree(t, capture(t, root), before)

	// A commit still in the shadow whose checkpoint was dropped is gone too.
	id := take(t, s)
	if _, err := s.git(context.Background(), nil, nil, "update-ref", "-d", "refs/tack/"+id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Restore(context.Background(), id); err == nil || !strings.Contains(err.Error(), "gone") {
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
	if _, err := s.Restore(context.Background(), id); err != nil {
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

// Two checkpoints in one second can be dropped either way round; if Prune drops the one Take remembers, Take makes a new one
// rather than naming one that's gone.
func TestTakeAfterPruneDroppedItsLast(t *testing.T) {
	root := repo(t)
	s := shadow(t, root)
	when := time.Unix(2_000_000_000, 0)
	s.Now = func() time.Time { return when }
	for i := range 12 {
		write(t, root, "untracked.go", fmt.Sprintf("a %d\n", i), 0o644)
		take(t, s)
		write(t, root, "untracked.go", fmt.Sprintf("b %d\n", i), 0o644)
		take(t, s)
		when = when.Add(MaxAge + time.Hour)
		if err := s.Prune(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Changes(context.Background(), take(t, s)); err != nil {
			t.Fatalf("round %d: %v", i, err)
		}
	}
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
