//go:build darwin && sandboxtest

// The escape tests (DESIGN §6.6). They start sandbox-exec, which macOS refuses inside an already sandboxed process, so
// they carry the sandboxtest tag and run from the day-loop checker or CI, never from tack's own bash:
//
//	go test -tags sandboxtest ./internal/sandbox/
package sandbox

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
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
	write(filepath.Join(f.state, "token"), "SECRET=tack-token\n")
	f.ownerGoCache = filepath.Join(f.home, "Library", "Caches", "go-build")
	f.ownerModCache = filepath.Join(f.home, "go", "pkg", "mod")
	os.MkdirAll(f.ownerGoCache, 0o755)
	os.MkdirAll(f.ownerModCache, 0o755)
	f.cfg = Config{Workspace: f.ws, Temp: dir(), GoCache: dir(), Home: f.home, Tack: []string{f.state}}
	f.env = []string{"HOME=" + f.home, "TMPDIR=" + f.cfg.Temp, "GOCACHE=" + f.cfg.GoCache, "PATH=" + os.Getenv("PATH"),
		"GOTOOLCHAIN=local", "GOFLAGS=-mod=mod", "GOPATH=" + filepath.Join(f.home, "go"), "STATE=" + f.state}
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
		{"reading tack's token", secs(SecTack), `cat "$STATE/token"`, "tack-token"},
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
		{"reading an ssh key", secs(SecSecrets), `cat "$HOME/.ssh/id_test"`, "ssh-key"},
		{"reading gh's token", secs(SecSecrets), `cat "$HOME/.config/gh/hosts.yml"`, "gh-token"},
		{"reading a keychain file", secs(SecSecrets), `cat "$HOME/Library/Keychains/login.keychain-db"`, "SECRET=keychain"},
		{"listing the keychain with security", secs(SecSecrets), `/usr/bin/security list-keychains && echo ESCAPED-security`, "ESCAPED-security"},
		{"reading git credentials", secs(SecSecrets), `cat "$HOME/.git-credentials"`, "SECRET=git-cred"},
		{"reading .netrc", secs(SecSecrets), `cat "$HOME/.netrc"`, "SECRET=netrc"},
		{"reading docker's config", secs(SecSecrets), `cat "$HOME/.docker/config.json"`, "SECRET=docker"},
		{"reading Messages", secs(SecSecrets), `cat "$HOME/Library/Messages/chat.db"`, "SECRET=messages"},
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
			// Leave nothing behind for the next case.
			os.Remove(filepath.Join(f.ws, ".git", "hooks", "pre-commit"))
			exec.Command("git", "-C", f.ws, "config", "--unset", "core.fsmonitor").Run()
		})
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

// lanIPv4 is this machine's first non-loopback IPv4 address: a way to reach a listener without loopback and without
// the internet.
func lanIPv4(t *testing.T) string {
	t.Helper()
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLoopback() && !n.IP.IsLinkLocalUnicast() {
			return n.IP.String()
		}
	}
	t.Fatal("no non-loopback IPv4 address to test the outside network with")
	return ""
}

// serveMark serves mark over HTTP on addr until the test ends, and returns the address it listens on.
func serveMark(t *testing.T, addr, mark string) string {
	t.Helper()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, mark) }))
	return ln.Addr().String()
}

// socks5 is a SOCKS5 proxy (no authentication, CONNECT only) on loopback, standing in for ssh -D: a local listener
// that relays a command to wherever it asks.
func socks5(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				b := make([]byte, 262)
				if _, err := io.ReadFull(c, b[:2]); err != nil {
					return
				}
				io.ReadFull(c, b[:b[1]])
				c.Write([]byte{5, 0})
				if _, err := io.ReadFull(c, b[:4]); err != nil || b[1] != 1 {
					return
				}
				var host string
				switch b[3] {
				case 1:
					io.ReadFull(c, b[:4])
					host = net.IP(b[:4]).String()
				case 3:
					io.ReadFull(c, b[:1])
					n := int(b[0])
					io.ReadFull(c, b[:n])
					host = string(b[:n])
				default:
					return
				}
				io.ReadFull(c, b[:2])
				port := int(b[0])<<8 | int(b[1])
				up, err := net.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
				if err != nil {
					c.Write([]byte{5, 1, 0, 1, 0, 0, 0, 0, 0, 0})
					return
				}
				defer up.Close()
				c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
				go io.Copy(up, c)
				io.Copy(c, up)
			}(c)
		}
	}()
	return ln.Addr().String()
}

// TestLoopbackEscapes (ADR-005): with Config.Loopback, the profile a go test command gets, every listener that was
// there before the command stays out of reach, plain or a SOCKS proxy to elsewhere, on loopback or through the LAN
// address; so do another host (outsideHost) and UDP. Each escape gets out with
// no sandbox, is stopped by the full profile, and gets out again with only its own sections removed.
func TestLoopbackEscapes(t *testing.T) {
	f := setup(t)
	f.cfg.Loopback = true
	lan := lanIPv4(t)
	outside := outsideHost(t)
	os.MkdirAll(filepath.Join(f.ws, "out"), 0o755)
	os.WriteFile(filepath.Join(f.ws, "out", "out_test.go"), []byte(`package out

import (
	"fmt"
	"net"
	"os"
	"testing"
	"time"
)

func TestDialOut(t *testing.T) {
	c, err := net.DialTimeout("tcp", os.Getenv("OUT"), 5*time.Second)
	if err != nil {
		t.Log(err)
		return
	}
	defer c.Close()
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	b := make([]byte, 8)
	if n, _ := c.Read(b); n > 0 {
		fmt.Printf("ESCAPED-outside %q\n", b[:n])
	}
}
`), 0o644)
	local := serveMark(t, "127.0.0.1:0", "ESCAPED-loopback-listener")
	local6 := serveMark(t, "[::1]:0", "ESCAPED-loopback6-listener")
	wide := serveMark(t, ":0", "ESCAPED-off-loopback")
	_, widePort, _ := net.SplitHostPort(wide)
	far := serveMark(t, ":0", "ESCAPED-through-socks")
	_, farPort, _ := net.SplitHostPort(far)
	proxy := socks5(t)
	udp, err := net.ListenPacket("udp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	_, udpPort, _ := net.SplitHostPort(udp.LocalAddr().String())
	go func() {
		b := make([]byte, 64)
		for {
			n, from, err := udp.ReadFrom(b)
			if err != nil {
				return
			}
			udp.WriteTo(append([]byte("ESCAPED-udp-"), b[:n]...), from)
		}
	}()

	cases := []struct {
		name     string
		sections []string
		script   string
		mark     string
	}{
		{"a loopback listener started before the command", secs(SecListeners), `/usr/bin/curl -s -m 5 http://` + local + `/`, "ESCAPED-loopback-listener"},
		{"an IPv6 loopback listener started before the command", secs(SecListeners), `/usr/bin/curl -s -m 5 http://` + local6 + `/`, "ESCAPED-loopback6-listener"},
		{"a SOCKS proxy started before the command", secs(SecListeners), `/usr/bin/curl -s -m 5 --socks5 ` + proxy + ` http://` + lan + `:` + farPort + `/`, "ESCAPED-through-socks"},
		{"a listener started before the command, through the LAN address", secs(SecListeners), `/usr/bin/curl -s -m 5 http://` + lan + `:` + widePort + `/`, "ESCAPED-off-loopback"},
		{"an outside host from a go test command", secs(SecNetwork), `OUT=` + outside + ` go test -count=1 -v ./out/`, "ESCAPED-outside"},
		{"UDP off loopback", secs(SecNetwork), `printf x | /usr/bin/nc -u -w 2 ` + lan + ` ` + udpPort, "ESCAPED-udp-x"},
		{"UDP on loopback", secs(SecNetwork), `printf y | /usr/bin/nc -u -w 2 127.0.0.1 ` + udpPort, "ESCAPED-udp-y"},
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
		})
	}
}

// TestLoopbackWorkRuns (ADR-005): with Config.Loopback, a test that serves on loopback and calls itself passes in the
// sandbox, over IPv4 (httptest's pattern) and IPv6 (ken's internal/modelfetch binds [::1]:0); without it, it fails, so
// Loopback is what lets it run.
func TestLoopbackWorkRuns(t *testing.T) {
	f := setup(t)
	os.MkdirAll(filepath.Join(f.ws, "lb"), 0o755)
	os.WriteFile(filepath.Join(f.ws, "lb", "lb_test.go"), []byte(`package lb

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServeAndCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "ok") }))
	defer srv.Close()
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
}

func TestIPv6SelfConnect(t *testing.T) {
	ln, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		if c, err := ln.Accept(); err == nil {
			c.Close()
		}
	}()
	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
}
`), 0o644)
	script := `go test -count=1 ./lb/ && echo OK-loopback`
	if out := f.run(&f.cfg, script); strings.Contains(out, "OK-loopback") {
		t.Fatalf("without Loopback the loopback tests passed, so this test cannot show Loopback is what lets them: %q", out)
	}
	on := f.cfg
	on.Loopback = true
	if out := f.run(&on, script); !strings.Contains(out, "OK-loopback") {
		t.Fatalf("with Loopback the loopback tests failed: %q", out)
	}
}

// TestLoopbackGap_ListenerStartedDuringCommand shows ADR-005's accepted gap: a listener started outside the sandbox
// while a command runs is not on that command's deny-list, so the command reaches it.
func TestLoopbackGap_ListenerStartedDuringCommand(t *testing.T) {
	f := setup(t)
	f.cfg.Loopback = true
	portFile := filepath.Join(f.cfg.Temp, "port")
	cmd := Command(f.cfg, "/bin/sh", "-c", `i=0; while [ ! -s "$TMPDIR/port" ] && [ $i -lt 100 ]; do sleep 0.1; i=$((i+1)); done; `+
		`/usr/bin/curl -s -m 5 "http://127.0.0.1:$(cat "$TMPDIR/port")/"`)
	cmd.Dir, cmd.Env = f.ws, f.env
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond) // the profile, and its deny-list, were fixed when the command started
	addr := serveMark(t, "127.0.0.1:0", "REACHED-later-listener")
	_, port, _ := net.SplitHostPort(addr)
	os.WriteFile(portFile, []byte(port), 0o644)
	cmd.Wait()
	if !strings.Contains(out.String(), "REACHED-later-listener") {
		t.Fatalf("the gap did not show: a listener started during the command was not reached: %q", out.String())
	}
}

// outsideHost is a TCP address on another machine that answers on connect: TACK_ESCAPE_OUTSIDE, or nobara's ssh port
// from the owner's ssh config. With neither, the test fails: an escape test that cannot reach outside proves nothing.
func outsideHost(t *testing.T) string {
	t.Helper()
	if a := os.Getenv("TACK_ESCAPE_OUTSIDE"); a != "" {
		return a
	}
	out, _ := exec.Command("ssh", "-G", "nobara").Output()
	for _, line := range strings.Split(string(out), "\n") {
		if h, ok := strings.CutPrefix(line, "hostname "); ok && h != "nobara" {
			return net.JoinHostPort(h, "22")
		}
	}
	t.Fatal("no outside host: set TACK_ESCAPE_OUTSIDE to host:port, or have nobara in the ssh config")
	return ""
}

// TestNoLoopbackWithoutIt (ADR-005): a command not given Loopback, as every command but go test is not
// (TestOnlyGoTestIsOfferedLoopback), cannot reach even a listener started after it began, which no deny-list holds;
// given Loopback, it can (the accepted gap), so Loopback is what keeps it out.
func TestNoLoopbackWithoutIt(t *testing.T) {
	f := setup(t)
	for _, loopback := range []bool{false, true} {
		cfg := f.cfg
		cfg.Loopback = loopback
		os.Remove(filepath.Join(cfg.Temp, "port"))
		cmd := Command(cfg, "/bin/sh", "-c", `i=0; while [ ! -s "$TMPDIR/port" ] && [ $i -lt 100 ]; do sleep 0.1; i=$((i+1)); done; `+
			`/usr/bin/curl -s -m 5 "http://127.0.0.1:$(cat "$TMPDIR/port")/"`)
		cmd.Dir, cmd.Env = f.ws, f.env
		var out strings.Builder
		cmd.Stdout, cmd.Stderr = &out, &out
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(300 * time.Millisecond)
		_, port, _ := net.SplitHostPort(serveMark(t, "127.0.0.1:0", "REACHED-later-listener"))
		os.WriteFile(filepath.Join(cfg.Temp, "port"), []byte(port), 0o644)
		cmd.Wait()
		if got := strings.Contains(out.String(), "REACHED-later-listener"); got != loopback {
			t.Errorf("Loopback %v: reached a listener started during the command: %v (%q)", loopback, got, out.String())
		}
	}
}

// inboundHost is an ssh host on another machine that can try to connect back to this one: TACK_ESCAPE_INBOUND_FROM,
// or nobara. Without one the test fails: it cannot show what another machine reaches.
func inboundHost(t *testing.T) string {
	t.Helper()
	if h := os.Getenv("TACK_ESCAPE_INBOUND_FROM"); h != "" {
		return h
	}
	if out, _ := exec.Command("ssh", "-G", "nobara").Output(); strings.Contains(string(out), "\nhostname ") &&
		!strings.Contains(string(out), "\nhostname nobara\n") {
		return "nobara"
	}
	t.Fatal("no machine to connect from: set TACK_ESCAPE_INBOUND_FROM to an ssh host, or have nobara in the ssh config")
	return ""
}

// TestLoopbackNotReachableFromAnotherMachine (ADR-005): a go test command's server, listening on all addresses and on
// each of this machine's own addresses, is not reachable from another machine. With no sandbox another machine reaches
// at least one, so the test can see the escape.
//
// It fails with the per-port profile: Seatbelt's localhost covers every address of the machine, so the profile lets
// the server listen on all of them, and Seatbelt checks inbound only at listen, by local address (2026-10-21: nobara
// connected over Tailscale). This is why 4.3 is held (docs/design/DECISIONS.md, ADR-005).
func TestLoopbackNotReachableFromAnotherMachine(t *testing.T) {
	f := setup(t)
	from := inboundHost(t)
	var mine []string
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLoopback() && !n.IP.IsLinkLocalUnicast() {
			mine = append(mine, n.IP.String())
		}
	}
	listen := []string{"0.0.0.0:0"}
	for _, a := range mine {
		listen = append(listen, a+":0")
	}
	os.MkdirAll(filepath.Join(f.ws, "srv"), 0o755)
	os.WriteFile(filepath.Join(f.ws, "srv", "srv_test.go"), []byte(`package srv

import (
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

func TestListen(t *testing.T) {
	p, _ := os.Create(os.Getenv("TMPDIR") + "/ports.tmp")
	for _, a := range strings.Fields(os.Getenv("ADDRS")) {
		ln, err := net.Listen("tcp", a)
		if err != nil {
			fmt.Println("LISTEN-REFUSED", a, err)
			continue
		}
		fmt.Fprintf(p, "%s %d\n", a, ln.Addr().(*net.TCPAddr).Port)
		go func(ln net.Listener) {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				fmt.Println("ACCEPTED", c.RemoteAddr())
				c.Close()
			}
		}(ln)
	}
	p.Close()
	os.Rename(os.Getenv("TMPDIR")+"/ports.tmp", os.Getenv("TMPDIR")+"/ports")
	time.Sleep(10 * time.Second)
}
`), 0o644)
	reached := func(cfg *Config) (int, string) {
		os.Remove(filepath.Join(f.cfg.Temp, "ports"))
		script := `ADDRS="` + strings.Join(listen, " ") + `" go test -count=1 -v ./srv/`
		var cmd *exec.Cmd
		if cfg == nil {
			cmd = exec.Command("/bin/sh", "-c", script)
		} else {
			cmd = Command(*cfg, "/bin/sh", "-c", script)
		}
		cmd.Dir, cmd.Env = f.ws, f.env
		var out strings.Builder
		cmd.Stdout, cmd.Stderr = &out, &out
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		var ports []byte
		for i := 0; i < 600 && len(ports) == 0; i++ {
			time.Sleep(100 * time.Millisecond)
			ports, _ = os.ReadFile(filepath.Join(f.cfg.Temp, "ports"))
		}
		var tries strings.Builder
		for _, line := range strings.Split(strings.TrimSpace(string(ports)), "\n") {
			fields := strings.Fields(line)
			if len(fields) != 2 {
				continue
			}
			hosts := mine
			if h, _, _ := net.SplitHostPort(fields[0]); h != "0.0.0.0" {
				hosts = []string{h}
			}
			for _, h := range hosts {
				fmt.Fprintf(&tries, "timeout 3 bash -c 'exec 3<>/dev/tcp/%s/%s' 2>/dev/null && echo connected %s:%s\n", h, fields[1], h, fields[1])
			}
		}
		ssh := exec.Command("ssh", "-o", "BatchMode=yes", from, "bash", "-s")
		ssh.Stdin = strings.NewReader(tries.String())
		got, _ := ssh.Output()
		cmd.Wait()
		return strings.Count(string(got), "connected "), string(got) + out.String()
	}
	if n, out := reached(nil); n == 0 {
		t.Fatalf("with no sandbox %s reached none of the listeners, so this test cannot see the escape: %s", from, out)
	}
	cfg := f.cfg
	cfg.Loopback = true
	if n, out := reached(&cfg); n > 0 {
		t.Errorf("a go test command's server was reached from %s %d times: %s", from, n, out)
	}
}
