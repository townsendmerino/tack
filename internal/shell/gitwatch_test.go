package shell

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/townsendmerino/tack/internal/perms"
)

// A directory holding a ready-made .git, renamed or linked into the workspace, arrives past a profile that sees only the path written. The
// shell removes what a command added and says so, and leaves alone the workspace's own .git and any nested one that was there before.
func TestACommandCannotLeaveARepositoryInsideTheWorkspace(t *testing.T) {
	s := session(t, true)
	root := s.Engine.Root
	outside := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".git"), 0o755)
	os.MkdirAll(filepath.Join(root, "vendor", "old", ".git"), 0o755) // a nested repository from before
	for name, cmd := range map[string]string{
		"a made one":              `mkdir -p sub1/.git/hooks && echo x > sub1/.git/hooks/pre-commit`,
		"a made one, capitals":    `mkdir -p sub2/.GIT`,
		"a gitfile":               `mkdir sub3 && echo 'gitdir: ../x' > g3 && cp g3 sub3/.git`,
		"renamed in with its dir": `mkdir -p ` + outside + `/a/.git/hooks && mv ` + outside + `/a sub4`,
		"renamed in, .git alone":  `mkdir -p ` + outside + `/b/.git && mkdir sub5 && mv ` + outside + `/b/.git sub5/.git`,
		"linked in":               `mkdir -p ` + outside + `/c/.git && ln -s ` + outside + `/c sub6`,
	} {
		r := runBy(t, s, perms.ByMode, cmd)
		if !strings.Contains(r.Output, "tack removed") {
			t.Errorf("%s: not reported: %+v", name, r)
		}
	}
	for _, p := range []string{"sub1/.git", "sub2/.GIT", "sub3/.git", "sub4/.git", "sub5/.git", "sub6"} {
		if _, err := os.Lstat(filepath.Join(root, p)); err == nil {
			t.Errorf("%s is still there", p)
		}
	}
	for _, p := range []string{".git", "vendor/old/.git"} {
		if _, err := os.Lstat(filepath.Join(root, p)); err != nil {
			t.Errorf("%s was removed", p)
		}
	}
	// What a command did not make is left, and nothing is reported.
	if r := runBy(t, s, perms.ByMode, `echo hi > note.txt && mkdir -p plain/dir`); strings.Contains(r.Output, "tack removed") {
		t.Errorf("an ordinary command: %+v", r)
	}
	if r := runBy(t, s, perms.ByMode, `echo more >> vendor/old/x`); strings.Contains(r.Output, "tack removed") {
		t.Errorf("a command beside an existing nested repository: %+v", r)
	}
}
