//go:build darwin

package sandbox

import (
	"os"
	"path/filepath"
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
