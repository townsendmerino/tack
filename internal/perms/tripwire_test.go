package perms

import "testing"

// TestTheTripwireCountsOnlyShapesWithNoHonestUse: a run-a-program flag written so the analysis cannot see it, a toolchain flag value that
// names the program to run, and a configuration write that plants one. Each counts.
func TestTheTripwireCountsOnlyShapesWithNoHonestUse(t *testing.T) {
	e := allowEngine(t)
	for _, cmd := range []string{
		// a run flag behind an escape, in the forms the review gave and their kin
		`go test \-exec id`, `go test -e\xec id`, `go test $'\x2dexec' id`, `go test $'\055exec' id`, `go test $'-exec' id`, `go test -e{x,x}ec id`,
		`go test {-exec,x} id`, `find . \-exec id ';'`, `find . $'\x2dexec' id ';'`, `git \-c core.pager=id log`,
		`go test $'-toolexec' id`, `go vet \-vettool=/x .`, `go test -exec\=id .`,
		// a run word hidden in a toolchain flag's value, or in GOFLAGS
		`go build -ldflags='-linkmode=external -extld=./evil' .`, `go build -ldflags=-extld=./evil`, `go test -gcflags=-toolexec=id .`,
		`go build -asmflags='-exec id' .`, `go build -gccgoflags=-exec=id .`, `GOFLAGS=-toolexec=id go test ./...`, `export GOFLAGS='-exec=id'; go test .`,
		`GOFLAGS="-mod=mod -toolexec=id" go build .`,
		// a configuration write that runs a program later
		`git config core.fsmonitor id`, `git config --global core.fsmonitor ./x`, `git config alias.x '!id'`, `git config --local alias.y "!sh -c id"`,
		`git config core.hooksPath /tmp/hooks`, `git config core.hooksPath ../hooks`, `git config core.hooksPath ~/h`, `git config diff.external id`, `git config core.pager "sh -c id"`,
		`git config diff.x.textconv id`, `git -C . config core.fsmonitor x`, `git config set core.fsmonitor x`, `git -c a=b -C . config alias.x '!id'`, `env GOFLAGS=-toolexec=x go build .`,
		`GOFLAGS=$'-toolexec=x' go build .`, `env GOFLAGS=$'-toolexec=x' go build .`, `go env -w GOFLAGS=-toolexec=id`, `go env -w GOFLAGS='-ldflags=-extld=x'`,
	} {
		if got := e.Tripwire(cmd); len(got) == 0 {
			t.Errorf("%q does not count", cmd)
		}
	}
}

// TestTheTripwireDoesNotCountAnHonestCommand: a plain dangerous flag just asks and is not counted; nor is anything people do for good
// reasons: a static link, a pinned toolchain, an ordinary alias or editor, reading config, escapes in text that is not a flag.
func TestTheTripwireDoesNotCountAnHonestCommand(t *testing.T) {
	e := allowEngine(t)
	for _, cmd := range []string{
		`find . -exec id ';'`, `find . -delete`, `go test -exec id`, `go test -exec=id .`, `go build -toolexec=id .`, `go vet -vettool=/x .`, `git -c core.pager=id log`,
		`go build -ldflags '-extldflags=-static' .`, `go build -ldflags='-s -w -X main.v=1' .`, `go build -ldflags "-extldflags '-static'" .`, `go build -gcflags=all=-N .`,
		`GOFLAGS=-mod=mod go test ./...`, `GOFLAGS=-race go build .`, `export GOFLAGS=-count=1`, `go env -w GOTOOLCHAIN=local`, `go env -w GOPRIVATE=example.com`, `go env GOFLAGS`,
		`git config core.hooksPath .githooks`, `git config core.fsmonitor true`, `git config core.sshCommand ssh`, `go env -w CC=clang`, `go env -w GOCACHEPROG=x`, `go env -w GOFLAGS=-mod=mod`, `env GOFLAGS=-mod=mod go build .`, `git -C sub config user.name x`, `git config user.name x`, `git config core.editor vim`, `git config alias.co checkout`, `git config --get alias.x`, `git config --list`, `git config --unset core.fsmonitor`,
		`git config core.fsmonitor`, `git log --oneline`, `git status`,
		`echo \-exec`, `echo $'\x2dexec'`, `ls \-la`, `ls $'a\nb'`, `go test -run 'a\nb' .`, `go test "a b" .`, `printf '%s' "\$HOME"`, `grep \-n x file`, `ls -e{x,y}ec`,
		`go test -ldflags=-extldflag .`, `go build -ldflags '-extld' `,
	} {
		got := e.Tripwire(cmd)
		if cmd == "go build -ldflags '-extld' " { // the one case that is a run word: a bare -extld with no program is still the flag
			if len(got) == 0 {
				t.Errorf("%q should count", cmd)
			}
			continue
		}
		if len(got) != 0 {
			t.Errorf("%q counts as %v; it is honest or only asks", cmd, got)
		}
	}
}

// TestTheTripwireCountsACallOnceAndNamesNothingElse: shapes are internal names, listed once each.
func TestTheTripwireCountsACallOnceAndNamesNothingElse(t *testing.T) {
	e := allowEngine(t)
	got := e.Tripwire(`go test \-exec id; go test \-toolexec=id; git config core.fsmonitor id`)
	seen := map[string]bool{}
	for _, s := range got {
		if seen[s] {
			t.Errorf("%q listed twice in %v", s, got)
		}
		seen[s] = true
	}
	if len(got) != 2 || !seen["escaped-flag"] || !seen["config-write"] {
		t.Errorf("shapes %v", got)
	}
}

// TestDecodeCandidatesIsWhatBashGives: the decoder the tripwire reads words with.
func TestDecodeCandidatesIsWhatBashGives(t *testing.T) {
	for src, want := range map[string][]string{
		`\-exec`: {"-exec"}, `-e\xec`: {"-exec"}, `$'\x2dexec'`: {"-exec"}, `$'\055exec'`: {"-exec"}, `$'-exec'`: {"-exec"}, `'a\nb'`: {`a\nb`}, `$'a\nb'`: {"a\nb"},
		`"a\"b"`: {`a"b`}, `"a\nb"`: {`a\nb`}, `-e{x,y}ec`: {"-exec", "-eyec"}, `{-exec,x}`: {"-exec", "x"}, `a{b}`: {"a{b}"}, `a"b"'c'`: {"abc"},
	} {
		got, ok := decodeCandidates(src)
		if !ok || len(got) != len(want) {
			t.Errorf("%s: %q, %v; want %q", src, got, ok, want)
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%s: %q; want %q", src, got, want)
			}
		}
	}
	for _, src := range []string{`$HOME`, `$(id)`, `$'\q'`} {
		if _, ok := decodeCandidates(src); ok {
			t.Errorf("%s decoded", src)
		}
	}
}
