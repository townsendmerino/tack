package perms

import (
	"os"
	"os/exec"
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

// TestGitRunsUnderARuleOnlyWhereItFindsTheWorkspacesOwnRepository: the file tools refuse a .git path but not HEAD, objects/, refs/ and a
// config written elsewhere, nor a gitfile (a .git file naming a gitdir:) in a directory the model made. Git run there uses another
// repository, whose config runs programs (core.fsmonitor, diff.external, core.pager). The test asks git, as the code does, over a real
// repository, and over each way a model can make git take another one.
func TestGitRunsUnderARuleOnlyWhereItFindsTheWorkspacesOwnRepository(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git on PATH")
	}
	root := t.TempDir()
	if out, err := exec.Command(git, "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	mk := func(rel string) string { p := filepath.Join(root, rel); os.MkdirAll(p, 0o755); return p }
	w := func(rel, body string) {
		os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0o755)
		os.WriteFile(filepath.Join(root, rel), []byte(body), 0o644)
	}
	// ev: a directory with the shape of a repository and a config that runs a program
	mk("ev/objects")
	mk("ev/refs")
	w("ev/HEAD", "ref: refs/heads/x\n")
	w("ev/config", "[core]\n\tbare = true\n\tfsmonitor = id\n")
	mk("ev/sub")
	w("gf/.git", "gitdir: ../ev\n")   // a gitfile
	mk("inv/.git")                    // an empty .git: not a repository, git goes on up
	w("file/.git", "not a gitfile\n") // ditto
	os.Symlink("../ev", filepath.Join(mk("sl"), ".git"))
	mk("cd")
	w("cd/HEAD", "ref: refs/heads/x\n")
	w("cd/commondir", "../ev\n")
	mk("plain/deep")
	for name, c := range map[string]struct {
		dir  string
		want bool
	}{
		"the root": {root, true}, "a plain subdirectory": {filepath.Join(root, "plain", "deep"), true}, "an empty .git is skipped": {filepath.Join(root, "inv"), true},
		"a .git file that is not a gitfile (git fails)": {filepath.Join(root, "file"), false},
		"the look-alike": {filepath.Join(root, "ev"), false}, "under the look-alike": {filepath.Join(root, "ev", "sub"), false},
		"a gitfile to it": {filepath.Join(root, "gf"), false}, "a symlinked .git": {filepath.Join(root, "sl"), false}, "a commondir": {filepath.Join(root, "cd"), false},
	} {
		if got := GitFindsOwnersRepo(git, c.dir, root); got != c.want {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
	if GitFindsOwnersRepo(git, t.TempDir(), t.TempDir()) {
		t.Error("a root with no repository")
	}
	e := allowEngine(t)
	e.Root, e.Path = root, filepath.Dir(git)+string(os.PathListSeparator)+os.Getenv("PATH")
	e.SessionDir = filepath.Join(root, "ev")
	if v := decide(e, `git status`); v.Action != Ask || !strings.Contains(strings.Join(v.Reasons, ";"), "own repository") {
		t.Errorf("git status in the look-alike: %s %v", v.Action, v.Reasons)
	}
	e.SessionDir = filepath.Join(root, "gf")
	if v := decide(e, `git status`); v.Action != Ask {
		t.Errorf("git status through a gitfile: %s", v.Action)
	}
	e.SessionDir = root
	if v := decide(e, `git status`); v.Action != Allow {
		t.Errorf("git status in the real repository: %s %v", v.Action, v.Reasons)
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

// TestEnvInFrontOfGoOrGitIsHeldToTheSameList: env sets variables and clears the environment for the program after it, outside the shell's own
// assignment syntax, so it is read the same way. This holds under a rule for env itself too.
func TestEnvInFrontOfGoOrGitIsHeldToTheSameList(t *testing.T) {
	e := allowEngine(t)
	r, _ := ParseRule("env")
	e.Rules = append(e.Rules, r)
	for _, cmd := range []string{`env HOME=x git status`, `command env HOME=x git status`, `env -i git status`, `env -u HOME git status`, `env GOROOT=./fake go build .`,
		`env GIT_CONFIG_COUNT=1 git status`, `env GIT_CONFIG_PARAMETERS=x git status`, `env GIT_CONFIG_KEY_0=core.fsmonitor GIT_CONFIG_VALUE_0=id git status`, `env -S 'x' git status`} {
		if v := decide(e, cmd); v.Action != Ask {
			t.Errorf("%q: %s; it must ask", cmd, v.Action)
		}
	}
	for _, cmd := range []string{`env GOOS=linux go build .`, `env ls`} {
		if v := decide(e, cmd); v.Action != Allow {
			t.Errorf("%q: %s %v", cmd, v.Action, v.Reasons)
		}
	}
}

// TestNothingACoveredCommandWritesCanMakeGitUseWhatAModelWrote: a rule for a writer (mkdir, cp, a redirect) does not cover one that names
// a .git entry in any letter case, at any depth: a .git file naming a gitdir:, or a config there, is how a model makes git run its program.
func TestNothingACoveredCommandWritesCanMakeGitUseWhatAModelWrote(t *testing.T) {
	e := allowEngine(t)
	for _, w := range []string{"mkdir", "mv", "cp", "ln", "touch", "tee", "echo", "sed", "cat"} {
		r, _ := ParseRule(w)
		e.Rules = append(e.Rules, r)
	}
	for _, cmd := range []string{`mkdir .git`, `mkdir sub/.git`, `mkdir -p a/.GIT`, `mv a .git`, `cp x .git/config`, `ln -s ev .git`, `ln -s ev sub/.Git`, `touch .git`,
		`echo x > .git/config`, `echo x > .GIT/config`, `echo gitdir: ../ev > sub/.git`, `echo x >> sub/.git`, `tee .git/config`, `sed -i s/a/b/ .git/config`,
		`git status; mkdir .git`, `cd sub && echo x > .git`} {
		if v := decide(e, cmd); v.Action != Ask {
			t.Errorf("%q: %s; it must ask", cmd, v.Action)
		}
	}
	for _, cmd := range []string{`mkdir sub/gitx`, `echo x > sub/a.gitignore`, `cat .gitignore`, `cp a b`} {
		if v := decide(e, cmd); v.Action != Allow {
			t.Errorf("%q: %s %v", cmd, v.Action, v.Reasons)
		}
	}
}

// TestGoWritesThroughASymlinkedDirectoryAreRefused: -o into a directory that is a symlink out of the tree, or to .git, asks.
func TestGoWritesThroughASymlinkedDirectoryAreRefused(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	os.MkdirAll(filepath.Join(root, ".git"), 0o755)
	os.MkdirAll(filepath.Join(root, "real"), 0o755)
	os.Symlink(outside, filepath.Join(root, "out"))
	os.Symlink(filepath.Join(root, ".git"), filepath.Join(root, "g"))
	for _, p := range []string{"out/x", "g/hooks", "out/sub/x"} {
		if GoOutputEscapes(root, root, []string{"build", "-o", p}) == "" {
			t.Errorf("-o %s is covered", p)
		}
	}
	for _, p := range []string{"real/x", "x", "newdir/x"} {
		if r := GoOutputEscapes(root, root, []string{"build", "-o", p}); r != "" {
			t.Errorf("-o %s: %s", p, r)
		}
	}
}
