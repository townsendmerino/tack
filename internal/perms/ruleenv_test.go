package perms

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestARuleDoesNotCoverWhatGoAndGitReadFromTheEnvironment: go and git read far more of their environment than ChangesPrograms lists. A
// variable that names a toolchain root, a pkg-config, linker flags, a home or config directory or a git directory makes the command
// run what a model put in the tree, so each asks under a rule, however it is set.
func TestARuleDoesNotCoverWhatGoAndGitReadFromTheEnvironment(t *testing.T) {
	e := allowEngine(t)
	for _, cmd := range []string{
		`GOROOT=./fake go build .`, `PKG_CONFIG=./evil go build .`, `CGO_CFLAGS=-B./evil go build .`, `CGO_LDFLAGS=-fuse-ld=./x go build .`,
		`HOME=./evilhome git diff`, `XDG_CONFIG_HOME=./x git diff`, `GIT_DIR=ev git status`, `GIT_WORK_TREE=. git status`,
		`export GOROOT=./fake; go build .`, `export GOROOT=./fake && go build .`, `declare -x HOME=./h; git log`, `read HOME; git log`,
		`printf -v HOME %s ./h; git log`, `for HOME in ./h; do git log; done`, `: ${HOME:=./h}; git log`, `export $NAME=x; go build .`,
		`(GIT_DIR=ev; export GIT_DIR; git status)`, `GOOS=linux GOROOT=./fake go build .`,
	} {
		if v := decide(e, cmd); v.Action != Ask {
			t.Errorf("%q: %s; it must ask", cmd, v.Action)
		}
	}
	// Chosen from an earlier command in the session.
	e.SessionEnv = map[string]string{"HOME": "./evilhome"}
	if v := decide(e, `git log`); v.Action != Ask {
		t.Errorf("git log after an exported HOME: %s", v.Action)
	}
	e.SessionEnv = nil
	for _, cmd := range []string{`GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build .`, `export GOOS=linux; go test ./...`, `go test ./...`, `git diff`} {
		if v := decide(e, cmd); v.Action != Allow {
			t.Errorf("%q: %s %v; it only chooses a target", cmd, v.Action, v.Reasons)
		}
	}
}

// TestGitDoesNotRunUnderARuleInADirectoryThatLooksLikeARepository: the file tools refuse a .git path, but a model can write HEAD, objects/
// , refs/ and a config into any other directory, and git run there takes it for a bare repository and runs config's fsmonitor, textconv
// or pager through a shell. The shape is found as git finds it: walking up from where the command runs, a .git first wins.
func TestGitDoesNotRunUnderARuleInADirectoryThatLooksLikeARepository(t *testing.T) {
	root := t.TempDir()
	ev := filepath.Join(root, "ev")
	os.MkdirAll(filepath.Join(ev, "objects"), 0o755)
	os.MkdirAll(filepath.Join(ev, "refs"), 0o755)
	os.WriteFile(filepath.Join(ev, "HEAD"), []byte("ref: refs/heads/x\n"), 0o644)
	os.WriteFile(filepath.Join(ev, "config"), []byte("[core]\n\tfsmonitor = id\n"), 0o644)
	os.MkdirAll(filepath.Join(ev, "sub"), 0o755)
	os.MkdirAll(filepath.Join(root, "ok", ".git"), 0o755)
	os.MkdirAll(filepath.Join(root, "ok", "d"), 0o755)
	if got := BareRepoAbove(ev); got != ev {
		t.Errorf("the directory itself: %q", got)
	}
	if got := BareRepoAbove(filepath.Join(ev, "sub")); got != ev {
		t.Errorf("a directory under it: %q", got)
	}
	if got := BareRepoAbove(filepath.Join(root, "ok", "d")); got != "" {
		t.Errorf("under a .git: %q", got)
	}
	if got := BareRepoAbove(root); got != "" {
		t.Errorf("a plain directory: %q", got)
	}
	e := allowEngine(t)
	e.SessionDir = ev
	if v := decide(e, `git status`); v.Action != Ask || !strings.Contains(strings.Join(v.Reasons, ";"), "as a repository") {
		t.Errorf("git status in the look-alike: %s %v", v.Action, v.Reasons)
	}
	e.SessionDir = filepath.Join(root, "ok", "d")
	if v := decide(e, `git status`); v.Action != Allow {
		t.Errorf("git status in a real repository: %s %v", v.Action, v.Reasons)
	}
}

// TestGoWritesOnlyInsideTheWorkingTree: -o and the profile flags to any path outside it, or into .git, are not covered.
func TestGoWritesOnlyInsideTheWorkingTree(t *testing.T) {
	for _, bad := range [][]string{
		{"build", "-o", "/usr/local/bin/x", "."}, {"build", "-o=/tmp/x", "."}, {"build", "-o", "../x", "."}, {"build", "-o", "a/../../x", "."},
		{"build", "-o", ".git/hooks/pre-commit", "."}, {"build", "-o", "a/.GIT/x", "."}, {"build", "-o", "~/x", "."}, {"build", "-o", "$HOME/x", "."},
		{"test", "-c", "-o", "/x"}, {"test", "-coverprofile", "/tmp/c"}, {"test", "-cpuprofile=../c"}, {"test", "-trace", ".git/t"}, {"test", "-outputdir", "/x"},
	} {
		if r := UnsafeInvocation("go", bad); r == "" {
			t.Errorf("go %v is covered", bad)
		}
	}
	for _, ok := range [][]string{{"build", "-o", "bin/x", "."}, {"build", "-o=out", "."}, {"test", "-c", "-o", "x.test"}, {"test", "-coverprofile", "c.out", "./..."}, {"test", "-cpuprofile=cpu.out"}} {
		if r := UnsafeInvocation("go", ok); r != "" {
			t.Errorf("go %v: %s", ok, r)
		}
	}
}

// TestGitBranchTakesANameAfterAFlagThatTakesAValueOnlyAfterEquals: git reads `--abbrev newname` as the flag with no value and a
// branch to create, so a rule for git branch does not cover it. A flag that does take the next word still works.
func TestGitBranchTakesANameAfterAFlagThatTakesAValueOnlyAfterEquals(t *testing.T) {
	for _, bad := range [][]string{{"branch", "--abbrev", "newname"}, {"branch", "--abbrev=7", "newname"}, {"branch", "--sort", "committerdate", "x"}, {"tag", "--abbrev", "v9"}} {
		if r := UnsafeInvocation("git", bad); r == "" {
			t.Errorf("git %v is covered", bad)
		}
	}
	for _, ok := range [][]string{{"branch", "--list", "--abbrev=7"}, {"branch", "--list", "--sort", "committerdate"}, {"branch", "--contains", "HEAD"}, {"branch", "-a", "--list", "feat*"}, {"log", "--abbrev", "7", "-n", "3"}} {
		if r := UnsafeInvocation("git", ok); r != "" {
			t.Errorf("git %v: %s", ok, r)
		}
	}
}

// TestGoRunOfAModuleVersionAsksAndGitRemoteCommandsAsk: remote code and remote helpers are not covered.
func TestGoRunOfAModuleVersionAsksAndGitRemoteCommandsAsk(t *testing.T) {
	for _, bad := range [][]string{{"run", "example.com/x@latest"}, {"run", "-race", "example.com/x@v1.2.3", "arg"}} {
		if UnsafeInvocation("go", bad) == "" {
			t.Errorf("go %v is covered", bad)
		}
	}
	for _, ok := range [][]string{{"run", "./cmd/x", "a@b"}, {"run", "main.go"}} {
		if r := UnsafeInvocation("go", ok); r != "" {
			t.Errorf("go %v: %s", ok, r)
		}
	}
	for _, sub := range []string{"ls-remote", "fetch", "clone", "pull", "push"} {
		if UnsafeInvocation("git", []string{sub, "evil::x"}) == "" {
			t.Errorf("git %s is covered", sub)
		}
	}
}
