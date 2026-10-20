package pilot

import (
	"archive/tar"
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"
)

var (
	treesMu sync.Mutex
	trees   = map[string]map[string]string{} // repo@base -> path -> content
)

// BaseTree is every regular file under 4 MB of repo at commit base, by slash path, read with git archive from the bare
// mirror <mirrors>/<repo>.git. Trees are cached for the process.
func BaseTree(mirrors, repo, base string) (map[string]string, error) {
	k := repo + "@" + base
	treesMu.Lock()
	defer treesMu.Unlock()
	if t, ok := trees[k]; ok {
		return t, nil
	}
	out, err := exec.Command("git", "--git-dir", filepath.Join(mirrors, repo+".git"), "archive", base).Output()
	if err != nil {
		return nil, fmt.Errorf("git archive %s: %w", k, err)
	}
	t := map[string]string{}
	tr := tar.NewReader(bytes.NewReader(out))
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag == tar.TypeReg && h.Size < 4<<20 {
			b, err := io.ReadAll(tr)
			if err != nil {
				return nil, err
			}
			t[h.Name] = string(b)
		}
	}
	trees[k] = t
	return t, nil
}

// cleanPath is a tool's path argument as a BaseTree key.
func cleanPath(p string) string {
	return strings.TrimPrefix(path.Clean(strings.TrimPrefix(p, "./")), "/")
}
