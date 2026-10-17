//go:build darwin && sandboxtest

package shell

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
		Wrap: func(c *exec.Cmd) *exec.Cmd { return sandbox.Wrap(cfg, c) }}
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
