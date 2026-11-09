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

// TestRealResolvesAPathThatDoesNotExistYet: Seatbelt matches real paths, so a profile path must be real even when it
// does not exist yet, as a state directory may not: the existing part is resolved, through a symlink here, and the rest
// kept.
func TestRealResolvesAPathThatDoesNotExistYet(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	target := filepath.Join(dir, "target")
	os.Mkdir(target, 0o700)
	link := filepath.Join(dir, "link") // the test's own symlink: it needs no /tmp that is one
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ in, want string }{
		{link, target},
		{filepath.Join(link, "state"), filepath.Join(target, "state")},
		{filepath.Join(link, "state", "a", "b"), filepath.Join(target, "state", "a", "b")},
		{link + "/x/../state", filepath.Join(target, "state")}, // written uncleaned: x does not exist
		{link + "/state/", filepath.Join(target, "state")},
	} {
		if got := real(c.in); got != c.want {
			t.Errorf("real(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestRealResolvesDotDotAfterTheLink: ".." after a symlink is the parent of the link's target, as the kernel resolves
// it, not of the link: lexical cleaning first would name another directory.
func TestRealResolvesDotDotAfterTheLink(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "tack-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	resolved, _ := filepath.EvalSymlinks(dir)
	os.MkdirAll(filepath.Join(dir, "target", "sub"), 0o700)
	os.Symlink(filepath.Join(dir, "target", "sub"), filepath.Join(dir, "link"))
	for _, c := range []struct{ in, want string }{
		{dir + "/link/../x", filepath.Join(resolved, "target", "x")},        // x does not exist
		{dir + "/link/../sub", filepath.Join(resolved, "target", "sub")},    // it does
		{dir + "/link/../new/../x", filepath.Join(resolved, "target", "x")}, // a ".." past a missing name
	} {
		if got := real(c.in); got != c.want {
			t.Errorf("real(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestRealFollowsLinksBeyondWhatExists: a symlink whose target does not exist yet is still followed, since the path
// will be its target's once that is made; and a "..", after a name that does not exist, cancels that name, so a link
// after it is followed too.
func TestRealFollowsLinksBeyondWhatExists(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "tack-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	resolved, _ := filepath.EvalSymlinks(dir)
	os.Symlink(filepath.Join(dir, "later"), filepath.Join(dir, "dangling")) // later does not exist
	os.MkdirAll(filepath.Join(dir, "there"), 0o700)
	os.Symlink(filepath.Join(dir, "there"), filepath.Join(dir, "link"))
	for _, c := range []struct{ in, want string }{
		{dir + "/dangling/state", filepath.Join(resolved, "later", "state")},
		{dir + "/dangling", filepath.Join(resolved, "later")},
		{dir + "/missing/../link/state", filepath.Join(resolved, "there", "state")},
	} {
		if got := real(c.in); got != c.want {
			t.Errorf("real(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
