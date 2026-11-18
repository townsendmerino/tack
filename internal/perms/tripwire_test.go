package perms

import (
	"os/exec"
	"strings"
	"testing"
)

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
		`git config core.hooksPath .githooks`, `git config core.fsmonitor true`, `git config core.sshCommand ssh`, `go env -w CC=clang`, `go env -w GOFLAGS=-mod=mod`, `env GOFLAGS=-mod=mod go build .`, `git -C sub config user.name x`, `git config user.name x`, `git config core.editor vim`, `git config alias.co checkout`, `git config --get alias.x`, `git config --list`, `git config --unset core.fsmonitor`,
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
		`git config --frobnicate user.name x`, `git config --future-flag alias.x checkout`, `git config --frobnicate core.sparseCheckout true`,
	})
}

// The fail-closed path covers every key that is counted, the program-running ones included (a value on the allowlist, such as
// `core.editor vim`, does not matter when the command cannot be read): the first form was a pass before the program keys were counted.
func TestTheFailClosedPathCoversTheProgramKeys(t *testing.T) {
	counts(t, []string{
		`git config --frobnicate core.editor vim`, `git config --frobnicate core.sshCommand ssh`, `git config --frobnicate credential.helper store`,
		`git config --frobnicate sequence.editor nano`, `git config --frobnicate gpg.program gpg`, `git config --frobnicate gpg.ssh.defaultKeyCommand k`,
		`git config --frobnicate filter.lfs.clean 'git-lfs clean -- %f'`, `git config --frobnicate merge.x.driver true`, `git config --future-flag pager.log false`,
		`git config --frobnicate diff.x.command d`, `git config --frobnicate interactive.diffFilter f`, `git config --frobnicate core.gitProxy p`,
		`git config --frobnicate remote.origin.uploadpack u`, `git config --frobnicate remote.origin.receivepack r`, `git config --frobnicate core.alternateRefsCommand c`,
		`git config --frobnicate difftool.x.cmd d`, `git config --frobnicate mergetool.x.cmd m`, `git config --frobnicate uploadpack.packObjectsHook h`,
		`git config --frobnicate submodule.x.update rebase`, `git config --frobnicate tar.x.command c`, `git config --frobnicate trailer.t.cmd c`, `git config --frobnicate trailer.t.command c`,
		`git config --frobnicate sendemail.sendmailCmd c`, `git config --frobnicate sendemail.smtpServer host`, `git config --frobnicate browser.x.cmd c`, `git config --frobnicate man.x.path p`,
		`git config --frobnicate guitool.x.cmd c`, `git config --frobnicate mergetool.x.path p`, `git config --frobnicate difftool.x.path p`, `git config --frobnicate remote.x.vcs v`,
		`git config --frobnicate include.path x`, `git config --frobnicate includeIf.gitdir:/x/.path y`, `git config --frobnicate url.'ext::sh -c id'.insteadOf https://x/`,
		`git config --frobnicate remote.origin.url 'ext::sh -c id'`, `git --future-opt x config core.editor vim`, `git cfg core.editor vim`, `git -c alias.x=y x credential.helper store`,
	}, []string{
		`git config --frobnicate user.name x`, `git config --frobnicate remote.origin.url https://example.com/x.git`, `git config --frobnicate credential.username me`,
		`git config --frobnicate uploadpack.allowFilter true`,
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
		`git -c alias.x=config x user.name y`, `git -c alias.x=config x --get core.pager`, `git -c alias.x=status x`,
		`git -c alias.x=config status`,
	})
}

// Third read: each of these writes core.pager in real git and was not counted. When the subcommand is not config, and either an alias was
// seen on the command line or the subcommand is not a git builtin (so it may be a stored alias or an external command), the later words
// are checked for a counted key, whatever they are.
func TestTheTripwireFollowsAliasesThatCanBeConfig(t *testing.T) {
	configForms(t, []string{
		`git -c alias.X=config x core.pager sh`,                                                       // git matches alias names case-insensitively
		`git -c alias.x=y -c alias.y=config x core.pager sh`, `git -c alias.y=config x core.pager sh`, // a chain, and an alias for another name (x may be stored)
		`git --config-env=alias.x=FOO x core.pager sh`, // an alias from the environment, attached
		`git --config-env alias.x=FOO x core.pager sh`, // and as two words
		`git -c "alias.x='config'" x core.pager sh`,    // a quoted value
		`git cfg core.pager less`,                      // a stored alias: not a builtin
		`git cfg --global core.fsmonitor id`, `git frob core.hooksPath /tmp/h`, `git -c alias.x=whatever x diff.y.textconv id`,
	}, []string{
		`git status`, `git log core.pager`, `git cfg user.name x`, `git -c alias.x=status x`, `git -c alias.x=status x -s`, `git commit -m 'core.pager'`,
		`git --config-env alias.x=FOO x`, `git frob --flag value`, `git checkout main`,
	})
}

// Writing an alias whose expansion is config is itself counted: it is how a later `git cfg core.pager sh` becomes a write.
func TestTheTripwireCountsWritingAnAliasToConfig(t *testing.T) {
	configForms(t, []string{
		`git config alias.cfg config`, `git config alias.cfg 'config --global'`, `git config alias.cfg CONFIG`, `git config --global alias.Cfg 'config'`,
		`git config --file x.cfg alias.cfg config`, `git config alias.x '!id'`,
	}, []string{
		`git config alias.co checkout`, `git config alias.st 'status -s'`, `git config alias.conf 'configure'`, `git config --get alias.cfg`,
		`git config --unset alias.cfg`,
	})
}

// gitBuiltins holds git's own commands and nothing else: a name on it that is not built in can be an alias a config file defines, and a
// write through it would pass. Checked against the git that is installed, when one is.
func TestTheBuiltinListHoldsOnlyBuiltins(t *testing.T) {
	out, err := exec.Command("git", "--list-cmds=builtins").Output()
	if err != nil {
		t.Skip("no git to ask:", err)
	}
	real := map[string]bool{}
	for _, c := range strings.Fields(string(out)) {
		real[c] = true
	}
	for c := range gitBuiltins {
		if !real[c] {
			t.Errorf("%q is on gitBuiltins and is not a built-in command of this git (%d are)", c, len(real))
		}
	}
	if len(gitBuiltins) < 100 {
		t.Errorf("gitBuiltins has %d entries", len(gitBuiltins))
	}
}

// A name that is not a built-in command is an alias when no program of that name is installed: `git gui`, `git citool`, `git send-email`
// and `git scalar` are programs git ships and not builtins, so an alias of that name wins, and the alias may be config anywhere in its value.
func TestTheTripwireCountsAnAliasThatShadowsAShippedProgramAndAnAliasWithConfigAnywhere(t *testing.T) {
	configForms(t, []string{
		`git gui core.pager sh`, `git citool core.pager sh`, `git send-email core.pager sh`, `git scalar core.pager sh`, `git mergetool core.fsmonitor id`,
		`git config alias.gui '-c a=b config'`, `git config alias.x 'config'`, `git config alias.x '-c a=b config --global'`, `git config alias.x 'foo config'`,
		`git config alias.x '--no-pager config'`, `git config alias.x '-C . CONFIG'`,
	}, []string{
		`git gui`, `git send-email --to a@b`, `git config alias.x 'status -s'`, `git config alias.x 'configure'`, `git config alias.x 'log --format=config-x'`,
		`git config alias.x 'reconfig'`,
	})
}

// counts checks one group of settings that make git or the Go toolchain run a program: each counted form counts, and each honest form
// (a value on that key's own list of known values, a read, a removal) does not.
func counts(t *testing.T, counted, honest []string) {
	t.Helper()
	e := allowEngine(t)
	for _, cmd := range counted {
		if got := e.Tripwire(cmd); len(got) == 0 {
			t.Errorf("%q does not count", cmd)
		}
	}
	for _, cmd := range honest {
		if got := e.Tripwire(cmd); len(got) != 0 {
			t.Errorf("%q counts as %v; it is honest, a read or a removal", cmd, got)
		}
	}
}

// For each key that names a program, a write counts unless the value is one that key's own list names: no bare word is honest in itself
// (python3.14, just, pytest and rbash are bare words that run a repository's file).

func TestTheTripwireCountsEditorsNotOnTheEditorList(t *testing.T) {
	counts(t, []string{
		`git config core.editor ./edit.sh`, `git config core.editor 'sh -c id'`, `git config core.editor '/tmp/ed'`, `git config sequence.editor ./seq`,
		`git config --global sequence.editor 'bash -c id'`, `git config core.editor 'vim; id'`, `git config sequence.editor '!id'`, `git config core.editor 'python3 x.py'`,
		`git config core.editor 'vim -c "!./x"'`, `git config core.editor 'vim -S x.vim'`, `git config core.editor 'make -f x.mk'`, `git config core.editor '. ./x'`,
		`git config core.editor 'eval ./x'`, `git config core.editor 'exec ./x'`, `git config core.editor sh`, `git config core.editor '/opt/homebrew/bin/nvim'`,
		`git config core.editor '/usr/local/bin/vim'`, `git config core.editor 'code --wait --extra'`,
		`git config core.editor python3.14`, `git config core.editor /usr/bin/python3.12`, `git config core.editor perl5.40`, `git config core.editor gawk`,
		`git config core.editor rbash`, `git config core.editor tclsh`, `git config core.editor busybox`, `git config core.editor just`, `git config core.editor pytest`,
		`git config core.editor tox`, `git config core.editor rake`, `git config core.editor ninja`, `git config core.editor yarn`, `git config core.editor task`,
		`git config core.editor gradle`, `git config core.editor 'vim +"!./x"'`, `git config core.editor 'vim -u ./vimrc'`, `git config core.editor 'vim --cmd x'`,
		`git config core.editor 'emacs --eval x'`, `git config core.editor 'emacs -f x'`, `git config core.editor 'emacsclient -e x'`, `git config core.editor 'busybox vi'`,
	}, []string{
		`git config core.editor vim`, `git config core.editor vi`, `git config core.editor nvim`, `git config sequence.editor nano`, `git config core.editor emacs`,
		`git config core.editor emacsclient`, `git config core.editor code`, `git config core.editor subl`, `git config core.editor mate`, `git config core.editor 'code --wait'`,
		`git config core.editor 'code -w'`, `git config core.editor 'subl -n -w'`, `git config core.editor 'emacsclient -t'`, `git config core.editor 'emacs -nw'`,
		`git config core.editor /usr/bin/vim`, `git config core.editor 'vim -f'`, `git config core.editor 'mate -w'`, `git config --get sequence.editor`,
		`git config --unset core.editor`, `git config core.editor ""`,
	})
}

func TestTheTripwireCountsPagersNotOnThePagerList(t *testing.T) {
	counts(t, []string{
		`git config core.pager 'less; id'`, `git config core.pager ./p`, `git config core.pager python3.14`, `git config core.pager 'sh -c id'`, `git config core.pager just`,
		`git config core.pager 'delta --pager "sh -c id"'`, `git config core.pager 'delta --pager sh'`, `git config core.pager 'less -k ./x'`, `git config core.pager id`,
		`git config pager.log ./p`, `git config pager.diff 'sh -c id'`, `git config pager.status /tmp/p`, `git config pager.log python3.14`,
		`git config interactive.diffFilter ./f`, `git config interactive.diffFilter 'diff-highlight; id'`, `git config interactive.diffFilter 'delta --pager sh'`,
		`git config interactive.diffFilter python3`, `git config pager.log 'bat --paging=never'`,
	}, []string{
		`git config core.pager less`, `git config core.pager 'less -FRX'`, `git config core.pager more`, `git config core.pager cat`, `git config core.pager delta`,
		`git config core.pager 'delta --dark'`, `git config core.pager diff-so-fancy`, `git config core.pager bat`, `git config core.pager 'bat --plain'`,
		`git config pager.log false`, `git config pager.diff true`, `git config pager.log less`, `git config interactive.diffFilter 'delta --color-only'`,
		`git config interactive.diffFilter diff-so-fancy`, `git config --unset core.pager`, `git config --get pager.log`, `git config core.pager ""`,
	})
}

func TestTheTripwireCountsFilterDriversThatAreNotGitLfs(t *testing.T) {
	counts(t, []string{
		`git config filter.x.clean ./clean.sh`, `git config filter.x.smudge './smudge %f'`, `git config filter.x.process /tmp/p`, `git config filter.lfs.clean 'sh -c id'`,
		`git config --local filter.x.clean 'cat; id'`, `git config filter.x.clean 'go run ./tools/c'`, `git config filter.x.clean 'git -c alias.y=!./e y'`,
		`git config filter.x.smudge 'git-lfs smudge -- %f; id'`, `git config filter.x.clean 'git-lfs clean -- ./x'`, `git config filter.x.clean python3.14`,
		`git config filter.x.clean cat`, `git config filter.x.clean true`, `git config filter.x.clean just`, `git config filter.x.smudge pytest`, `git config filter.x.process rbash`,
		`git config filter.x.clean /usr/bin/python3.12`, `git config filter.lfs.clean 'git-lfs clean -- %f extra'`,
	}, []string{
		`git config filter.lfs.clean 'git-lfs clean -- %f'`, `git config filter.lfs.smudge 'git-lfs smudge -- %f'`, `git config filter.lfs.process 'git-lfs filter-process'`,
		`git config filter.lfs.process 'git-lfs filter-process --skip'`, `git config filter.lfs.required true`, `git config --unset filter.x.clean`,
		`git config --get filter.x.smudge`, `git config filter.x.clean ""`,
	})
}

// These take no program from a repository's own configuration and have no honest value: any non-empty value counts, `true` too.
func TestTheTripwireCountsTheKeysWithNoHonestValue(t *testing.T) {
	var counted []string
	for _, key := range []string{
		"merge.x.driver", "diff.x.command", "diff.external", "diff.x.textconv", "difftool.x.cmd", "mergetool.x.cmd", "difftool.x.path", "mergetool.x.path",
		"core.gitProxy", "core.alternateRefsCommand", "uploadpack.packObjectsHook", "tar.x.command", "trailer.t.cmd", "trailer.t.command", "sendemail.sendmailCmd",
		"browser.x.cmd", "browser.x.path", "man.x.cmd", "man.x.path", "guitool.x.cmd",
	} {
		for _, v := range []string{"true", "vim", "./x", "'sh -c id'", "git-upload-pack"} {
			counted = append(counted, "git config "+key+" "+v)
		}
	}
	counts(t, counted, []string{
		`git config merge.x.driver ""`, `git config diff.x.command ""`, `git config --unset merge.x.driver`, `git config --get diff.x.command`, `git config merge.x.name 'a driver'`,
		`git config merge.tool vimdiff`, `git config diff.algorithm patience`, `git config diff.x.binary true`, `git config trailer.t.key 'Reviewed-by: '`,
		`git config mergetool.keepBackup false`, `git config man.viewer man`, `git config browser.x.foo bar`,
	})
}

func TestTheTripwireCountsSubmoduleUpdateCommandsSmtpPathsAndRemoteVcs(t *testing.T) {
	counts(t, []string{
		`git config submodule.x.update '!./x'`, `git config submodule.x.update '!sh -c id'`, `git config submodule.x.update '!true'`,
		`git config sendemail.smtpServer /usr/sbin/sendmail`, `git config sendemail.smtpServer ./x`, `git config sendemail.smtpServer ~/x`,
		`git config remote.origin.vcs hg`, `git config remote.origin.vcs evil`,
	}, []string{
		`git config submodule.x.update rebase`, `git config submodule.x.update merge`, `git config submodule.x.update checkout`, `git config submodule.x.update none`,
		`git config sendemail.smtpServer smtp.example.com`, `git config remote.origin.vcs ""`, `git config submodule.x.url https://x/y`, `git config sendemail.smtpPort 587`,
	})
}

// credential.helper takes only its known values: a word that is not one runs git-credential-<word> from PATH.
func TestTheTripwireCountsCredentialHelpersNotOnTheList(t *testing.T) {
	counts(t, []string{
		`git config credential.helper python3.14`, `git config credential.helper foo`, `git config credential.helper ./helper`, `git config credential.helper '!./h'`,
		`git config credential.helper '!gh auth git-credential --evil'`, `git config credential.helper 'cache --socket ./s'`, `git config credential.helper 'store --file ./f'`,
		`git config credential.helper 'libsecret; id'`, `git config credential.helper /tmp/git-credential-store`, `git config credential.helper '!id'`,
		`git config credential.helper '!/tmp/gh auth git-credential'`, `git config credential.helper just`, `git config credential.helper pytest`,
		`git config credential.https://example.com.helper foo`, `git config credential.helper '/usr/lib/git-core/git-credential-evil'`,
		`git config credential.helper '!f() { id; }; f'`, `git config credential.helper /usr/bin/python3.12`,
	}, []string{
		`git config credential.helper cache`, `git config credential.helper 'cache --timeout=3600'`, `git config credential.helper store`, `git config credential.helper osxkeychain`,
		`git config credential.helper manager`, `git config credential.helper manager-core`, `git config credential.helper libsecret`,
		`git config credential.helper /usr/lib/git-core/git-credential-libsecret`, `git config credential.helper /usr/local/share/gcm-core/git-credential-manager`,
		`git config credential.helper '!gh auth git-credential'`, `git config credential.helper '!/usr/bin/gh auth git-credential'`,
		`git config credential.https://github.com.helper '!gh auth git-credential'`, `git config credential.https://example.com.helper manager`,
		`git config --get credential.helper`, `git config --unset credential.helper`, `git config credential.helper ""`, `git config credential.username me`,
	})
}

func TestTheTripwireCountsSshCommandsThatRunOrReadARepositoryFile(t *testing.T) {
	counts(t, []string{
		`git config core.sshCommand ./ssh-wrap`, `git config core.sshCommand 'sh -c id'`, `git config core.sshCommand '/tmp/x -i key'`, `git config core.sshCommand 'ssh; id'`,
		`git config core.sshCommand 'ssh -o ProxyCommand=./x'`, `git config core.sshCommand 'ssh -F ./cfg'`, `git config core.sshCommand 'ssh -oProxyCommand=./x host'`,
		`git config core.sshCommand 'ssh -o LocalCommand=./x -o PermitLocalCommand=yes'`, `git config core.sshCommand 'ssh -o KnownHostsCommand=./x'`,
		`git config core.sshCommand 'ssh -o proxycommand=./x'`, `git config core.sshCommand 'ssh -o PermitLocalCommand=yes'`, `git config core.sshCommand 'ssh -o Include=./c'`,
		`git config core.sshCommand '"sh" x.sh'`, `git config core.sshCommand 'FOO=1 sh x.sh'`, `git config core.sshCommand 'timeout 9 sh x.sh'`,
		`git config core.sshCommand 'ssh -i ./key'`, `git config core.sshCommand 'ssh -i /tmp/key'`, `git config core.sshCommand 'ssh -F /tmp/cfg'`,
		`git config core.sshCommand 'ssh -F ~/.ssh/../x'`, `git config core.sshCommand 'ssh -i ~/other/key'`, `git config core.sshCommand 'ssh -p 22; id'`,
		`git config core.sshCommand 'ssh -p x'`, `git config core.sshCommand 'ssh -l'`, `git config core.sshCommand 'ssh -J host'`, `git config core.sshCommand 'ssh host id'`,
		`git config core.sshCommand python3.14`, `git config core.sshCommand rbash`,
	}, []string{
		`git config core.sshCommand ssh`, `git config core.sshCommand 'ssh -o StrictHostKeyChecking=accept-new'`, `git config core.sshCommand '/usr/bin/ssh'`,
		`git config core.sshCommand 'ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null'`, `git config core.sshCommand 'ssh -oBatchMode=yes'`,
		`git config core.sshCommand 'ssh -i ~/.ssh/id_ed25519'`, `git config core.sshCommand 'ssh -F ~/.ssh/config_work'`, `git config core.sshCommand 'ssh -p 2222'`,
		`git config core.sshCommand 'ssh -l git'`, `git config core.sshCommand 'ssh -i ~/.ssh/key -o IdentitiesOnly=yes'`, `git config core.sshCommand 'ssh -q -4'`,
		`git config --get core.sshCommand`, `git config --unset core.sshCommand`, `git config core.sshCommand ""`,
	})
}

func TestTheTripwireCountsGpgAndAskpassProgramsNotOnTheirLists(t *testing.T) {
	counts(t, []string{
		`git config gpg.program ./gpg-wrap`, `git config gpg.ssh.program /tmp/sign`, `git config gpg.x509.program ~/s`, `git config gpg.openpgp.program 'sh -c id'`,
		`git config --system gpg.program ../x`, `git config gpg.program /usr/local/bin/gpg`, `git config gpg.program 'gpg --homedir ./h'`, `git config gpg.program python3.14`,
		`git config gpg.ssh.program /Applications/1Password.app/Contents/MacOS/evil`, `git config gpg.ssh.program just`,
		`git config gpg.ssh.defaultKeyCommand ./key`, `git config gpg.ssh.defaultKeyCommand 'ssh-add -L; id'`, `git config gpg.ssh.defaultKeyCommand 'ssh-add ./k'`,
		`git config core.askPass ./askpass`, `git config core.askPass ~/x`, `git config --global core.askPass ../tools/ask`, `git config core.askPass /usr/local/bin/ask`,
		`git config core.askPass 'ssh-askpass; id'`, `git config core.askPass python3`, `git config core.askPass just`,
		`git config remote.origin.uploadpack ./up`, `git config remote.origin.receivepack /tmp/rp`, `git config remote.x.uploadpack 'sh -c id'`, `git config remote.x.uploadpack python3.14`,
	}, []string{
		`git config gpg.program gpg`, `git config gpg.program gpg2`, `git config gpg.program gpgsm`, `git config gpg.ssh.program ssh-keygen`, `git config gpg.format ssh`,
		`git config gpg.program /usr/bin/gpg`, `git config gpg.ssh.program /Applications/1Password.app/Contents/MacOS/op-ssh-sign`, `git config --get gpg.program`,
		`git config gpg.ssh.defaultKeyCommand 'ssh-add -L'`, `git config core.askPass ssh-askpass`, `git config core.askPass /usr/bin/ssh-askpass`, `git config core.askPass ksshaskpass`,
		`git config remote.origin.uploadpack git-upload-pack`, `git config remote.origin.receivepack git-receive-pack`, `git config core.gitProxy ""`,
	})
}

func TestTheTripwireCountsIncludesThatPullInAFile(t *testing.T) {
	counts(t, []string{
		`git config include.path ../.gitconfig`, `git config include.path ./extra`, `git config include.path /tmp/c`, `git config include.path ~/c`,
		`git config includeIf.gitdir:/x/.path ../c`, `git config includeIf.onbranch:main.path ./c`, `git config --local include.path extra.cfg`,
	}, []string{
		`git config --get include.path`, `git config --unset include.path`, `git config include.path ""`, `git config user.name x`, `git config includeIf.gitdir:/x/.foo bar`,
	})
}

// The same class as diff.external: ext:: URLs.
func TestTheTripwireCountsExtUrls(t *testing.T) {
	counts(t, []string{
		`git config remote.origin.url 'ext::sh -c id'`, `git config remote.origin.url 'EXT::./x'`, `git config remote.origin.pushurl 'ext::./x'`,
		`git config url.'ext::sh -c id'.insteadOf https://x/`,
	}, []string{
		`git config remote.origin.url https://example.com/x.git`, `git config remote.origin.url git@example.com:x/y.git`, `git config remote.origin.url /srv/git/x.git`,
	})
}

func TestTheTripwireCountsCompilerVariablesThatAreNotCompilers(t *testing.T) {
	counts(t, []string{
		`go env -w GOCACHEPROG=./cacheprog`, `go env -w GOCACHEPROG=/tmp/cp`, `go env -w GOCACHEPROG=x`, `go env -w GOCACHEPROG=cacheprog`,
		`go env -w CC=./cc`, `go env -w CC=/tmp/cc`, `go env -w CXX=~/cxx`, `go env -w CC=../x/cc`, `go env -w CC='sh -c id'`, `go env -w CC=python3.14`, `go env -w CC=just`,
		`CC=./cc go build .`, `env CC=./cc go build .`, `GOCACHEPROG=./p go build .`, `env GOCACHEPROG=/tmp/p go test .`, `export CC=./cc; go build .`, `CXX=/tmp/c++ go build .`,
		`go env -w CC=/opt/homebrew/bin/gcc-14`, `go env -w CC=/usr/local/bin/clang`, `go env -w CC='clang -B./x'`, `go env -w CC='gcc -m32'`,
		`go env -w FC=./fc`, `go env -w FC=/tmp/fc`, `go env -w PKG_CONFIG=./pc`, `go env -w PKG_CONFIG=/tmp/pc`, `go env -w PKG_CONFIG=just`, `go env -w AR=./ar`,
		`go env -w AR='sh -c id'`, `go env -w AR=python3`, `PKG_CONFIG=./pc go build .`, `env AR=./ar go build .`,
	}, []string{
		`go env -w CC=clang`, `go env -w CC=gcc`, `go env -w CC=gcc-14`, `go env -w CC=cc`, `go env -w CC=/usr/bin/clang`, `go env -w CXX=clang++`, `go env -w CXX=g++-13`,
		`go env -w FC=gfortran`, `go env -w PKG_CONFIG=pkg-config`, `go env -w PKG_CONFIG=pkgconf`, `go env -w AR=ar`, `go env -w AR=llvm-ar`,
		`CC=clang go build .`, `env CC=gcc go build .`, `CC=/usr/bin/cc go build .`, `go env CC`, `go env -u CC`, `go env -w GOPRIVATE=example.com`, `go env -w CC=""`,
	})
}

// go env -w accepts these (checked with go env -w): the flags that name where the compiler and linker find their programs or load code.
func TestTheTripwireCountsCgoFlagsThatNameProgramsOrPlugins(t *testing.T) {
	counts(t, []string{
		`go env -w CGO_LDFLAGS=-B./x`, `go env -w CGO_LDFLAGS='-O2 -B /tmp/x'`, `go env -w CGO_LDFLAGS=-B../x`, `CGO_LDFLAGS=-B./x go build .`, `env CGO_LDFLAGS='-g -B.' go build .`,
		`go env -w CGO_CFLAGS=-B./x`, `go env -w CGO_CPPFLAGS=-B/tmp/x`, `go env -w CGO_CXXFLAGS='-O2 -B ./x'`, `go env -w CGO_FFLAGS=-B.`, `CGO_CFLAGS=-B./x go build .`,
		`go env -w CGO_LDFLAGS=-Wl,-plugin=./p.so`, `go env -w CGO_LDFLAGS='-Wl,-plugin,./p.so'`, `go env -w CGO_LDFLAGS=-fuse-ld=./ld`, `go env -w CGO_LDFLAGS=-fuse-ld=/tmp/ld`,
		`go env -w CGO_LDFLAGS=-fuse-ld=../ld`,
	}, []string{
		`go env -w CGO_LDFLAGS=-O2`, `go env -w CGO_LDFLAGS='-L/usr/lib -lm'`, `go env -w CGO_LDFLAGS=-Wl,-rpath,/x`, `go env -w CGO_LDFLAGS=-fuse-ld=lld`, `go env -w CGO_LDFLAGS=-fuse-ld=gold`,
		`go env -w CGO_LDFLAGS=-Bstatic`, `go env -w CGO_CFLAGS=-O2`, `go env -w CGO_CFLAGS='-I/usr/include -Wall'`, `CGO_LDFLAGS=-O2 go build .`, `CGO_CFLAGS=-g go build .`,
		`go env CGO_LDFLAGS`, `go env -u CGO_LDFLAGS`,
	})
}

// The program keys sit on top of the git config reader: any file or scope, a prefix, a bundle, an alias.
func TestTheProgramKeysAreReadThroughTheConfigParser(t *testing.T) {
	counts(t, []string{
		`git config --file x.cfg credential.helper 'sh -c id'`, `git config -f x.cfg core.editor ./e`, `git config --global --type=path core.sshCommand ./s`,
		`git config -zf x.cfg gpg.program ./g`, `git config --fil x.cfg include.path y`, `git config set --value v filter.x.clean ./c`,
		`git -c alias.x=config x pager.log ./p`, `git --config-env=alias.x=A x core.gitProxy ./p`,
	}, []string{
		`git config --file x.cfg credential.helper store`, `git config -f x.cfg core.editor vim`, `git config --global --type=path core.sshCommand ssh`,
		`git config --file x.cfg --get core.editor`, `git config --file x.cfg --unset credential.helper`,
	})
}
