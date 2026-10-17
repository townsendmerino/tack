//go:build darwin

// Package sandbox confines the commands tack runs, on macOS with Seatbelt through sandbox-exec (DESIGN §6.6). The
// profile starts from (deny default) and is built from named sections, each the reason one kind of escape fails; the
// escape tests (sandbox_test.go, build tag sandboxtest) show each escape caught, and caught by its own section.
//
// What it allows: reading the file system, except tack's own files and the secrets deny-list; writing the workspace
// (except files that would later run outside the sandbox), the session's temp directory and tack's own Go build
// cache; running programs, except those that launch work outside the sandbox. The network is denied outright (P1;
// a per-domain proxy arrives with web_fetch in P5).
package sandbox

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// Exe is sandbox-exec. Apple deprecated its public interface long ago; it works on macOS 26 (DESIGN §17, R6).
const Exe = "/usr/bin/sandbox-exec"

// Config is what a session's profile is made from. Paths are absolute; symlinks are resolved when the profile is built,
// since Seatbelt matches real paths (/tmp is /private/tmp).
type Config struct {
	Workspace string   // readable and writable, with the exceptions in Section "later"
	Temp      string   // the session's temp directory, writable
	GoCache   string   // tack's own GOCACHE, writable; the owner's caches are not
	Home      string   // the owner's home, for the secrets deny-list and shell startup files
	Tack      []string // tack's own socket, lock, token, state directory and config: neither read nor written
	// Without drops named sections. Only the escape tests use it, to show each section is what catches its escape.
	Without []string
}

// Sections, in the order they are written.
const (
	SecBase    = "base"    // what every process needs: fork, exec, signals within the sandbox, sysctl, mach lookups
	SecRead    = "read"    // reading the file system
	SecWrite   = "write"   // writing only the workspace, the session's temp directory and tack's own Go cache
	SecLaunch  = "launch"  // open, launchctl, osascript and LaunchServices: a program they start runs outside
	SecMount   = "mount"   // mounting volumes: a mounted volume is another way out
	SecNetwork = "network" // all network access
	SecTack    = "tack"    // tack's own files: a command must not approve its own request or read transcripts
	SecLater   = "later"   // workspace files that would run later, outside the sandbox: git hooks and config, agent config
	SecSecrets = "secrets" // the secrets deny-list, enforced by the kernel
)

func real(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

func q(p string) string { return fmt.Sprintf("%q", real(p)) }

// Profile returns the SBPL profile for c.
func Profile(c Config) string {
	skip := map[string]bool{}
	for _, s := range c.Without {
		skip[s] = true
	}
	var b strings.Builder
	sec := func(name string, lines ...string) {
		if skip[name] {
			return
		}
		fmt.Fprintf(&b, "; %s\n", name)
		for _, l := range lines {
			b.WriteString(l + "\n")
		}
	}
	b.WriteString("(version 1)\n(deny default)\n")
	sec(SecBase,
		"(allow process-fork)",
		"(allow process-exec*)",
		"(allow process-info*)",
		"(allow signal (target same-sandbox))",
		"(allow sysctl-read)",
		"(allow mach-lookup)",
		"(allow ipc-posix*)",
		"(allow file-ioctl)",
		"(allow pseudo-tty)",
		`(allow file-write* (literal "/dev/null") (literal "/dev/zero") (literal "/dev/dtracehelper") (regex #"^/dev/tty") (regex #"^/dev/fd/"))`,
		// Xcode's xcrun shims (/usr/bin/git and the rest) keep a lookup cache in the per-user temp directory: that file
		// only, not the directory.
		`(allow file-write* (regex #"^/private/var/folders/[^/]+/[^/]+/T/xcrun_db"))`,
	)
	sec(SecRead, "(allow file-read*)")
	if skip[SecWrite] {
		b.WriteString("(allow file-write*)\n") // the hole the write allowlist closes, for its escape tests
	} else {
		sec(SecWrite, fmt.Sprintf("(allow file-write* (subpath %s) (subpath %s) (subpath %s))", q(c.Workspace), q(c.Temp), q(c.GoCache)))
	}
	sec(SecLaunch,
		`(deny process-exec (literal "/usr/bin/open") (literal "/bin/launchctl") (literal "/usr/bin/osascript") (literal "/usr/bin/osacompile") (literal "/usr/bin/automator") (literal "/usr/bin/shortcuts"))`,
		`(deny mach-lookup (global-name "com.apple.coreservices.launchservicesd") (global-name-prefix "com.apple.lsd.") (global-name "com.apple.coreservices.appleevents") (global-name "com.apple.xpc.launchd.domain.system"))`,
	)
	sec(SecMount,
		`(deny process-exec (literal "/usr/bin/hdiutil") (literal "/usr/sbin/diskutil") (literal "/sbin/mount") (regex #"^/sbin/mount_") (literal "/usr/bin/hdik"))`,
		`(deny mach-lookup (global-name "com.apple.DiskArbitration.diskarbitrationd") (global-name-prefix "com.apple.diskimages"))`,
	)
	if skip[SecNetwork] {
		b.WriteString("(allow network*)\n") // the hole (deny default) and this section close, for its escape tests
	} else {
		sec(SecNetwork, "(deny network*)")
	}
	if len(c.Tack) > 0 {
		var paths []string
		for _, p := range c.Tack {
			paths = append(paths, fmt.Sprintf("(subpath %s)", q(p)))
		}
		sec(SecTack, fmt.Sprintf("(deny file-read* file-write* %s)", strings.Join(paths, " ")))
	}
	ws := real(c.Workspace)
	later := []string{
		fmt.Sprintf("(subpath %q)", ws+"/.git/hooks"), fmt.Sprintf("(literal %q)", ws+"/.git/config"),
		fmt.Sprintf("(literal %q)", ws+"/.mcp.json"), fmt.Sprintf("(subpath %q)", ws+"/.claude"),
		fmt.Sprintf("(subpath %q)", ws+"/.tack"), fmt.Sprintf("(subpath %q)", ws+"/.vscode"),
	}
	home := real(c.Home)
	for _, f := range []string{".zshrc", ".zshenv", ".zprofile", ".zlogin", ".bashrc", ".bash_profile", ".profile"} {
		later = append(later, fmt.Sprintf("(literal %q)", filepath.Join(home, f)))
	}
	sec(SecLater, fmt.Sprintf("(deny file-write* %s)", strings.Join(later, " ")))
	sec(SecSecrets, fmt.Sprintf(`(deny file-read* file-write* (regex #"/\.env($|\.)") (regex #"\.(pem|key|p12|pfx)$") (subpath %q) (subpath %q) (subpath %q) (subpath %q) (subpath %q))`,
		filepath.Join(home, ".ssh"), filepath.Join(home, ".aws"), filepath.Join(home, ".gnupg"),
		filepath.Join(home, ".config/gh"), filepath.Join(home, "Library/Keychains")))
	return b.String()
}

// Command returns name with args, run under c's profile.
func Command(c Config, name string, args ...string) *exec.Cmd {
	return exec.Command(Exe, append([]string{"-p", Profile(c), name}, args...)...)
}

// Wrap returns cmd re-pointed to run under c's profile, keeping its environment, directory, files and process
// attributes.
func Wrap(c Config, cmd *exec.Cmd) *exec.Cmd {
	cmd.Args = append([]string{Exe, "-p", Profile(c), cmd.Path}, cmd.Args[1:]...)
	cmd.Path = Exe
	return cmd
}
