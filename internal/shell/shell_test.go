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
	"github.com/townsendmerino/tack/internal/sandbox"
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
		s.Wrap, s.Sweep, s.Group = pretendSandbox, pretendSweep, pretendGroup
	}
	return s
}

// pretendSandbox stands in for the sandbox in tests of the run-time check; confinement itself is tested by
// internal/sandbox's escape tests and TestShellInsideTheSandbox (build tag sandboxtest).
func pretendSandbox(c *exec.Cmd, _, _ string) *exec.Cmd { return c }

func pretendSweep(sandbox.Target) (sandbox.Report, error) { return sandbox.Report{}, nil }

var pretendGroup = sandbox.Target{Mark: "s-test", Since: time.Unix(1, 0)}

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
// not know about, or a sandbox with no sweep for what a command detaches, refuses to run anything.
func TestEngineAndSandboxMustAgree(t *testing.T) {
	s := session(t, true)
	s.Wrap = nil
	if _, err := s.Run(context.Background(), "echo hi", time.Second); err == nil || !strings.Contains(err.Error(), "has no sandbox") {
		t.Fatalf("sandboxed engine, no sandbox: %v", err)
	}
	s = session(t, false)
	s.Wrap, s.Sweep, s.Group = pretendSandbox, pretendSweep, pretendGroup
	if _, err := s.Run(context.Background(), "echo hi", time.Second); err == nil {
		t.Fatal("a sandbox the engine does not know about ran")
	}
	s = session(t, true)
	s.Sweep = nil
	if _, err := s.Run(context.Background(), "echo hi", time.Second); err == nil || !strings.Contains(err.Error(), "needs Sweep and a Group") {
		t.Fatalf("a sandboxed shell with no sweep ran: %v", err)
	}
	s = session(t, true)
	s.Group = sandbox.Target{}
	if _, err := s.Run(context.Background(), "echo hi", time.Second); err == nil || !strings.Contains(err.Error(), "a Group") {
		t.Fatalf("a sandboxed shell with no session group ran: %v", err)
	}
}

// destroyRepo is session's workspace as a git repository with one committed file, changed since, so a discard shows.
func destroyRepo(t *testing.T, s *Session) (file string) {
	t.Helper()
	root := s.Engine.Root
	file = filepath.Join(root, "work.txt")
	os.WriteFile(file, []byte("committed\n"), 0o644)
	for _, args := range [][]string{{"init", "-q"}, {"add", "work.txt"}, {"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-qm", "x"}} {
		c := exec.Command("git", args...)
		c.Dir = root
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	os.WriteFile(file, []byte("a day's work\n"), 0o644)
	return file
}

func runBy(t *testing.T, s *Session, by, cmd string) Result {
	t.Helper()
	r, err := s.Run(WithApproval(context.Background(), by), cmd, 30*time.Second)
	if err != nil {
		t.Fatalf("%q: %v", cmd, err)
	}
	return r
}

// What the parse can't see is stopped when it runs (DESIGN §6.6, "Checked again as it runs"), in auto too, where the
// sandbox would let it through: a day's work survives, and a directory.
func TestDestroyingWorkStopsAtRunTime(t *testing.T) {
	for _, by := range []string{perms.ByMode, perms.ByRule} {
		t.Run(by, func(t *testing.T) {
			s := session(t, true, "git", "rm", "env", "sh") // so only everyMode can stop these
			file := destroyRepo(t, s)
			os.MkdirAll(filepath.Join(s.Engine.Root, "src", "deep"), 0o755)
			os.WriteFile(filepath.Join(s.Engine.Root, "CLAUDE.md"), []byte("orders\n"), 0o644)
			for _, cmd := range []string{
				`how=--hard; git reset $how`,
				`set -- reset --hard; git "$@"`,
				`d=.; git -C $d reset --hard`,
				`eval "git reset --hard"`,
				`d=src; rm -rf $d`,
				`rm -rf s*`,
				`p=git; $p clean -fd`,
				`x=rm; env $x -rf src`,
				`how="-c 'git reset --hard'"; eval sh $how`,
				`f=CLAUDE.md; echo overwritten > $f`,
				`x=0; HUSKY=$x git status`,
			} {
				r := runBy(t, s, by, cmd)
				if !strings.Contains(r.Stopped, "asks in every mode") || r.Exit == 0 {
					t.Errorf("%s: not stopped by the rule: %+v", cmd, r)
				}
			}
			if b, _ := os.ReadFile(file); string(b) != "a day's work\n" {
				t.Errorf("the work is gone: %q", b)
			}
			if _, err := os.Stat(filepath.Join(s.Engine.Root, "src", "deep")); err != nil {
				t.Errorf("the directory is gone: %v", err)
			}
			if b, _ := os.ReadFile(filepath.Join(s.Engine.Root, "CLAUDE.md")); string(b) != "orders\n" {
				t.Errorf("the instruction file changed: %q", b)
			}
			// What isn't on the list runs. Expanded or not, ordinary git and a temp directory go through.
			tmp, err := os.MkdirTemp("", "tack-destroy")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.RemoveAll(tmp) })
			for _, cmd := range []string{`how=--soft; git reset $how HEAD`, `git status`, `d=` + tmp + `; rm -rf $d`, `d=node_modules; rm -rf $d`} {
				if r := runBy(t, s, by, cmd); r.Stopped != "" {
					t.Errorf("%s: stopped: %s", cmd, r.Stopped)
				}
			}
			if _, err := os.Stat(tmp); err == nil {
				t.Errorf("rm -rf of the temp directory did not run")
			}
		})
	}
}

// A user who was asked approved that command, and it runs, whatever it expands to: they were told it could not be seen.
// Nothing the parse asks about is rejected once approved.
func TestApprovedCommandsRun(t *testing.T) {
	for _, cmd := range []string{
		`git reset --hard`,
		`git status; how=--hard; git reset $how`,
		`eval "git reset --hard"`,
		`p=git; $p reset --hard`,
		`set -- reset --hard; git "$@"`,
	} {
		s := session(t, true) // no rule covers git, or the check of what approval saw would pass on the rule
		file := destroyRepo(t, s)
		if len(s.Engine.AlwaysAsksIn(cmd)) == 0 {
			t.Fatalf("%s did not ask", cmd)
		}
		if r := runBy(t, s, perms.ByUser, cmd); r.Stopped != "" || r.Exit != 0 {
			t.Fatalf("%s, approved, did not run: %+v", cmd, r)
		}
		if b, _ := os.ReadFile(file); string(b) != "committed\n" {
			t.Fatalf("%s, approved, did not happen: %q", cmd, b)
		}
	}
	s := session(t, true)
	protected := filepath.Join(s.Engine.Root, "CLAUDE.md")
	if err := os.WriteFile(protected, []byte("orders\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := runBy(t, s, perms.ByUser, `echo new 1<>CLAUDE.md`); r.Stopped != "" {
		t.Fatalf("an approved <> redirect: %+v", r)
	}
}

// A user's approval covers what they were asked about. A command the parse saw nothing wrong with was approved for another
// reason, so what only shows when it runs still stops it: here a path that exists by then.
func TestApprovalDoesNotCoverWhatNothingAskedAbout(t *testing.T) {
	s := session(t, true, "git")
	destroyRepo(t, s)
	cmd := `echo x > new.txt; git add new.txt; git checkout new.txt`
	if asks := s.Engine.AlwaysAsksIn(cmd); len(asks) != 0 {
		t.Fatalf("it asked: %v", asks)
	}
	if r := runBy(t, s, perms.ByUser, cmd); !strings.Contains(r.Stopped, "asks in every mode") {
		t.Fatalf("approved for another reason, it ran: %+v", r)
	}
}

// A reason saying something can't be seen covers that thing when it shows: a redirection's target, a variable's value.
func TestApprovalCoversWhatItSaidItCouldNotSee(t *testing.T) {
	for _, cmd := range []string{`f=CLAUDE.md; echo new > $f`, `x=0; HUSKY=$x git status`, `x=0; HUSKY=$x env git status`,
		`how=--soft; env git reset $how`, `d=sub; env rm -rf $d`, `t=5; timeout $t git status`, `how=--hard; command git reset $how`, `d=sub; for x in a; do rm -rf $d; done`,
		`d=sub; cd $d; echo y > y.txt`} {
		s := session(t, true)
		destroyRepo(t, s)
		if len(s.Engine.AlwaysAsksIn(cmd)) == 0 {
			t.Fatalf("%s did not ask", cmd)
		}
		if r := runBy(t, s, perms.ByUser, cmd); r.Stopped != "" || r.Exit != 0 {
			t.Errorf("%s, approved, did not run: %+v", cmd, r)
		}
	}
}

// An approval covers the reasons it showed, and no others. Approved for HUSKY=0 in the session, or for one --no-verify, a command
// that only shows it destroys work when it runs is still stopped, and the checks of what approval saw stay on.
func TestApprovalCoversOnlyWhatItShowed(t *testing.T) {
	for _, c := range []struct{ setup, cmd, want string }{
		{`export HUSKY=0`, `echo x > new.txt; git add new.txt; git checkout new.txt`, "asks in every mode"},
		{``, `git commit --no-verify -qm x --allow-empty; echo x > new.txt; git add new.txt; git checkout new.txt`, "asks in every mode"},
		{``, `git reset --hard; ln -s src build; rm -rf build`, "asks in every mode"},
		{``, `git commit --no-verify -qm x --allow-empty; F=--upload-pack; git fetch $F x y`, "runs another program"},
		{``, `rm -rf $d; echo x > new.txt; git add new.txt; git checkout new.txt`, "asks in every mode"},
		{``, `d=node_modules; rm -rf $d; ln -s src build; rm -rf build`, "asks in every mode"},
		{``, `F=--upload-pack=evil; git pull $F origin main`, "runs another program"},
		{``, `F=-exec; find . $F /bin/echo {} +`, "runs another program"},
		{``, `git commit --no-verify -qm "avoid -exec" --allow-empty; F=-exec; find . $F /bin/echo {} +`, "runs another program"},
		// The same reason, or the same file name, from another statement.
		{``, `git checkout -- work.txt; echo x > b.txt; git add b.txt; git checkout b.txt`, "asks in every mode"},
		{``, `echo a >> .github/workflows/ci.yml; ln -s .github/workflows/ci.yml ci.yml; echo bad > ci.yml`, "asks in every mode"},
		{``, `d=sub; (cd $d && echo a > out.txt); ln -s CLAUDE.md out.txt; echo b > out.txt`, "asks in every mode"},
	} {
		s := session(t, true)
		destroyRepo(t, s)
		os.MkdirAll(filepath.Join(s.Engine.Root, ".github", "workflows"), 0o755)
		os.WriteFile(filepath.Join(s.Engine.Root, "CLAUDE.md"), []byte("orders\n"), 0o644)
		os.MkdirAll(filepath.Join(s.Engine.Root, "src"), 0o755)
		if c.setup != "" {
			if r := runBy(t, s, perms.ByUser, c.setup); r.Stopped != "" {
				t.Fatalf("%s: %+v", c.setup, r)
			}
		}
		if len(s.Engine.AlwaysAsksIn(c.cmd)) == 0 {
			t.Fatalf("%s did not ask", c.cmd)
		}
		if r := runBy(t, s, perms.ByUser, c.cmd); !strings.Contains(r.Stopped, c.want) {
			t.Errorf("%s: %+v, want it stopped: %s", c.cmd, r, c.want)
		}
	}
}

// An approved export that turns hooks off does not wedge the session: each later command that runs a program asks, an
// approved one runs, and unset, which runs none, gets out in any mode.
func TestSessionHooksOffAsksWithoutWedging(t *testing.T) {
	s := session(t, true, "git")
	destroyRepo(t, s)
	if r := runBy(t, s, perms.ByUser, `export HUSKY=0`); r.Stopped != "" {
		t.Fatalf("export: %+v", r)
	}
	if len(s.Engine.AlwaysAsksIn(`git status`)) == 0 {
		t.Fatal("git status did not ask with HUSKY=0 in the session")
	}
	if r := runBy(t, s, perms.ByMode, `git status`); r.Stopped == "" {
		t.Fatal("auto ran git with HUSKY=0 in the session")
	}
	if r := runBy(t, s, perms.ByUser, `git status`); r.Stopped != "" || r.Exit != 0 {
		t.Fatalf("approved, it did not run: %+v", r)
	}
	if asks := s.Engine.AlwaysAsksIn(`unset HUSKY`); len(asks) != 0 {
		t.Fatalf("unset HUSKY asks: %v", asks)
	}
	if r := runBy(t, s, perms.ByMode, `unset HUSKY`); r.Stopped != "" {
		t.Fatalf("unset: %+v", r)
	}
	if asks := s.Engine.AlwaysAsksIn(`git status`); len(asks) != 0 {
		t.Fatalf("after unset, git status asks: %v", asks)
	}
	if r := runBy(t, s, perms.ByMode, `git status`); r.Stopped != "" {
		t.Fatalf("after unset, auto stopped: %+v", r)
	}
}

// The library's pair for rm: $HOME stops and $TMPDIR runs, which only the expanded path can tell. The home path does not
// exist, so a failure here costs nothing.
func TestRmByExpandedPathAtRunTime(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	s := session(t, true, "rm")
	if r := runBy(t, s, perms.ByMode, `rm -rf $HOME/tack-destroy-test-no-such-dir`); r.Stopped == "" {
		t.Fatalf("$HOME was not stopped: %+v", r)
	}
	if r := runBy(t, s, perms.ByMode, `rm -rf $TMPDIR/tack-destroy-test-no-such-dir`); r.Stopped != "" {
		t.Fatalf("$TMPDIR was stopped: %s", r.Stopped)
	}
}

// A hook-disabling value that only shows at run time stops whatever program it is handed to, not only git.
func TestHooksOffStopsAnyProgramAtRunTime(t *testing.T) {
	s := session(t, true, "git", "env", "sh")
	destroyRepo(t, s)
	for _, cmd := range []string{`x=0; HUSKY=$x git status`, `x=0; HUSKY=$x env git status`, `x=0; HUSKY=$x sh -c "git status"`,
		`x=1; HUSKY_SKIP_HOOKS=$x env git status`} {
		if r := runBy(t, s, perms.ByMode, cmd); r.Stopped == "" {
			t.Errorf("%s: not stopped: %+v", cmd, r)
		}
	}
	if r := runBy(t, s, perms.ByMode, `x=1; HUSKY=$x env git status`); r.Stopped != "" {
		t.Errorf("HUSKY=1 was stopped: %s", r.Stopped)
	}
}

// A user who approved a command that changes directory first approved what they were shown: it runs, in that directory,
// and from the directory an earlier command left the session in.
func TestApprovalHoldsAfterCd(t *testing.T) {
	s := session(t, true)
	root := s.Engine.Root
	script := "#!/bin/sh\necho flushed\n"
	if err := os.WriteFile(filepath.Join(root, "sub", "manage.py"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []string{`cd sub && ./manage.py flush`, `./manage.py flush`} { // the first leaves the session in sub
		if asks := s.Engine.AlwaysAsksIn(cmd); len(asks) == 0 {
			t.Fatalf("%s did not ask", cmd)
		}
		if r := runBy(t, s, perms.ByUser, cmd); r.Stopped != "" || r.Exit != 0 {
			t.Fatalf("%s, approved, did not run: %+v", cmd, r)
		}
	}
	os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte("orders\n"), 0o644)
	if r := runBy(t, s, perms.ByUser, `cd .. && echo new > CLAUDE.md`); r.Stopped != "" {
		t.Fatalf("an approved redirect after cd: %+v", r)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "CLAUDE.md")); string(b) != "new\n" {
		t.Fatalf("the approved redirect did not happen: %q", b)
	}
}

// What a flag that runs another program needs: to have been shown, in the command the user approved or in code they were shown.
// Only a flag that appears when it runs stops.
func TestApprovedRunFlagShown(t *testing.T) {
	for _, cmd := range []string{`p=find; $p . -maxdepth 0 -exec /bin/echo {} +`, `eval "find . -maxdepth 0 -exec /bin/echo {} +"`} {
		s := session(t, true)
		if r := runBy(t, s, perms.ByUser, cmd); r.Stopped != "" || r.Exit != 0 {
			t.Errorf("%s, approved, did not run: %+v", cmd, r)
		}
	}
}

// HUSKY=0 in the session asks for every command that runs a program, seen or not, so an approved one runs.
func TestSessionHooksReachUnresolvedPrograms(t *testing.T) {
	s := session(t, true)
	destroyRepo(t, s)
	if r := runBy(t, s, perms.ByUser, `export HUSKY=0`); r.Stopped != "" {
		t.Fatalf("export: %+v", r)
	}
	for _, cmd := range []string{`p=git; $p status`, `git status`} {
		if len(s.Engine.AlwaysAsksIn(cmd)) == 0 {
			t.Errorf("%s did not ask", cmd)
		}
		if r := runBy(t, s, perms.ByUser, cmd); r.Stopped != "" || r.Exit != 0 {
			t.Errorf("%s, approved, did not run: %+v", cmd, r)
		}
	}
	// A program that doesn't resolve from where the parse stands: it's in sub, where cd $d goes.
	if err := os.WriteFile(filepath.Join(s.Engine.Root, "sub", "run.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := `d=sub; cd $d && ./run.sh`
	if got := s.Engine.AlwaysAsksIn(cmd); !strings.Contains(strings.Join(got, ";"), "HUSKY=0") {
		t.Errorf("a program it can't resolve: %v", got)
	}
	if r := runBy(t, s, perms.ByUser, cmd); r.Stopped != "" || r.Exit != 0 {
		t.Errorf("%s, approved, did not run: %+v", cmd, r)
	}
}

// A program that doesn't exist yet when the parse reads it runs if the user approved that command, as shown.
func TestApprovedProgramThatAppearsWhenItRuns(t *testing.T) {
	for _, cmd := range []string{`d=sub; cd $d && ./run.sh`, `mkdir -p made; printf '#!/bin/sh\n' > made/run; chmod +x made/run; made/run`} {
		s := session(t, true)
		if err := os.WriteFile(filepath.Join(s.Engine.Root, "sub", "run.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		if r := runBy(t, s, perms.ByUser, cmd); r.Stopped != "" || r.Exit != 0 {
			t.Errorf("%s, approved, did not run: %+v", cmd, r)
		}
	}
}

// That pass is the user's, for what they were shown. A rule doesn't see a program that wasn't there, and it stops.
func TestRuleDoesNotCoverAProgramThatAppears(t *testing.T) {
	s := session(t, true, "mkdir", "chmod")
	cmd := `mkdir -p made; printf '#!/bin/sh\n' > made/run; chmod +x made/run; made/run`
	if r := runBy(t, s, perms.ByRule, cmd); !strings.Contains(r.Stopped, "was not approved") {
		t.Fatalf("%+v", r)
	}
}

// A shell handed code on its stdin stops at run time too, in case the parse didn't see the pipe, and one with nothing fed to it runs.
func TestShellFedAtRunTime(t *testing.T) {
	s := session(t, true, "sh", "env")
	if err := os.WriteFile(filepath.Join(s.Engine.Root, "script.sh"), []byte("exit 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []string{`echo x | sh script.sh`, `echo x | env sh script.sh`, `sh script.sh <<< x`} {
		if r := runBy(t, s, perms.ByMode, cmd); !strings.Contains(r.Stopped, "asks in every mode") {
			t.Errorf("%s: %+v", cmd, r)
		}
	}
	if r := runBy(t, s, perms.ByMode, `sh script.sh`); r.Stopped != "" {
		t.Errorf("a named script with nothing fed to it: %+v", r)
	}
	if r := runBy(t, s, perms.ByUser, `echo x | sh script.sh`); r.Stopped != "" {
		t.Errorf("approved: %+v", r)
	}
}

// Approved, it runs: what the parse asked about, however it spelled the path or set the variable.
func TestApprovedRedirectsAndVarsRun(t *testing.T) {
	for _, cmd := range []string{`d=sub; cd $d; echo x > ./ci.yml`, `f=./out.txt; echo x > $f`, `printf -v HUSKY 0; export HUSKY; git status`} {
		s := session(t, true)
		destroyRepo(t, s)
		if len(s.Engine.AlwaysAsksIn(cmd)) == 0 {
			t.Fatalf("%s did not ask", cmd)
		}
		if r := runBy(t, s, perms.ByUser, cmd); r.Stopped != "" || r.Exit != 0 {
			t.Errorf("%s, approved, did not run: %+v", cmd, r)
		}
	}
}
