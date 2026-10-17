//go:build darwin && sandboxtest

package shell

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/perms"
	"github.com/townsendmerino/tack/internal/sandbox"
)

// TestShellInsideTheSandbox runs the real child under the real profile. A program writing outside the workspace is
// not a redirection, so the run-time check lets it start and the kernel stops the write; network is refused; and
// the shell still works inside.
func TestShellInsideTheSandbox(t *testing.T) {
	t.Setenv("TACK_SHELL_CHILD", "1")
	dir := func() string { d, _ := filepath.EvalSymlinks(t.TempDir()); return d }
	ws, home, tmp, cache, outside := dir(), dir(), dir(), dir(), dir()
	cfg := sandbox.Config{Workspace: ws, Temp: tmp, GoCache: cache, Home: home}
	e := &perms.Engine{Root: ws, Path: os.Getenv("PATH"), Sandboxed: true}
	s := &Session{Exe: os.Args[0], Engine: e, Env: []string{"HOME=" + home, "TMPDIR=" + tmp, "GOCACHE=" + cache},
		Wrap:  func(c *exec.Cmd, mark string) *exec.Cmd { cfg := cfg; cfg.Mark = mark; return sandbox.Wrap(cfg, c) },
		Sweep: sandbox.Sweep}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	run := func(cmd string) Result {
		r, err := s.Run(context.Background(), cmd, 20*time.Second)
		if err != nil {
			t.Fatalf("%q: %v", cmd, err)
		}
		return r
	}
	if r := run("echo inside > note.txt && cat note.txt"); r.Output != "inside\n" || r.Stopped != "" {
		t.Fatalf("inside the workspace: %+v", r)
	}
	r := run(`/usr/bin/touch "` + filepath.Join(outside, "planted") + `"`)
	if r.Exit == 0 || !strings.Contains(r.Output, "Operation not permitted") {
		t.Fatalf("a program writing outside: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(outside, "planted")); err == nil {
		t.Fatal("the program wrote outside the workspace")
	}
	if r := run("/usr/bin/curl -s -m 5 http://" + ln.Addr().String() + "/"); r.Exit == 0 {
		t.Fatalf("network inside the sandbox: %+v", r)
	}
}

// TestCancelReachesDetachedProcesses is halt's escape test through the shell (DESIGN §6.6: "halt reaches everything
// they started"). A command forks a process that calls setsid(), clears its environment and closes its files, then
// runs past its timeout. Killing the process group misses the detached process; the sweep by the command's sandbox
// mark kills it.
func TestCancelReachesDetachedProcesses(t *testing.T) {
	t.Setenv("TACK_SHELL_CHILD", "1")
	dir := func() string { d, _ := filepath.EvalSymlinks(t.TempDir()); return d }
	ws, home, tmp, cache := dir(), dir(), dir(), dir()
	cfg := sandbox.Config{Workspace: ws, Temp: tmp, GoCache: cache, Home: home}
	detach := `/usr/bin/perl -MPOSIX -e 'exit if fork; POSIX::setsid() or die "setsid: $!"; open(my $p, ">", $ARGV[0]) or die; ` +
		`print $p $$; close $p; POSIX::close($_) for 0..9; exec "/usr/bin/env", "-i", "/bin/sleep", "300"' detached.pid; sleep 60`
	run := func(sweep func(string) (int, error)) (Result, int) {
		os.Remove(filepath.Join(ws, "detached.pid"))
		e := &perms.Engine{Root: ws, Path: os.Getenv("PATH"), Sandboxed: true}
		s := &Session{Exe: os.Args[0], Engine: e, Env: []string{"HOME=" + home, "TMPDIR=" + tmp, "GOCACHE=" + cache},
			Wrap:  func(c *exec.Cmd, mark string) *exec.Cmd { cfg := cfg; cfg.Mark = mark; return sandbox.Wrap(cfg, c) },
			Sweep: sweep}
		r, err := s.Run(context.Background(), detach, 2*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join(ws, "detached.pid"))
		if err != nil {
			t.Fatalf("the detached process never started: %+v", r)
		}
		pid, err := strconv.Atoi(string(b))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { syscall.Kill(pid, syscall.SIGKILL) })
		return r, pid
	}
	gone := func(pid int) bool {
		for range 200 {
			if syscall.Kill(pid, 0) != nil {
				return true
			}
			time.Sleep(10 * time.Millisecond)
		}
		return false
	}

	// A sweep that kills nothing leaves only the process-group kill: the detached process gets out.
	r, pid := run(func(string) (int, error) { return 0, nil })
	if r.Stopped == "" || gone(pid) {
		t.Fatalf("with the process-group kill alone the detached process did not get out, so this test cannot see it: %+v", r)
	}
	r, pid = run(sandbox.Sweep)
	if !gone(pid) {
		t.Fatalf("the detached process %d outlived the cancelled command: %+v", pid, r)
	}
	if !strings.Contains(r.Stopped, "1 process(es) it had detached") {
		t.Errorf("Stopped does not say what was killed: %q", r.Stopped)
	}
}
