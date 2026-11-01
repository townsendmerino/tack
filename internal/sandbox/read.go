//go:build darwin

package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// systemRead are the system paths a build reads: the OS, its libraries and frameworks, the command-line tools and
// Xcode (git and cc), Homebrew, time zones, the developer-directory link and devices.
var systemRead = []string{"/System", "/usr", "/bin", "/sbin", "/Library", "/private/etc", "/private/var/db/timezone",
	"/private/var/select", "/Applications/Xcode.app", "/opt/homebrew", "/dev"}

// homeRead are the files under the owner's home a build or git reads: git's and Go's own configuration. Section
// secrets still denies git's credentials.
var homeRead = []string{".gitconfig", ".config/git", "Library/Application Support/go"}

// readLines is section read: the metadata of any path (a path's components are stat'ed on the way to it, and a cwd
// is found through its parents), and the contents of only the system paths, the workspace, the session's temp
// directory, tack's Go cache, the owner's git and Go configuration, and c.Read. Nothing else outside the workspace is
// readable: not the owner's other repositories, not the E1 mirrors or other runs' records.
func readLines(c Config) []string {
	var paths []string
	add := func(kind, p string) { paths = append(paths, fmt.Sprintf("(%s %s)", kind, q(p))) }
	paths = append(paths, `(literal "/")`)
	for _, p := range systemRead {
		add("subpath", p)
	}
	for _, p := range []string{c.Workspace, c.Temp, c.GoCache} {
		if p != "" {
			add("subpath", p)
		}
	}
	if c.Home != "" {
		for _, p := range homeRead {
			add("subpath", filepath.Join(c.Home, p))
		}
	}
	for _, p := range c.Read {
		add("subpath", p)
	}
	return []string{
		"(allow file-read-metadata)",
		"(allow file-read* " + strings.Join(paths, " ") + ")",
		// xcrun's lookup cache and the per-user cache directory the command-line tools keep, in the per-user temp area;
		// the rest of that area holds other processes' temp files.
		`(allow file-read* (regex #"^/private/var/folders/[^/]+/[^/]+/T/xcrun_db") (regex #"^/private/var/folders/[^/]+/[^/]+/C/"))`,
	}
}

// ReadPaths are the paths a command needs to read beyond the system's, for Config.Read: tack's own executable (the
// sandboxed shell is `tack shell`), Go's GOROOT, GOMODCACHE and checksum-database cache (GOPATH/pkg/sumdb, which a
// toolchain switch verifies against) as go reports them under env, and env's PATH directories, without the home
// directory, the root or a relative entry, any of which would open far more. Each is its real path, symlinks
// resolved, as Seatbelt matches.
func ReadPaths(env []string, exe, home string) []string {
	var out []string
	if exe != "" {
		out = append(out, real(exe))
	}
	var path string
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			path = v
		}
	}
	h := real(home)
	for _, d := range filepath.SplitList(path) {
		if !filepath.IsAbs(d) {
			continue
		}
		if r := real(d); r != "/" && r != h {
			out = append(out, r)
		}
	}
	if goExe, err := lookPath("go", path); err == nil {
		cmd := exec.Command(goExe, "env", "GOROOT", "GOMODCACHE", "GOPATH")
		cmd.Env = env
		if b, err := cmd.Output(); err == nil {
			if v := strings.Split(strings.TrimSpace(string(b)), "\n"); len(v) == 3 {
				ps := []string{v[0], v[1]}
				if gp := filepath.SplitList(strings.TrimSpace(v[2])); len(gp) > 0 {
					ps = append(ps, filepath.Join(gp[0], "pkg", "sumdb"))
				}
				for _, p := range ps {
					if p = strings.TrimSpace(p); filepath.IsAbs(p) {
						out = append(out, real(p))
					}
				}
			}
		}
	}
	return out
}

// lookPath finds name in the PATH list path.
func lookPath(name, path string) (string, error) {
	for _, d := range filepath.SplitList(path) {
		p := filepath.Join(d, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return "", fmt.Errorf("%s not on PATH", name)
}
