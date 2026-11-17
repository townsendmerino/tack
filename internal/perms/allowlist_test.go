package perms

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/townsendmerino/tack/internal/proto"
)

// allowEngine is an engine over fake go, git and find programs with the rules a session builds up with `s`: go test, build, vet, run,
// list and env; git status, diff, log, show, grep, branch, tag, remote, stash, config and rev-parse; and find on a directory.
func allowEngine(t *testing.T) *Engine {
	t.Helper()
	bin := t.TempDir()
	// git is the real one, over a real repository: the engine asks it which repository a command would use (GitFindsOwnersRepo).
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git on PATH")
	}
	for _, p := range []string{"go", "find", "id"} {
		os.WriteFile(filepath.Join(bin, p), []byte("#!/bin/sh\n"), 0o755)
	}
	root := t.TempDir()
	if out, err := exec.Command(git, "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	e := &Engine{Root: root, Path: bin + string(os.PathListSeparator) + filepath.Dir(git), Sandboxed: true}
	for _, s := range []string{"go test", "go build", "go vet", "go run", "go list", "go env", "go version", "git status", "git diff", "git log", "git show",
		"git grep", "git branch", "git tag", "git remote", "git stash", "git config", "git rev-parse", "git reset", "git commit", "git difftool", "git bisect",
		"git submodule", "git filter-branch", "git checkout", "git", "go", "find ."} {
		r, err := ParseRule(s)
		if err != nil {
			t.Fatal(err)
		}
		e.Rules = append(e.Rules, r)
	}
	return e
}

// TestARuleStillCoversWhatCannotRunAProgram: the ordinary read-only and build commands a rule exists for are covered, flags and all.
func TestARuleStillCoversWhatCannotRunAProgram(t *testing.T) {
	e := allowEngine(t)
	for _, cmd := range []string{
		"go test ./...", "go test -race -count=1 -run TestX -v ./internal/perms", "go test -timeout 30s -tags sandboxtest ./...",
		"go test -coverprofile=c.out ./...", "go test ./... -args -flag value", "go test -ldflags '-s -w' ./...", "go test -ldflags=-X=main.v=1 .",
		"go build -o bin/tack ./cmd/tack", "go build -trimpath -ldflags '-s -w -X main.version=1.2 -linkmode=internal' ./...",
		"go build -gcflags='all=-N -l' ./...", "go build -gcflags=-m=2 .", "go vet ./...", "go run . -v-not-a-go-flag", "go run main.go --name x",
		"go list -json ./...", "go list -f '{{.Dir}}' ./...", "go env", "go env GOPATH GOFLAGS", "go env -json", "go version",
		"git status", "git status -sb", "git status --porcelain=v1", "git diff", "git diff --stat HEAD~1", "git diff -U3 --name-only main...",
		"git log --oneline -n 5", "git log -5 --pretty=format:%h --since=2.days", "git show HEAD:README.md", "git grep -n TODO -- '*.go'",
		"git branch", "git branch -a", "git branch --list 'feat*'", "git tag", "git tag -l 'v1*'", "git remote -v", "git remote show origin",
		"git stash list", "git config --get user.name", "git config --list", "git rev-parse --show-toplevel", "git rev-parse --abbrev-ref HEAD",
		"find . -name '*.go' -type f", "find . -maxdepth 2 -newer go.mod -print", "find . -path ./vendor -prune -o -name x -print",
	} {
		if v := e.Decide(proto.ModeAsk, "bash", false, bash(cmd)); v.Action != Allow {
			t.Errorf("%q: %s (%v); a rule covers it", cmd, v.Action, v.Reasons)
		}
	}
}

// TestARuleNeverCoversWhatCouldRunAProgramOrChangeConfig: each of these ran a program or persisted a change that would, under the old
// denylist, and each asks now, with a rule for the program and the first argument in place: the shapes in the security review, and
// the ones in the same families. "git" and "go" rules with no argument are here too: the allowlist holds under them as well.
func TestARuleNeverCoversWhatCouldRunAProgramOrChangeConfig(t *testing.T) {
	e := allowEngine(t)
	for _, cmd := range []string{
		// go: the linker and compiler flags that run a program
		"go test -ldflags='-linkmode=external -extld=./evil' .", "go build -ldflags=-extld=./evil", "go build -ldflags '-extldflags=-x' .",
		"go build -ldflags=-r./x .", "go test -ldflags '-s -w -extld id' .", `go build -ldflags "-X main.v=a b" -ldflags "-extld=id" .`,
		"go build -gcflags=-toolexec=id .", "go build -gcflags=all=-p=x .", "go build -asmflags=-I=/x -asmflags=-Dx .", "go build -asmflags=-evil .",
		"go build -gccgoflags=-x .", "go build -ldflags '-X' .", "go build -ldflags '-X main.v=\"x' .",
		// go: flags that run a program or reach outside
		"go run . -exec x", "go run main.go --toolexec x", // the program's own arguments, which the old denylist still reads as go's: it asks
		"go test -exec id", "go test -exec=id .", "go test -toolexec=id .", "go build -toolexec id .", "go vet -vettool=/x .", "go build -overlay=o.json .",
		"go build -C .. .", "go test -modfile=other.mod .", "go build -pkgdir /x .", "go test -someunknownflag .", "go build -mod=evil .", "go build -compiler=gccgo .",
		// go: subcommands and the environment
		"go generate ./...", "go install ./...", "go get x", "go mod edit -replace a=b", "go clean -cache", "go fmt ./...", "go work use .", "go tool compile x",
		"go env -w GOFLAGS=-toolexec=id", "go env -u GOFLAGS", "go env -w=GOFLAGS=x", "go env GOFLAGS=-toolexec=id",
		// git: flags and subcommands that run a program
		"git grep -O id", "git grep --open-files-in-pager=id pat", "git grep -Oid pat", "git difftool -x id", "git difftool --extcmd=id", "git difftool --tool=x",
		"git bisect run id", "git bisect start", "git submodule foreach id", "git filter-branch --tree-filter id", "git -c core.pager=id log",
		"git -c alias.x=!id status", "git --config-env=core.pager=X log", "git -C .. status", "git --exec-path=/x status", "git --git-dir=/x status",
		"git log --output=/tmp/x", "git diff --ext-diff", "git diff --textconv", "git log --upload-pack=id", "git show --exec=id", "git diff --someunknown",
		// git: configuration that runs a program later
		"git config core.fsmonitor id", "git config alias.x '!id'", "git config --global core.pager id", "git config --unset core.pager",
		"git config --add core.hooksPath x", "git config --local user.name x", "git config", "git config -e", "git config --edit",
		// git: writes
		"git branch newname", "git branch -D x", "git branch -m a b", "git branch --set-upstream-to=x", "git tag v1", "git tag -d v1", "git tag -a v1 -m x",
		"git remote add x y", "git remote set-url x y", "git remote remove x", "git stash", "git stash drop", "git stash pop", "git reset", "git reset --soft HEAD~1",
		"git commit -m x", "git checkout main", "git checkout -- .", "git add .", "git rebase main", "git clean -n", "git fetch", "git push",
		// find: actions that run a program or write
		"find . -exec id ';'", "find . -execdir id ';'", "find . -ok id ';'", "find . -okdir id ';'", "find . -delete", "find . -fprint /tmp/x",
		"find . -fprint0 /tmp/x", "find . -fprintf /tmp/x '%p'", "find . -fls /tmp/x", "find . -evil",
	} {
		if v := e.Decide(proto.ModeAsk, "bash", false, bash(cmd)); v.Action != Ask {
			t.Errorf("%q: %s; it must ask even with a rule", cmd, v.Action)
		}
	}
}

// TestAFlagNobodyListedAsksUnderEveryRuleAndSubcommand: the allowlist is a list of what passes, so a flag that is on no list never does:
// for each program and each subcommand a rule may cover, made-up flags ask, as a short, a long and a go-style one.
func TestAFlagNobodyListedAsksUnderEveryRuleAndSubcommand(t *testing.T) {
	e := allowEngine(t)
	subs := map[string][]string{"go": {"test", "build", "vet", "run", "list", "env"}, "git": {"status", "diff", "log", "show", "grep", "branch", "tag", "remote", "stash", "config", "rev-parse"}, "find": {"."}}
	for prog, list := range subs {
		for _, sub := range list {
			for _, flag := range []string{"-Zq", "--zzz-unlisted", "-zzzunlisted", "--zzz=unlisted"} {
				cmd := prog + " " + sub + " " + flag
				if prog == "find" {
					cmd = "find . " + flag
				}
				if v := e.Decide(proto.ModeAsk, "bash", false, bash(cmd)); v.Action != Ask {
					t.Errorf("%q: %s; a flag on no list must ask", cmd, v.Action)
				}
			}
		}
	}
}

// TestAProgramWithoutAnAllowlistIsCoveredAsBefore: the allowlist is for go, git and find; a rule for another program covers what it
// covered, so the change reaches nothing else.
func TestAProgramWithoutAnAllowlistIsCoveredAsBefore(t *testing.T) {
	e := allowEngine(t)
	bin := e.Path
	os.WriteFile(filepath.Join(bin, "make"), []byte("#!/bin/sh\n"), 0o755)
	r, _ := ParseRule("make test")
	e.Rules = append(e.Rules, r)
	if v := e.Decide(proto.ModeAsk, "bash", false, bash("make test -j4 --weird-flag")); v.Action != Allow {
		t.Errorf("make test --weird-flag: %s", v.Action)
	}
	if HasAllowlist("/usr/bin/make") || !HasAllowlist("/usr/bin/go") || !HasAllowlist("git") || !HasAllowlist("/bin/find") {
		t.Error("HasAllowlist")
	}
}

// TestAutoModeIsNotNarrowedByTheAllowlist: in auto mode the sandbox is the boundary and rules do not decide, so the allowlist, which is
// about what a rule covers, changes nothing there.
func TestAutoModeIsNotNarrowedByTheAllowlist(t *testing.T) {
	e := allowEngine(t)
	if v := e.Decide(proto.ModeAuto, "bash", false, bash("go build -ldflags=-extld=id .")); v.Action != Allow {
		t.Errorf("auto: %s", v.Action)
	}
}
