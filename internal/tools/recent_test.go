package tools

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
