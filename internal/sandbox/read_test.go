//go:build darwin

package sandbox

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// ReadPaths names Go's checksum-database cache, GOPATH/pkg/sumdb, which a toolchain switch reads, beside GOROOT and
// GOMODCACHE, and leaves out the home directory and / when PATH holds them; every path is real (symlinks resolved).
func TestReadPaths(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	gopath := filepath.Join(home, "gp")
	env := append(os.Environ(), "GOPATH="+gopath, "GOMODCACHE="+filepath.Join(gopath, "pkg", "mod"), "GOFLAGS=",
		"PATH="+os.Getenv("PATH")+string(filepath.ListSeparator)+home+string(filepath.ListSeparator)+"/")
	got := ReadPaths(env, "", home)
	for _, want := range []string{filepath.Join(gopath, "pkg", "sumdb"), filepath.Join(gopath, "pkg", "mod")} {
		if !slices.Contains(got, want) {
			t.Errorf("ReadPaths %q lacks %s", got, want)
		}
	}
	if slices.Contains(got, home) || slices.Contains(got, "/") {
		t.Errorf("ReadPaths %q holds the home directory or /", got)
	}
	for _, p := range got {
		if r := real(p); r != p {
			t.Errorf("%s is not its real path %s", p, r)
		}
	}
}
