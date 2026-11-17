//go:build darwin

package sandbox

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// TestCheckRefusesOverlap: a tack path that is the workspace, contains it, or lies inside it is refused, through
// symlinks (/tmp is /private/tmp) and for paths not yet created; a sibling, or a directory whose name merely shares the
// workspace's as a prefix, is not.
func TestCheckRefusesOverlap(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	ws := filepath.Join(root, "task", "repo")
	os.MkdirAll(ws, 0o755)
	link := filepath.Join(t.TempDir(), "link") // an unresolved path to root
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		tack string
		bad  bool
	}{
		{"the workspace itself", ws, true},
		{"its parent, holding config.toml", filepath.Join(root, "task"), true},
		{"its parent, through a symlink", filepath.Join(link, "task"), true},
		{"a state directory inside it, not yet created", filepath.Join(ws, ".tack", "state"), true},
		{"inside it, through a symlink, not yet created", filepath.Join(link, "task", "repo", "state"), true},
		{"a sibling", filepath.Join(root, "task", "repo-tack"), false},
		{"a sibling, not yet created", filepath.Join(root, "task", "A1-tack", "state"), false},
	} {
		err := Config{Workspace: ws, Tack: []string{filepath.Join(root, "elsewhere"), c.tack}}.Check()
		if (err != nil) != c.bad {
			t.Errorf("%s (%s): err %v, want refused=%v", c.name, c.tack, err, c.bad)
		}
	}
}

// TestTheGitDenyListMatchesWhatItIsFor: the rules' regular expressions are matched here (Go's syntax reads these as Seatbelt does) against
// paths that must be denied and paths that must stay writable, so that git add and git commit work and nothing that redirects git does.
// The workspace path is quoted so its dots and brackets match only themselves. The escape tests (sandboxtest) show the kernel refusing.
func TestTheGitDenyListMatchesWhatItIsFor(t *testing.T) {
	ws := "/w.s/[x]"
	var res []*regexp.Regexp
	for _, rule := range gitRedirects(ws) {
		m := regexp.MustCompile(`^\(regex #"(.*)"\)$`).FindStringSubmatch(rule)
		if m == nil {
			t.Fatalf("not a regex rule: %s", rule)
		}
		res = append(res, regexp.MustCompile(m[1]))
	}
	denied := func(p string) bool {
		for _, r := range res {
			if r.MatchString(p) {
				return true
			}
		}
		return false
	}
	for _, p := range []string{".git", "sub/.git", "sub/deep/.GIT", ".git/config", ".git/CONFIG", ".git/Config.Worktree", ".git/commondir", ".git/hooks", ".git/hooks/pre-commit",
		".git/HOOKS/x", ".git/info/exclude", ".git/info/attributes", ".git/info/sparse-checkout", ".git/objects/info/alternates", ".git/objects/INFO/Alternates",
		".git/objects/info/http-alternates", ".git/modules/m/config", ".git/modules/a/b/config", ".git/modules/m/hooks/post-commit", ".git/worktrees/w/commondir",
		".git/rebase-merge/git-rebase-todo", ".git/rebase-apply/patch", ".git/sequencer/todo", ".git/REBASE-MERGE/x", ".git/remotes/origin", ".git/branches/b",
		".git/modules/m/commondir", ".git/modules/m/config.worktree", ".git/modules/m/info/exclude", ".git/modules/m/objects/info/alternates", ".git/modules/m/HEAD",
		".git/worktrees/w/gitdir", ".git/worktrees/w/config.worktree", "sub/.git/config", "sub/.Git/hooks/x"} {
		if !denied(ws + "/" + p) {
			t.Errorf("%s is writable", p)
		}
	}
	for _, p := range []string{".git/index", ".git/index.lock", ".git/HEAD", ".git/ORIG_HEAD", ".git/FETCH_HEAD", ".git/COMMIT_EDITMSG", ".git/packed-refs", ".git/refs/heads/main",
		".git/logs/HEAD", ".git/objects/ab/cdef", ".git/objects/pack/pack-1.pack", ".git/objects/info/packs", ".git/worktrees/w/HEAD",
		".gitignore", ".github/workflows/x", "a.git", "sub/.gitattributes", "config", "hooks/x", "info/x", "sub/config"} {
		if denied(ws + "/" + p) {
			t.Errorf("%s is denied", p)
		}
	}
	// A different workspace's .git is not this one's, and the quoted dots match only dots.
	if denied("/wXs/[x]/.git/config") || denied("/w.s/x/.git/config") {
		t.Error("the workspace path is not quoted")
	}
	if got := regexQuote("/a.b[c]/d+e"); got != `/a\.b\[c\]/d\+e` {
		t.Errorf("regexQuote = %q", got)
	}
	if got := regexQuote(`/a"b\c`); got != `/a\"b\\c` {
		t.Errorf("regexQuote of a quote and a backslash = %q", got)
	}
}

// TestCheckRefusesAWorkspacePathTheProfileCannotQuote: a double quote or a control character in the workspace path is refused, so the path
// written into the profile's regular expressions never needs more than regexQuote.
func TestCheckRefusesAWorkspacePathTheProfileCannotQuote(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	for _, name := range []string{`a"b`, "a\nb", "a\rb"} {
		ws := filepath.Join(root, name)
		os.MkdirAll(ws, 0o755)
		if err := (Config{Workspace: ws, Tack: []string{filepath.Join(root, "elsewhere")}}).Check(); err == nil {
			t.Errorf("%q was accepted", name)
		}
	}
	ws := filepath.Join(root, "fine [x].y")
	os.MkdirAll(ws, 0o755)
	if err := (Config{Workspace: ws, Tack: []string{filepath.Join(root, "elsewhere")}}).Check(); err != nil {
		t.Errorf("brackets and dots: %v", err)
	}
}
