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

// TestTheTripwireCountsAConfigWriteToAnyFileOrScope: git config --file, -f (also attached), --global, --system, --worktree and an option
// with a value before the key are the same write as a plain git config; the file's name is not the key.
func TestTheTripwireCountsAConfigWriteToAnyFileOrScope(t *testing.T) {
	e := allowEngine(t)
	for _, cmd := range []string{
		`git config --file x.cfg core.fsmonitor id`, `git config -f x.cfg core.fsmonitor id`, `git config --file=x.cfg core.fsmonitor id`,
		`git config -fx.cfg core.fsmonitor id`, `git config --file /tmp/c core.hooksPath /tmp/h`, `git config --file ~/.gitconfig core.pager 'sh -c id'`,
		`git config --file x.cfg alias.x '!id'`, `git config --file x.cfg diff.external id`, `git config --file x.cfg diff.x.textconv id`,
		`git -C . config --file x.cfg core.fsmonitor id`,
		`git config --system core.fsmonitor id`, `git config --worktree core.hooksPath ../h`, `git config --global core.pager 'sh -c id'`,
		`git config --local --replace-all core.fsmonitor id`, `git config --add core.pager id`,
		`git config --comment hi core.fsmonitor id`, `git config --type=bool core.fsmonitor id`, `git config --type bool core.fsmonitor id`,
		`git config --file x.cfg --comment hi core.fsmonitor id`,
	} {
		if got := e.Tripwire(cmd); len(got) == 0 {
			t.Errorf("%q does not count", cmd)
		}
	}
	for _, cmd := range []string{
		`git config --file x.cfg user.name x`, `git config --file .gitmodules submodule.a.url https://x/y`, `git config -f x.cfg --get core.fsmonitor`,
		`git config --file x.cfg --unset core.fsmonitor`, `git config --file x.cfg core.fsmonitor true`, `git config --global core.fsmonitor false`,
		`git config --file x.cfg core.hooksPath .githooks`, `git config --system user.email a@b`, `git config --worktree core.sparseCheckout true`,
		`git config --file x.cfg --list`, `git config --file x.cfg alias.co checkout`, `git config --file x.cfg core.fsmonitor`,
		`git config --file x.cfg set core.fsmonitor id`, // git refuses it: "set" is taken for the key, which has no section
	} {
		if got := e.Tripwire(cmd); len(got) != 0 {
			t.Errorf("%q counts as %v; it is honest, a read or a removal", cmd, got)
		}
	}
}

// Each form below was checked by hand against git (git writes the key) and was missed by the first version: one test per form.

// git accepts any unique prefix of a long option, so --fil is --file and --comm is --comment; an ambiguous prefix is an error in git
// and writes nothing.
func TestTheTripwireReadsUniquePrefixesOfLongOptions(t *testing.T) {
	configForms(t, []string{
		`git config --fil x.cfg core.fsmonitor id`, `git config --comm hi core.fsmonitor id`,
		`git config --typ bool core.fsmonitor id`, `git config --def x core.fsmonitor id`, `git config --glob core.fsmonitor id`,
		`git config --repl core.fsmonitor id`, `git config --file=x.cfg core.fsmonitor id`,
	}, []string{
		`git config --fil x.cfg --unset core.fsmonitor`, `git config --lis`, `git config --get-a core.fsmonitor`,
		// an ambiguous prefix is an error in git; it is counted now (TestTheTripwireCountsAPrefixItCannotResolve), as a prefix it cannot resolve fails closed
	})
}

// A bundle of short options: -z is a flag, -f takes the next word unless the value is stuck to it.
func TestTheTripwireSplitsBundledShortOptions(t *testing.T) {
	configForms(t, []string{
		`git config -zf x.cfg core.fsmonitor id`, `git config -zfx.cfg core.fsmonitor id`, `git config -z core.fsmonitor id`,
	}, []string{
		`git config -zl`, `git config -zf x.cfg --get core.fsmonitor`, `git config -zf x.cfg core.fsmonitor true`,
	})
}

// --value takes the next word, in git config and in git config set.
func TestTheTripwireSkipsTheValueOfValueOption(t *testing.T) {
	configForms(t, []string{
		`git config set --value foo core.fsmonitor id`, `git config --value foo core.fsmonitor id`, `git config --value=foo core.fsmonitor id`,
		`git config set --value foo --append core.pager id`,
	}, []string{
		`git config set --value foo core.fsmonitor true`, `git config --value foo --unset core.fsmonitor`,
	})
}

// get, list and unset are subcommands only first: after the key they are words, and `git config core.pager sh get` writes the pager.
func TestTheTripwireTakesSubcommandWordsOnlyFirst(t *testing.T) {
	configForms(t, []string{
		`git config core.pager sh get`, `git config core.pager list`, `git config core.fsmonitor unset`, `git config alias.x '!get'`,
		`git config --global core.pager get`,
	}, []string{
		`git config get core.pager`, `git config list`, `git config unset core.fsmonitor`, `git config get --all core.fsmonitor`, `git config set core.fsmonitor true`,
	})
}

// git's global options that take a separate value: it is not the subcommand. --exec-path takes a value only attached.
func TestTheTripwireSkipsTheValueOfGlobalGitOptions(t *testing.T) {
	configForms(t, []string{
		`git --config-env core.x=HOME config core.fsmonitor id`, `git --attr-source HEAD config core.fsmonitor id`,
		`git --exec-path config core.fsmonitor id`, `git --exec-path=/x config core.fsmonitor id`, `git -C . -c a=b --git-dir .git config core.fsmonitor id`,
		`git --namespace n --work-tree . config core.fsmonitor id`, `git --config-env=core.x=HOME config core.fsmonitor id`,
	}, []string{
		`git --config-env core.x=HOME config --get core.fsmonitor`, `git --attr-source HEAD status`, `git --exec-path`, `git --attr-source HEAD config core.fsmonitor true`,
	})
}

// configForms checks one group of git config forms: each counted form counts, and each of the others does not.
func configForms(t *testing.T, counted, others []string) {
	t.Helper()
	e := allowEngine(t)
	for _, cmd := range counted {
		if got := e.Tripwire(cmd); len(got) == 0 {
			t.Errorf("%q does not count", cmd)
		}
	}
	for _, cmd := range others {
		if got := e.Tripwire(cmd); len(got) != 0 {
			t.Errorf("%q counts as %v; it is a read, a removal, an honest value or an error in git", cmd, got)
		}
	}
}

// Second read of the parser (checked against git 2.53): four spellings that were missed.

// -t is --type and takes a value, alone, in a bundle and in set mode.
func TestTheTripwireTakesTheValueOfShortT(t *testing.T) {
	configForms(t, []string{
		`git config -t path core.fsmonitor /bin/id`, `git config -zt path core.fsmonitor /bin/id`, `git config set -t path core.fsmonitor /bin/id`,
		`git config -tpath core.fsmonitor id`,
	}, []string{
		`git config -t bool --get core.fsmonitor`, `git config -t path core.fsmonitor true`,
	})
}

// --no-<flag> negations, and their prefixes, are flags with no value.
func TestTheTripwireReadsNegatedOptions(t *testing.T) {
	configForms(t, []string{
		`git config --no-global core.fsmonitor id`, `git config --no-file core.fsmonitor id`, `git config --no-g core.fsmonitor id`,
		`git config set --no-value core.fsmonitor id`, `git config --no-type core.fsmonitor id`, `git config --no-comment core.fsmonitor id`,
		`git config --no-includes core.fsmonitor id`,
	}, []string{
		`git config --no-global core.fsmonitor true`, `git config --no-file --get core.fsmonitor`,
	})
}

// Set mode has fewer options than the legacy mode, so a prefix that is unique to git there (set --e, set --l) is ambiguous in the table.
// A prefix the table cannot resolve is counted when a later word names a counted key, never passed.
func TestTheTripwireCountsAPrefixItCannotResolve(t *testing.T) {
	configForms(t, []string{
		`git config set --e core.fsmonitor id`, `git config set --l core.fsmonitor id`, `git config --f x.cfg core.fsmonitor id`,
		`git config --fi x.cfg core.fsmonitor id`, `git config --g core.fsmonitor id`,
	}, []string{
		`git config --g core.sparseCheckout true`, `git config set --l user.name x`,
	})
}

// git's global --shallow-file takes a separate value, which is not the subcommand.
func TestTheTripwireSkipsTheValueOfShallowFile(t *testing.T) {
	configForms(t, []string{
		`git --shallow-file x config core.fsmonitor id`, `git -C . --shallow-file x config --global core.pager id`,
	}, []string{
		`git --shallow-file x status`, `git --shallow-file x config --get core.fsmonitor`,
	})
}

// An option the table does not know, or a short letter it does not know, fails closed: the command counts if any later word names a
// counted key, whatever the value, and does not count if none does.
func TestTheTripwireFailsClosedOnAnUnknownOption(t *testing.T) {
	configForms(t, []string{
		`git config --frobnicate core.fsmonitor true`, `git config --frobnicate x core.fsmonitor id`, `git config -q core.hooksPath ~/h`,
		`git config --future-flag=1 core.pager id`, `git config --future-flag diff.x.textconv id`, `git config --future-flag alias.x '!id'`,
		`git config --future-flag CORE.FSMONITOR id`, `git config set --future-flag core.fsmonitor id`,
	}, []string{
		`git config --frobnicate user.name x`, `git config --frobnicate core.editor vim`, `git config --future-flag alias.x checkout`,
	})
}

// An option git does not know before the subcommand may take a value; the config command behind it is found and checked closed.
func TestTheTripwireFailsClosedOnAnUnknownGlobalOption(t *testing.T) {
	configForms(t, []string{
		`git --future-opt x config core.fsmonitor id`, `git --future-opt config core.fsmonitor id`,
	}, []string{
		`git --future-opt x config user.name y`, `git --future-opt x status`, `git --no-pager log`, `git -p diff`, `git --bare config --get core.fsmonitor`,
	})
}

// git -c alias.x=config … x …: the alias is a config command, with the alias's own words in front of the rest.
func TestTheTripwireFollowsAnAliasToConfig(t *testing.T) {
	configForms(t, []string{
		`git -c alias.x=config x core.pager sh`, `git -c alias.cfg='config --global' cfg core.fsmonitor id`, `git -c alias.x=config x --file y core.fsmonitor id`,
		`git -c user.name=a -c alias.x=config x core.hooksPath /tmp/h`,
	}, []string{
		`git -c alias.x=config x user.name y`, `git -c alias.x=config x --get core.pager`, `git -c alias.x=status x`, `git -c alias.y=config x core.pager sh`,
		`git -c alias.x=config status`,
	})
}
