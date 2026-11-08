//go:build darwin && sandboxtest

// The escape tests (DESIGN §6.6). They start sandbox-exec, which macOS refuses inside an already sandboxed process, so
// they carry the sandboxtest tag and run from the day-loop checker or CI, never from tack's own bash:
//
//	go test -tags sandboxtest ./internal/sandbox/
package sandbox

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

type fixture struct {
	cfg             Config
	ws, home, state string
	ownerGoCache    string
	ownerModCache   string
	env             []string
}

func setup(t *testing.T) fixture {
	t.Helper()
	dir := func() string { d, _ := filepath.EvalSymlinks(t.TempDir()); return d }
	f := fixture{ws: dir(), home: dir(), state: dir()}
	write := func(p, body string) {
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range [][]string{{"git", "init", "-q"}} {
		cmd := exec.Command(c[0], c[1:]...)
		cmd.Dir = f.ws
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}
	write(filepath.Join(f.ws, "go.mod"), "module x\n\ngo 1.27\n")
	write(filepath.Join(f.ws, "x_test.go"), "package x\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) {}\n")
	write(filepath.Join(f.ws, ".env"), "SECRET=ws-dotenv\n")
	write(filepath.Join(f.home, ".ssh", "id_test"), "SECRET=ssh-key\n")
	write(filepath.Join(f.home, ".config", "gh", "hosts.yml"), "github.com:\n    oauth_token: SECRET=gh-token\n")
	write(filepath.Join(f.home, "Library", "Keychains", "login.keychain-db"), "SECRET=keychain\n")
	write(filepath.Join(f.home, ".git-credentials"), "https://x:SECRET=git-cred.com\n")
	write(filepath.Join(f.home, ".netrc"), "machine x password SECRET=netrc\n")
	write(filepath.Join(f.home, ".docker", "config.json"), "{\"auths\":\"SECRET=docker\"}\n")
	write(filepath.Join(f.home, "Library", "Messages", "chat.db"), "SECRET=messages\n")
	write(filepath.Join(f.home, ".zshrc"), "# owner's\n")
	write(filepath.Join(f.home, "repos", "mirror.git", "ESCAPED-list"), "SECRET=mirror-fix\n")
	write(filepath.Join(f.home, ".config", "git", "credentials"), "https://x:SECRET=xdg-git-cred@x\n")
	write(filepath.Join(f.home, ".gitconfig"), "[user]\n\tname = owner\n")
	write(filepath.Join(f.state, "token"), "SECRET=tack-token\n")
	f.ownerGoCache = filepath.Join(f.home, "Library", "Caches", "go-build")
	f.ownerModCache = filepath.Join(f.home, "go", "pkg", "mod")
	os.MkdirAll(f.ownerGoCache, 0o755)
	os.MkdirAll(f.ownerModCache, 0o755)
	f.cfg = Config{Workspace: f.ws, Temp: dir(), GoCache: dir(), Home: f.home, Tack: []string{f.state}}
	f.env = []string{"HOME=" + f.home, "TMPDIR=" + f.cfg.Temp, "GOCACHE=" + f.cfg.GoCache, "PATH=" + os.Getenv("PATH"),
		"GOTOOLCHAIN=local", "GOFLAGS=-mod=mod", "GOPATH=" + filepath.Join(f.home, "go"), "STATE=" + f.state}
	f.cfg.Read = ReadPaths(f.env, "", f.home)
	return f
}

// run runs script with /bin/sh in the workspace: under cfg's profile, or with no sandbox when cfg is nil.
func (f fixture) run(cfg *Config, script string) string {
	var cmd *exec.Cmd
	if cfg == nil {
		cmd = exec.Command("/bin/sh", "-c", script)
	} else {
		cmd = Command(*cfg, "/bin/sh", "-c", script)
	}
	cmd.Dir, cmd.Env = f.ws, f.env
	out, _ := cmd.CombinedOutput()
	return string(out)
}

func (f fixture) without(sections ...string) *Config {
	c := f.cfg
	c.Without = sections
	return &c
}

// TestEscapes: each escape gets out with no sandbox (so the test can see it), is stopped by the full profile, and gets
// out again with only its own section removed, so that section is what stops it.
func TestEscapes(t *testing.T) {
	f := setup(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "ESCAPED-network") }))
	url := "http://" + ln.Addr().String() + "/"

	cases := []struct {
		name     string
		sections []string // removing these together lets the escape out
		script   string
		mark     string
	}{
		{"osascript runs AppleScript", secs(SecLaunch), `/usr/bin/osascript -e 'return "ESCAPED-" & "osascript"'`, "ESCAPED-osascript"},
		{"launchctl reaches launchd", secs(SecLaunch), `/bin/launchctl print-disabled gui/$(id -u) >/dev/null && echo ESCAPED-launchctl`, "ESCAPED-launchctl"},
		{"open reaches LaunchServices", secs(SecLaunch), `/usr/bin/open -a NoSuchAppForTackEscapeTest 2>&1`, "Unable to find application"},
		{"lsappinfo, not on the exec list, reaches LaunchServices", secs(SecLaunch), `/usr/bin/lsappinfo front`, "ASN:"},
		{"hdiutil reaches disk images", secs(SecMount), `/usr/bin/hdiutil info >/dev/null && echo ESCAPED-hdiutil`, "ESCAPED-hdiutil"},
		{"network to a local server", secs(SecNetwork), `/usr/bin/curl -s -m 5 ` + url, "ESCAPED-network"},
		{"reading tack's token", secs(SecTack + "+" + SecRead), `cat "$STATE/token"`, "tack-token"},
		{"writing into tack's state", secs(SecTack + "+" + SecWrite), `echo x > "$STATE/approve" && echo ESCAPED-tack-write`, "ESCAPED-tack-write"},
		{"writing a git hook", secs(SecLater), `echo x > .git/hooks/pre-commit && echo ESCAPED-hook`, "ESCAPED-hook"},
		{"writing git config", secs(SecLater), `git config core.fsmonitor /evil && echo ESCAPED-gitconfig`, "ESCAPED-gitconfig"},
		{"writing .claude settings", secs(SecLater), `mkdir -p .claude && echo x > .claude/settings.json && echo ESCAPED-claude`, "ESCAPED-claude"},
		{"writing .vscode tasks", secs(SecLater), `mkdir -p .vscode && echo x > .vscode/tasks.json && echo ESCAPED-vscode`, "ESCAPED-vscode"},
		{"writing .mcp.json", secs(SecLater), `echo x > .mcp.json && echo ESCAPED-mcp`, "ESCAPED-mcp"},
		{"writing a shell startup file", secs(SecLater + "+" + SecWrite), `echo x >> "$HOME/.zshrc" && echo ESCAPED-zshrc`, "ESCAPED-zshrc"},
		{"planting in the owner's Go build cache", secs(SecWrite), `echo x > "$HOME/Library/Caches/go-build/planted" && echo ESCAPED-gocache`, "ESCAPED-gocache"},
		{"planting in the owner's module cache", secs(SecWrite), `echo x > "$HOME/go/pkg/mod/planted" && echo ESCAPED-modcache`, "ESCAPED-modcache"},
		{"reading .env", secs(SecSecrets), `cat .env`, "ws-dotenv"},
		{"reading an ssh key", secs(SecSecrets + "+" + SecRead), `cat "$HOME/.ssh/id_test"`, "ssh-key"},
		{"reading gh's token", secs(SecSecrets + "+" + SecRead), `cat "$HOME/.config/gh/hosts.yml"`, "gh-token"},
		{"reading a keychain file", secs(SecSecrets + "+" + SecRead), `cat "$HOME/Library/Keychains/login.keychain-db"`, "SECRET=keychain"},
		{"listing the keychain with security", secs(SecSecrets), `/usr/bin/security list-keychains && echo ESCAPED-security`, "ESCAPED-security"},
		{"reading git credentials", secs(SecSecrets + "+" + SecRead), `cat "$HOME/.git-credentials"`, "SECRET=git-cred"},
		{"reading .netrc", secs(SecSecrets + "+" + SecRead), `cat "$HOME/.netrc"`, "SECRET=netrc"},
		{"reading docker's config", secs(SecSecrets + "+" + SecRead), `cat "$HOME/.docker/config.json"`, "SECRET=docker"},
		{"reading Messages", secs(SecSecrets + "+" + SecRead), `cat "$HOME/Library/Messages/chat.db"`, "SECRET=messages"},
		{"reading git's credentials under .config/git, which git may read", secs(SecSecrets), `cat "$HOME/.config/git/credentials"`, "SECRET=xdg-git-cred"},
		{"reading another repository outside the workspace", secs(SecRead), `cat "$HOME/repos/mirror.git/ESCAPED-list"`, "SECRET=mirror-fix"},
		{"listing another repository outside the workspace", secs(SecRead), `ls "$HOME/repos/mirror.git"`, "ESCAPED-list"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if out := f.run(nil, tc.script); !strings.Contains(out, tc.mark) {
				t.Fatalf("with no sandbox the escape did not get out, so this test cannot see it: %q", out)
			}
			if out := f.run(&f.cfg, tc.script); strings.Contains(out, tc.mark) {
				t.Errorf("the profile let it out: %q", out)
			}
			if out := f.run(f.without(tc.sections...), tc.script); !strings.Contains(out, tc.mark) {
				t.Errorf("with %v removed it still did not get out, so they are not what stops it: %q", tc.sections, out)
			}
			// A case read also stops: with the home and tack's state readable, as [sandbox] read could make a parent
			// of them, its own section still stops it, and removing only that section lets it out.
			if own := slices.DeleteFunc(slices.Clone(tc.sections), func(s string) bool { return s == SecRead }); len(own) > 0 && len(own) < len(tc.sections) {
				open := f.cfg
				open.Read = append(slices.Clone(f.cfg.Read), f.home, f.state)
				if out := f.run(&open, tc.script); strings.Contains(out, tc.mark) {
					t.Errorf("with the home readable, %v did not stop it: %q", own, out)
				}
				open.Without = own
				if out := f.run(&open, tc.script); !strings.Contains(out, tc.mark) {
					t.Errorf("with the home readable and %v removed, it still did not get out: %q", own, out)
				}
			}
			// Leave nothing behind for the next case.
			os.Remove(filepath.Join(f.ws, ".git", "hooks", "pre-commit"))
			exec.Command("git", "-C", f.ws, "config", "--unset", "core.fsmonitor").Run()
		})
	}
}

// TestDaemonSocketIsOutOfReach: a command cannot connect to the daemon's socket, through which it could approve its own
// requests or lift a halt (DESIGN §6.6, "tack itself is out of reach"). The socket is in tack's state directory, made
// short under /tmp as a socket path must be (internal/daemon.MaxSocketPath). It gets out with no sandbox and is
// stopped by the profile. Sections network and tack each stop it alone, so a loopback allowance in network cannot
// reopen it; with both removed it gets out.
func TestDaemonSocketIsOutOfReach(t *testing.T) {
	f := setup(t)
	state, err := os.MkdirTemp("/tmp", "tack-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(state)
	if state, err = filepath.EvalSymlinks(state); err != nil { // Seatbelt matches real paths: /tmp is /private/tmp
		t.Fatal(err)
	}
	sock := filepath.Join(state, "daemon.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Write([]byte("ESCAPED-daemon-sock\n"))
			c.Close()
		}
	}()
	f.cfg.Tack = append(f.cfg.Tack, state)
	script := `/usr/bin/nc -U ` + sock + ` </dev/null 2>&1`
	const mark = "ESCAPED-daemon-sock"
	if out := f.run(nil, script); !strings.Contains(out, mark) {
		t.Fatalf("with no sandbox the connection did not get through, so this test cannot see it: %q", out)
	}
	if out := f.run(&f.cfg, script); strings.Contains(out, mark) {
		t.Errorf("the profile let a command connect to the daemon's socket: %q", out)
	}
	for _, alone := range []string{SecNetwork, SecTack} {
		if out := f.run(f.without(alone), script); strings.Contains(out, mark) {
			t.Errorf("with only %s removed the connection got through, so the other does not stop it alone: %q", alone, out)
		}
	}
	if out := f.run(f.without(SecNetwork, SecTack), script); !strings.Contains(out, mark) {
		t.Errorf("with %s and %s removed the connection still did not get through, so they are not what stops it: %q", SecNetwork, SecTack, out)
	}
}

// TestWorkStillRuns: what a coding session needs works inside the profile: go test with tack's own cache, git, and
// writes to the workspace and the session's temp directory.
func TestWorkStillRuns(t *testing.T) {
	f := setup(t)
	for _, script := range []string{
		`go version`,
		`go test ./... && echo OK-gotest`,
		`git status --short && git add x_test.go && echo OK-git`,
		`echo hello > note.txt && cat note.txt`,
		`echo t > "$TMPDIR/scratch" && cat "$TMPDIR/scratch"`,
	} {
		out := f.run(&f.cfg, script)
		if strings.Contains(out, "Operation not permitted") || strings.Contains(out, "not permitted") ||
			strings.Contains(out, "FAIL") {
			t.Errorf("%q inside the profile: %s", script, out)
		}
		t.Logf("%s → %s", script, strings.TrimSpace(out))
	}
	if out := f.run(&f.cfg, `go test ./... && echo OK-gotest`); !strings.Contains(out, "OK-gotest") {
		t.Errorf("go test did not pass inside the profile: %s", out)
	}
}

// secs splits "a+b" into the sections whose removal together opens an escape.
func secs(s string) []string { return strings.Split(s, "+") }

// detach starts, under cfg's profile (or none when cfg is nil), a process that leaves the command's process group the
// way it is told, clears its environment, closes its standard files and sleeps, and returns its pid once it is running.
// The command itself is killed by process group, as the shell's cancel does, so only the detached process is left.
func (f fixture) detach(t *testing.T, cfg *Config, how string) int {
	t.Helper()
	pidFile := filepath.Join(f.ws, "detached-"+strings.ReplaceAll(how, " ", "-")+".pid")
	os.Remove(pidFile)
	leave := map[string]string{
		"setsid":  `POSIX::setsid() or die "setsid: $!";`,
		"setpgid": `POSIX::setpgid(0, 0) or die "setpgid: $!";`,
	}[how]
	script := `exit if fork; ` + leave + ` open(my $p, ">", $ARGV[0]) or die; print $p $$; close $p; ` +
		`POSIX::close($_) for 0..9; exec "/usr/bin/env", "-i", "/bin/sleep", "300";`
	args := []string{"/usr/bin/perl", "-MPOSIX", "-e", script, pidFile}
	var cmd *exec.Cmd
	if cfg == nil {
		cmd = exec.Command(args[0], args[1:]...)
	} else {
		cmd = Command(*cfg, args[0], args[1:]...)
	}
	cmd.Dir, cmd.Env = f.ws, f.env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("starting the detacher: %v: %s", err, out)
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) // the group kill the shell does on cancel
	for range 200 {
		if b, err := os.ReadFile(pidFile); err == nil && len(b) > 0 {
			pid, err := strconv.Atoi(string(b))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { syscall.Kill(pid, syscall.SIGKILL) })
			if !alive(pid) {
				t.Fatalf("the detached process %d is not running after the group kill", pid)
			}
			return pid
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the detached process never wrote %s", pidFile)
	return 0
}

// alive reports whether pid is running now.
func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

// gone reports whether pid has ended, waiting up to two seconds for a killed one to be reaped.
func gone(pid int) bool {
	for range 200 {
		if !alive(pid) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// TestDetachedProcessesAreSwept is halt's escape test (DESIGN §6.6: "halt reaches everything they started"). A command
// can call setsid() or setpgid() and leave the process group the shell kills on cancel. It gets out of a group kill with
// no sandbox and inside the profile alike. A sweep of the command's mark, or of its session's, catches it; with section
// mark removed, the sweep cannot find it and it gets out again.
func TestDetachedProcessesAreSwept(t *testing.T) {
	f := setup(t)
	session := Target{Mark: NewMark("s"), Since: time.Now()}
	for _, how := range []string{"setsid", "setpgid"} {
		t.Run(how, func(t *testing.T) {
			f.detach(t, nil, how) // with no sandbox, the group kill misses it: the escape this test watches for

			cmd := Target{Mark: NewMark("c"), Since: time.Now()}
			cfg := f.cfg
			cfg.Group, cfg.Mark = session.Mark, cmd.Mark
			pid := f.detach(t, &cfg, how)
			if r, err := Sweep(cmd, true); err != nil || r.Killed != 0 || len(r.Kill) != 1 || r.Kill[0].PID != pid || !alive(pid) {
				t.Errorf("a dry run must list the process and kill nothing: %+v, err %v, alive %v", r, err, alive(pid))
			}
			if r, err := Sweep(cmd, false); err != nil || r.Killed != 1 || !gone(pid) {
				t.Errorf("the command's sweep: %+v, err %v; want the one detached process killed", r, err)
			}

			noMark := cfg
			noMark.Without = []string{SecMark}
			pid = f.detach(t, &noMark, how)
			if r, err := Sweep(cmd, false); err != nil || len(r.Kill) != 0 || r.Killed != 0 {
				t.Errorf("with section mark removed, the sweep selected %+v, err %v", r, err)
			}
			if !alive(pid) {
				t.Errorf("with section mark removed the process %d was still found, so the section is not what marks it", pid)
			}

			pid = f.detach(t, &cfg, how)
			if r, err := Sweep(session, false); err != nil || r.Killed != 1 || !gone(pid) {
				t.Errorf("the session's sweep, as tack halt runs it: %+v, err %v", r, err)
			}
		})
	}
}

// TestSweepRefusesAProcessOlderThanItsTarget: a process carrying the mark that started before the target's start time
// means something is wrong, so the sweep kills nothing and lists it. The process is checked alive, and the sweep sends
// no signal.
func TestSweepRefusesAProcessOlderThanItsTarget(t *testing.T) {
	f := setup(t)
	cfg := f.cfg
	cfg.Group, cfg.Mark = NewMark("s"), NewMark("c")
	pid := f.detach(t, &cfg, "setsid")
	time.Sleep(10 * time.Millisecond)
	r, err := Sweep(Target{Mark: cfg.Mark, Since: time.Now()}, false)
	if err != nil || r.Refused == "" || r.Killed != 0 || len(r.Older) != 1 || r.Older[0].PID != pid {
		t.Fatalf("sweep %+v, err %v", r, err)
	}
	if !alive(pid) {
		t.Fatal("a refused sweep killed the process")
	}
}

// TestSweepTouchesNothingElse: a sweep kills only its own target's processes: never an unsandboxed process, nor one in
// another session's sandbox.
func TestSweepTouchesNothingElse(t *testing.T) {
	f := setup(t)
	mine, other := Target{Mark: NewMark("s"), Since: time.Now()}, Target{Mark: NewMark("s"), Since: time.Now()}
	otherCfg := f.cfg
	otherCfg.Group, otherCfg.Mark = other.Mark, NewMark("c")
	plain := f.detach(t, nil, "setsid")
	neighbour := f.detach(t, &otherCfg, "setsid")
	if r, err := Sweep(mine, false); err != nil || len(r.Kill) != 0 || r.Killed != 0 {
		t.Fatalf("a session with none of its own running selected %+v, err %v", r, err)
	}
	if !alive(plain) || !alive(neighbour) {
		t.Fatalf("a sweep killed what was not its own: unsandboxed alive %v, other session alive %v", alive(plain), alive(neighbour))
	}
	if r, err := Sweep(other, false); err != nil || r.Killed != 1 || !gone(neighbour) {
		t.Fatalf("the other session's sweep: %+v, err %v", r, err)
	}
	if !alive(plain) {
		t.Fatal("a session's sweep killed an unsandboxed process")
	}
}

// Config.Read ([sandbox] read) opens one directory and nothing beside it: a checkpoint under it is readable, its
// sibling is not, and with it left out the checkpoint is not readable either.
func TestReadAddsOnlyItsDirectory(t *testing.T) {
	f := setup(t)
	models, other := filepath.Join(f.home, "models"), filepath.Join(f.home, "other")
	os.MkdirAll(models, 0o755)
	os.MkdirAll(other, 0o755)
	os.WriteFile(filepath.Join(models, "ckpt.gguf"), []byte("OK-checkpoint\n"), 0o644)
	os.WriteFile(filepath.Join(other, "note"), []byte("SECRET=beside\n"), 0o644)
	with := f.cfg
	with.Read = append(append([]string{}, f.cfg.Read...), models)
	if out := f.run(&with, `cat "$HOME/models/ckpt.gguf"`); !strings.Contains(out, "OK-checkpoint") {
		t.Errorf("a [sandbox] read directory is not readable: %q", out)
	}
	if out := f.run(&with, `cat "$HOME/other/note"`); strings.Contains(out, "SECRET=beside") {
		t.Errorf("a directory beside the [sandbox] read one is readable: %q", out)
	}
	if out := f.run(&f.cfg, `cat "$HOME/models/ckpt.gguf"`); strings.Contains(out, "OK-checkpoint") {
		t.Errorf("without [sandbox] read the checkpoint is readable: %q", out)
	}
}

// A [sandbox] read directory given through a symlink is readable, since the profile writes its real path.
func TestReadThroughASymlink(t *testing.T) {
	f := setup(t)
	target := filepath.Join(f.home, "store", "models")
	os.MkdirAll(target, 0o755)
	os.WriteFile(filepath.Join(target, "ckpt.gguf"), []byte("OK-linked\n"), 0o644)
	link := filepath.Join(f.home, "models")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	c := f.cfg
	c.Read = append(append([]string{}, f.cfg.Read...), link)
	if out := f.run(&c, `cat "$HOME/models/ckpt.gguf"`); !strings.Contains(out, "OK-linked") {
		t.Errorf("a [sandbox] read symlink is not readable: %q", out)
	}
}
