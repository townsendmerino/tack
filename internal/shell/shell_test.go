package shell

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/perms"
)

// With TACK_SHELL_CHILD=1 this test binary is `tack shell`.
func TestMain(m *testing.M) {
	if os.Getenv("TACK_SHELL_CHILD") == "1" {
		os.Exit(Child(os.Stdin, os.Stdout, os.NewFile(3, "reply")))
	}
	os.Exit(m.Run())
}

// session is a shell over a fresh workspace, with the given rules, inside or outside the sandbox.
func session(t *testing.T, sandboxed bool, rules ...string) *Session {
	t.Helper()
	t.Setenv("TACK_SHELL_CHILD", "1")
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(root, "sub"), 0o755)
	os.WriteFile(filepath.Join(root, ".env"), []byte("SECRET=1\n"), 0o644)
	e := &perms.Engine{Root: root, Path: os.Getenv("PATH"), Sandboxed: sandboxed}
	for _, s := range rules {
		r, err := perms.ParseRule(s)
		if err != nil {
			t.Fatal(err)
		}
		e.Rules = append(e.Rules, r)
	}
	s := &Session{Exe: os.Args[0], Engine: e}
	if sandboxed {
		s.Wrap = pretendSandbox
	}
	return s
}

// pretendSandbox stands in for the sandbox in tests of the run-time check; confinement itself is tested by
// internal/sandbox's escape tests and TestShellInsideTheSandbox (build tag sandboxtest).
func pretendSandbox(c *exec.Cmd) *exec.Cmd { return c }

func runOK(t *testing.T, s *Session, cmd string) Result {
	t.Helper()
	r, err := s.Run(context.Background(), cmd, 10*time.Second)
	if err != nil {
		t.Fatalf("%q: %v", cmd, err)
	}
	return r
}

func TestRunsAndCarriesDirAndExports(t *testing.T) {
	s := session(t, false)
	if r := runOK(t, s, "echo hi; printf x > out.txt"); r.Exit != 0 || r.Output != "hi\n" || r.Stopped != "" {
		t.Fatalf("first: %+v", r)
	}
	if b, _ := os.ReadFile(filepath.Join(s.Engine.Root, "out.txt")); string(b) != "x" {
		t.Fatalf("redirect inside the repository: %q", b)
	}
	runOK(t, s, "cd sub && export GREETING=hello && LOCAL=1")
	r := runOK(t, s, `pwd; echo "$GREETING/$LOCAL"`)
	if r.Output != filepath.Join(s.Engine.Root, "sub")+"\nhello/\n" {
		t.Fatalf("the directory and exported variable carry over, an unexported one does not: %q", r.Output)
	}
	if r := runOK(t, s, "false"); r.Exit != 1 {
		t.Fatalf("exit status: %+v", r)
	}
}

// TestRunTimeCheck: what approval could not see stops the whole command when it appears.
func TestRunTimeCheck(t *testing.T) {
	cases := []struct {
		cmd, want, notOut string
	}{
		{`P=/bin/echo; $P leaked; echo after`, "was not approved: it appeared only when the command ran", "leaked"},
		{`F=-exec; find . -maxdepth 0 $F /bin/echo leaked {} \;; echo after`, "find -exec runs another program", "leaked"},
		{`D=..; echo leaked > $D/escape.txt; echo after`, "writes outside the repository", "after"},
		{`read X < .env; echo after`, "secrets deny-list", "after"},
	}
	for _, tc := range cases {
		s := session(t, false)
		r := runOK(t, s, tc.cmd)
		if r.Exit != 126 || !strings.Contains(r.Stopped, tc.want) || strings.Contains(r.Output, tc.notOut) ||
			strings.Contains(r.Output, "after") {
			t.Errorf("%q: %+v", tc.cmd, r)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(session(t, false).Engine.Root), "escape.txt")); err == nil {
		t.Error("a file was written outside the repository")
	}
}

// TestRulesCountAtRunTimeOnlyInTheSandbox: a program approval did not see runs if a rule covers it, but only when
// commands run inside the sandbox.
func TestRulesCountAtRunTimeOnlyInTheSandbox(t *testing.T) {
	cmd := `P=ls; $P -d .`
	if r := runOK(t, session(t, true, "ls"), cmd); r.Stopped != "" || r.Output != ".\n" {
		t.Fatalf("sandboxed with a rule: %+v", r)
	}
	if r := runOK(t, session(t, false, "ls"), cmd); !strings.Contains(r.Stopped, "was not approved") {
		t.Fatalf("unsandboxed, the rule must not count: %+v", r)
	}
}

// TestTimeoutKillsTheProcessGroup: a timeout kills the command and what it started in the background, and Run returns
// promptly.
func TestTimeoutKillsTheProcessGroup(t *testing.T) {
	s := session(t, false)
	start := time.Now()
	r, err := s.Run(context.Background(), "sleep 30 & sleep 30", 300*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if el := time.Since(start); el > 5*time.Second || !strings.Contains(r.Stopped, "timeout") {
		t.Fatalf("after %s: %+v", el, r)
	}
}

// TestLeavingTheRepositoryResetsTheDirectory: the next command starts at the root again.
func TestLeavingTheRepositoryResetsTheDirectory(t *testing.T) {
	s := session(t, false)
	if r := runOK(t, s, "cd /"); !strings.Contains(r.Output, "left the repository") {
		t.Fatalf("no notice: %q", r.Output)
	}
	if r := runOK(t, s, "pwd"); r.Output != s.Engine.Root+"\n" {
		t.Fatalf("pwd after leaving: %q", r.Output)
	}
}

// TestEnvironmentBypass is the bypass the rule exists for: an earlier command exports GOFLAGS=-toolexec=…, and then a
// rule-covered go test would run another program. Approval must ask, and if the command runs anyway as rule-allowed,
// the run-time check must stop it before go starts. In one command the user approved, the user saw the export, and it
// runs; in auto mode inside the sandbox, the sandbox is the boundary, and it runs.
func TestEnvironmentBypass(t *testing.T) {
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "go"), []byte("#!/bin/sh\necho \"go ran with GOFLAGS=$GOFLAGS\"\n"), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GOFLAGS", "")
	s := session(t, true, "go test")
	s.Engine.Path = os.Getenv("PATH")
	user := WithApproval(context.Background(), perms.ByUser)
	rule := WithApproval(context.Background(), perms.ByRule)

	if r, err := s.Run(user, "export GOFLAGS=-toolexec=/evil", 10*time.Second); err != nil || r.Exit != 0 {
		t.Fatalf("the user-approved export: %+v %v", r, err)
	}
	if v := s.Engine.Decide("ask", "bash", false, []byte(`{"command":"go test ./..."}`)); v.Action != perms.Ask ||
		!strings.Contains(strings.Join(v.Reasons, ";"), "GOFLAGS=-toolexec=/evil was set by an earlier command") {
		t.Fatalf("approval of the later go test: %s %v", v.Action, v.Reasons)
	}
	r, err := s.Run(rule, "go test ./...", 10*time.Second)
	if err != nil || !strings.Contains(r.Stopped, "would run with GOFLAGS=-toolexec=/evil") || strings.Contains(r.Output, "go ran") {
		t.Fatalf("a rule-allowed go test with the inherited GOFLAGS ran: %+v %v", r, err)
	}

	s = session(t, true, "go test")
	s.Engine.Path = os.Getenv("PATH")
	if r := runWith(t, s, rule, "export GOFLAGS=-toolexec=/evil && go test"); !strings.Contains(r.Stopped, "would run with GOFLAGS") {
		t.Fatalf("rule-allowed, in one command: %+v", r)
	}
	if r := runWith(t, s, user, "export GOFLAGS=-x && go test"); r.Stopped != "" || r.Output != "go ran with GOFLAGS=-x\n" {
		t.Fatalf("user-approved, in one command: %+v", r)
	}
	auto := WithApproval(context.Background(), perms.ByMode)
	if r := runWith(t, s, auto, "export GOFLAGS=-y && go test"); r.Stopped != "" || r.Output != "go ran with GOFLAGS=-y\n" {
		t.Fatalf("auto inside the sandbox: %+v", r)
	}
}

func runWith(t *testing.T, s *Session, ctx context.Context, cmd string) Result {
	t.Helper()
	r, err := s.Run(ctx, cmd, 10*time.Second)
	if err != nil {
		t.Fatalf("%q: %v", cmd, err)
	}
	return r
}

// TestEngineAndSandboxMustAgree: an engine that trusts a sandbox the shell does not have, or a sandbox the engine does
// not know about, refuses to run anything.
func TestEngineAndSandboxMustAgree(t *testing.T) {
	s := session(t, true)
	s.Wrap = nil
	if _, err := s.Run(context.Background(), "echo hi", time.Second); err == nil || !strings.Contains(err.Error(), "has no sandbox") {
		t.Fatalf("sandboxed engine, no sandbox: %v", err)
	}
	s = session(t, false)
	s.Wrap = pretendSandbox
	if _, err := s.Run(context.Background(), "echo hi", time.Second); err == nil {
		t.Fatal("a sandbox the engine does not know about ran")
	}
}
