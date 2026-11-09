package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// gitRepo is a repository with two commits under sub/ and a file at the top, then an uncommitted change to sub/a.go
// and an untracked sub/new.go.
func gitRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
			"GIT_AUTHOR_DATE=2026-11-07T10:00:00Z", "GIT_COMMITTER_DATE=2026-11-07T10:00:00Z")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	os.MkdirAll(filepath.Join(root, "sub"), 0o755)
	run("init", "-q")
	os.WriteFile(filepath.Join(root, "sub", "a.go"), []byte("package a\n"), 0o644)
	os.WriteFile(filepath.Join(root, "top.go"), []byte("package top\n"), 0o644)
	run("add", "sub/a.go", "top.go")
	run("commit", "-q", "-m", "first: add a and top")
	os.WriteFile(filepath.Join(root, "sub", "b.go"), []byte("package a\n"), 0o644)
	run("add", "sub/b.go")
	run("commit", "-q", "-m", "second: add b")
	os.WriteFile(filepath.Join(root, "sub", "a.go"), []byte("package a\n\nfunc A() {}\n"), 0o644)
	os.WriteFile(filepath.Join(root, "sub", "new.go"), []byte("package a\n"), 0o644)
	return root
}

func recent(t *testing.T, root, input string) Result {
	t.Helper()
	ws, err := NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	r, err := recentTool{}.Run(context.Background(), ws, json.RawMessage(input))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRecentlyChanged(t *testing.T) {
	root := gitRepo(t)
	r := recent(t, root, `{}`)
	for _, want := range []string{"Uncommitted changes (git status):\n M sub/a.go\n?? sub/new.go", "Last 10 commits (git log), newest first:",
		"2026-11-07 second: add b\n  A\tsub/b.go", "first: add a and top", "  A\ttop.go"} {
		if !strings.Contains(r.Text, want) {
			t.Errorf("lacks %q:\n%s", want, r.Text)
		}
	}
	if r := recent(t, root, `{"commits":1}`); strings.Contains(r.Text, "first: add") || !strings.Contains(r.Text, "second: add") {
		t.Errorf("one commit:\n%s", r.Text)
	}
	for _, bad := range []string{`{"commits":0}`, `{"commits":31}`, `{"commits":"x"}`} {
		if r := recent(t, root, bad); !r.IsError {
			t.Errorf("%s: %s", bad, r.Text)
		}
	}
}

// TestRecentlyChangedInASubdirectory: in a workspace below the repository's top, paths are from the workspace root and
// only its own files appear.
func TestRecentlyChangedInASubdirectory(t *testing.T) {
	root := gitRepo(t)
	r := recent(t, filepath.Join(root, "sub"), `{}`)
	if !strings.Contains(r.Text, " M a.go\n?? new.go") || !strings.Contains(r.Text, "  A\tb.go") || strings.Contains(r.Text, "top.go") ||
		strings.Contains(r.Text, "sub/") {
		t.Errorf("subdirectory view:\n%s", r.Text)
	}
}

func TestRecentlyChangedOutsideGit(t *testing.T) {
	if r := recent(t, t.TempDir(), `{}`); !r.IsError || !strings.Contains(r.Text, "not a git repository") {
		t.Errorf("%+v", r)
	}
}

// TestRecentlyChangedRunsNothingTheRepoConfigures: a repository whose config names an fsmonitor hook, and an
// environment pointing git elsewhere, change nothing: the hook never runs and the workspace's own history is shown.
func TestRecentlyChangedRunsNothingTheRepoConfigures(t *testing.T) {
	root := gitRepo(t)
	marker := filepath.Join(t.TempDir(), "ran")
	hook := filepath.Join(t.TempDir(), "hook.sh")
	os.WriteFile(hook, []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o755)
	cmd := exec.Command("git", "config", "core.fsmonitor", hook)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	other := gitRepo(t)
	os.WriteFile(filepath.Join(other, "only-in-other.go"), []byte("package x\n"), 0o644)
	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	t.Setenv("GIT_WORK_TREE", other)
	r := recent(t, root, `{}`)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the repository's fsmonitor hook ran")
	}
	if r.IsError || !strings.Contains(r.Text, "M sub/a.go") || strings.Contains(r.Text, "only-in-other.go") {
		t.Errorf("%+v", r)
	}
}

// TestRecentlyChangedIsOptIn: the tool is in a set only when asked for, so sessions without it keep their prompt.
func TestRecentlyChangedIsOptIn(t *testing.T) {
	has := func(s *Set) bool {
		for _, sp := range s.Specs() {
			if sp.Name == "recently_changed" {
				return true
			}
		}
		return false
	}
	if has(New(Options{})) || !has(New(Options{RecentlyChanged: true})) {
		t.Fatal("recently_changed is in a set only with Options.RecentlyChanged")
	}
}

// gitIn runs git in dir for a test.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestRecentlyChangedRunsNoFilterOrGPG: a repository whose config names a clean filter for a stat-dirty file, and a
// gpg program with signatures shown for a signed commit, runs neither.
func TestRecentlyChangedRunsNoFilterOrGPG(t *testing.T) {
	root := gitRepo(t)
	marker := filepath.Join(t.TempDir(), "ran")
	prog := filepath.Join(t.TempDir(), "prog.sh")
	os.WriteFile(prog, []byte("#!/bin/sh\ntouch "+marker+"\ncat\n"), 0o755)
	os.WriteFile(filepath.Join(root, ".gitattributes"), []byte("*.txt filter=Evil\n"), 0o644)
	os.WriteFile(filepath.Join(root, "f.txt"), []byte("hi\n"), 0o644)
	gitIn(t, root, "add", ".gitattributes", "f.txt")
	gitIn(t, root, "commit", "-q", "-m", "third: add f")
	// A commit carrying a signature header, so showing signatures would run gpg.program on it.
	os.WriteFile(filepath.Join(root, "g.go"), []byte("package x\n"), 0o644)
	gitIn(t, root, "add", "g.go")
	tree := gitIn(t, root, "write-tree") // a tree with g.go, so the signed commit changes a file and the log shows it
	head := gitIn(t, root, "rev-parse", "HEAD")
	obj := "tree " + tree + "\nparent " + head + "\nauthor t <t@t> 1762500000 +0000\ncommitter t <t@t> 1762500000 +0000\n" +
		"gpgsig -----BEGIN PGP SIGNATURE-----\n \n x\n -----END PGP SIGNATURE-----\n\nsigned: nothing\n"
	cmd := exec.Command("git", "hash-object", "-t", "commit", "-w", "--stdin")
	cmd.Dir, cmd.Stdin = root, strings.NewReader(obj)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	gitIn(t, root, "update-ref", "HEAD", strings.TrimSpace(string(out)))
	for _, kv := range [][2]string{{"filter.Evil.clean", prog}, {"filter.Evil.process", prog}, {"log.showSignature", "true"}, {"gpg.program", prog}} {
		gitIn(t, root, "config", kv[0], kv[1])
	}
	time.Sleep(1100 * time.Millisecond)
	os.WriteFile(filepath.Join(root, "f.txt"), []byte("hi\n"), 0o644) // stat-dirty, same content: status must hash it
	r := recent(t, root, `{}`)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the repository's filter or gpg program ran")
	}
	if r.IsError || !strings.Contains(r.Text, "signed: nothing") {
		t.Errorf("%+v", r)
	}
}

// TestRecentlyChangedPathsInASubdirectory: renamed and quoted paths are from the workspace root, both sides of a
// rename.
func TestRecentlyChangedPathsInASubdirectory(t *testing.T) {
	root := gitRepo(t)
	gitIn(t, root, "mv", "sub/b.go", "sub/c.go")
	os.WriteFile(filepath.Join(root, "sub", "a b.go"), []byte("package a\n"), 0o644)
	r := recent(t, filepath.Join(root, "sub"), `{}`)
	if !strings.Contains(r.Text, "R  b.go -> c.go") || !strings.Contains(r.Text, "?? a b.go") || strings.Contains(r.Text, "sub/") {
		t.Errorf("subdirectory paths:\n%s", r.Text)
	}
}

// TestRecentlyChangedEmptyAndLong: a repository with no commits and a clean one each say (none); status and log are
// cut on counts of their own, so a long status leaves the log whole.
func TestRecentlyChangedEmptyAndLong(t *testing.T) {
	t.Setenv("LANG", "de_DE.UTF-8")
	t.Setenv("LC_ALL", "de_DE.UTF-8")
	empty := t.TempDir()
	gitIn(t, empty, "init", "-q")
	os.WriteFile(filepath.Join(empty, "x.go"), []byte("package x\n"), 0o644)
	if r := recent(t, empty, `{}`); r.IsError || !strings.Contains(r.Text, "?? x.go") || !strings.HasSuffix(r.Text, "newest first:\n(none)") {
		t.Errorf("no commits:\n%+v", r)
	}
	clean := gitRepo(t)
	gitIn(t, clean, "add", "-A")
	gitIn(t, clean, "commit", "-q", "-m", "third")
	if r := recent(t, clean, `{}`); !strings.Contains(r.Text, "(git status):\n(none)") {
		t.Errorf("clean tree:\n%s", r.Text)
	}
	long := gitRepo(t)
	for i := range recentMaxLines + 50 {
		os.WriteFile(filepath.Join(long, fmt.Sprintf("u%03d.go", i)), []byte("package x\n"), 0o644)
	}
	r := recent(t, long, `{}`)
	if strings.Count(r.Text, "\n?? ")+strings.Count(r.Text, "\n M ") != recentMaxLines || !strings.Contains(r.Text, "[… more; narrow") || !strings.Contains(r.Text, "first: add a and top") {
		t.Errorf("a long status: %d untracked lines, log whole %v", strings.Count(r.Text, "\n?? "), strings.Contains(r.Text, "first: add"))
	}
}

// TestRecentlyChangedCutsTheLogOnItsOwnCount: a long status and a long log are each cut at recentMaxLines lines.
func TestRecentlyChangedCutsTheLogOnItsOwnCount(t *testing.T) {
	root := gitRepo(t)
	for c := range 30 {
		for f := range 7 {
			os.WriteFile(filepath.Join(root, fmt.Sprintf("c%02d_%d.go", c, f)), []byte("package x\n"), 0o644)
			gitIn(t, root, "add", fmt.Sprintf("c%02d_%d.go", c, f))
		}
		gitIn(t, root, "commit", "-q", "-m", fmt.Sprintf("commit %d", c))
	}
	for i := range recentMaxLines + 10 {
		os.WriteFile(filepath.Join(root, fmt.Sprintf("u%03d.go", i)), []byte("package x\n"), 0o644)
	}
	r := recent(t, root, `{"commits":30}`)
	_, log, _ := strings.Cut(r.Text, "newest first:\n")
	log, _, cut := strings.Cut(log, "\n[… more; ask for fewer commits]")
	if n := strings.Count(log, "\n") + 1; !cut || n != recentMaxLines {
		t.Errorf("the log shows %d lines (cut %v), want %d", n, cut, recentMaxLines)
	}
}
