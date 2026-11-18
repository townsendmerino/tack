package shell

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// A command may not leave a repository of its own making inside the workspace. The sandbox denies the parts of a .git that redirect git,
// but it sees only the path being written: a directory holding a ready-made .git, built where the command may write (its temp
// directory) and renamed or linked in, arrives whole (DESIGN §6.6). The owner's editor or git, run later in that directory, would use
// its config. So after every command the workspace is scanned for .git entries that were not there before it, and each is removed and
// reported. The workspace's own .git, and any that existed before the command, are left alone.

// maxScan bounds the entries one scan visits; a larger tree is not scanned, and says so.
const maxScan = 400000

// nestedGits returns the paths below root that are, or lead to, a git directory of their own: an entry named .git (any letter case)
// other than root's own, and a symlink whose target is outside root and is, or holds, a .git. ok is false when the tree was too large.
func nestedGits(root string) (found map[string]bool, ok bool) {
	found = map[string]bool{}
	rootGit := filepath.Join(root, ".git")
	n := 0
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if n++; n > maxScan {
			return errTooLarge
		}
		if p == rootGit {
			return filepath.SkipDir
		}
		if strings.EqualFold(d.Name(), ".git") && p != root {
			found[p] = true
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			if t, err := filepath.EvalSymlinks(p); err == nil && !within(root, t) {
				if _, err := os.Lstat(filepath.Join(t, ".git")); err == nil || strings.EqualFold(filepath.Base(t), ".git") {
					found[p] = true
				}
			}
		}
		return nil
	})
	return found, err == nil
}

var errTooLarge = fs.ErrInvalid

func within(root, p string) bool {
	r, err := filepath.Rel(root, p)
	return err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator))
}

// removeNewGits removes what a command added to the workspace's set of nested git entries, and returns the paths it removed, sorted.
func removeNewGits(root string, before map[string]bool) []string {
	after, _ := nestedGits(root)
	var removed []string
	for p := range after {
		if before[p] {
			continue
		}
		if err := os.RemoveAll(p); err == nil {
			removed = append(removed, p)
		}
	}
	sort.Strings(removed)
	return removed
}
