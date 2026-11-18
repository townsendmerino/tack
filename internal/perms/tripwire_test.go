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

// counts checks one group of settings that make git or the Go toolchain run a program: each counted form counts, and each honest form
// (a short allowlist of known-honest values, a read, a removal) does not.
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

// A write of these keys counts unless the whole value is on the allowlist: one bare word without metacharacters (and not an interpreter),
// a program and --wait or -w, a git-lfs filter command, a credential cache with --timeout, ssh with plain -o options other than the ones
// that run a program, or one path under /usr/bin, /bin, /sbin, /usr/sbin, /usr/libexec.

func TestTheTripwireCountsSshCommandAskPassAndCredentialHelper(t *testing.T) {
	counts(t, []string{
		`git config core.sshCommand ./ssh-wrap`, `git config core.sshCommand 'sh -c id'`, `git config core.sshCommand '/tmp/x -i key'`, `git config core.sshCommand 'ssh; id'`,
		`git config core.sshCommand 'ssh -o ProxyCommand=./x'`, `git config core.sshCommand 'ssh -F ./cfg'`, `git config core.sshCommand 'ssh -oProxyCommand=./x host'`,
		`git config core.sshCommand 'ssh -o LocalCommand=./x -o PermitLocalCommand=yes'`, `git config core.sshCommand 'ssh -o KnownHostsCommand=./x'`,
		`git config core.sshCommand 'ssh -o proxycommand=./x'`, `git config core.sshCommand 'ssh -o PermitLocalCommand=yes'`, `git config core.sshCommand 'ssh -o Include=./c'`,
		`git config core.sshCommand '"sh" x.sh'`, `git config core.sshCommand 'FOO=1 sh x.sh'`, `git config core.sshCommand 'timeout 9 sh x.sh'`,
		`git config core.askPass ./askpass`, `git config core.askPass ~/x`, `git config --global core.askPass ../tools/ask`, `git config core.askPass /usr/local/bin/ask`,
		`git config credential.helper '!f() { id; }; f'`, `git config credential.helper ./helper`, `git config credential.https://example.com.helper /tmp/h`,
		`git config credential.helper 'cache --socket ./s'`, `git config credential.helper 'store --file ./f'`,
	}, []string{
		`git config core.sshCommand ssh`, `git config core.sshCommand 'ssh -o StrictHostKeyChecking=accept-new'`, `git config core.sshCommand '/usr/bin/ssh'`,
		`git config core.sshCommand 'ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null'`, `git config core.sshCommand 'ssh -oBatchMode=yes'`,
		`git config core.askPass ssh-askpass`, `git config credential.helper store`, `git config credential.helper 'cache --timeout=3600'`,
		`git config credential.helper osxkeychain`, `git config credential.https://example.com.helper manager`, `git config --get credential.helper`,
		`git config --unset credential.helper`, `git config credential.helper ""`, `git config credential.username me`,
	})
}

func TestTheTripwireCountsEditorsThatAreNotAName(t *testing.T) {
	counts(t, []string{
		`git config core.editor ./edit.sh`, `git config core.editor 'sh -c id'`, `git config core.editor '/tmp/ed'`, `git config sequence.editor ./seq`,
		`git config --global sequence.editor 'bash -c id'`, `git config core.editor 'vim; id'`, `git config sequence.editor '!id'`, `git config core.editor 'python3 x.py'`,
		`git config core.editor 'vim -c "!./x"'`, `git config core.editor 'vim -S x.vim'`, `git config core.editor 'make -f x.mk'`, `git config core.editor '. ./x'`,
		`git config core.editor 'eval ./x'`, `git config core.editor 'exec ./x'`, `git config core.editor sh`, `git config core.editor '/opt/homebrew/bin/nvim'`,
		`git config core.editor '/usr/local/bin/vim'`, `git config core.editor 'code --wait --extra'`,
	}, []string{
		`git config core.editor vim`, `git config core.editor 'code --wait'`, `git config sequence.editor nano`, `git config core.editor /usr/bin/vim`,
		`git config core.editor 'code -w'`, `git config --get sequence.editor`, `git config --unset core.editor`,
	})
}

func TestTheTripwireCountsGpgProgram(t *testing.T) {
	counts(t, []string{
		`git config gpg.program ./gpg-wrap`, `git config gpg.ssh.program /tmp/sign`, `git config gpg.x509.program ~/s`, `git config gpg.openpgp.program 'sh -c id'`,
		`git config --system gpg.program ../x`, `git config gpg.program /usr/local/bin/gpg`, `git config gpg.program 'gpg --homedir ./h'`,
	}, []string{
		`git config gpg.program gpg`, `git config gpg.program gpg2`, `git config gpg.ssh.program ssh-keygen`, `git config gpg.format ssh`,
		`git config gpg.program /usr/bin/gpg`, `git config --get gpg.program`,
	})
}

func TestTheTripwireCountsFilterDrivers(t *testing.T) {
	counts(t, []string{
		`git config filter.x.clean ./clean.sh`, `git config filter.x.smudge './smudge %f'`, `git config filter.x.process /tmp/p`, `git config filter.lfs.clean 'sh -c id'`,
		`git config --local filter.x.clean 'cat; id'`, `git config filter.x.clean 'go run ./tools/c'`, `git config filter.x.clean 'git -c alias.y=!./e y'`,
		`git config filter.x.smudge 'git-lfs smudge -- %f; id'`, `git config filter.x.clean 'git-lfs clean -- ./x'`,
	}, []string{
		`git config filter.lfs.clean 'git-lfs clean -- %f'`, `git config filter.lfs.smudge 'git-lfs smudge -- %f'`, `git config filter.lfs.process 'git-lfs filter-process'`,
		`git config filter.lfs.process 'git-lfs filter-process --skip'`, `git config filter.lfs.required true`, `git config --unset filter.x.clean`,
		`git config --get filter.x.smudge`,
	})
}

func TestTheTripwireCountsMergeDrivers(t *testing.T) {
	counts(t, []string{
		`git config merge.x.driver './merge.sh %O %A %B'`, `git config merge.x.driver 'sh -c id'`, `git config merge.ours.driver /tmp/m`, `git config --global merge.x.driver ../m`,
		`git config merge.x.driver 'git merge-file %A %O %B'`,
	}, []string{
		`git config merge.ours.driver true`, `git config merge.x.name 'a driver'`, `git config --unset merge.x.driver`, `git config merge.tool vimdiff`,
	})
}

func TestTheTripwireCountsDiffExternalAndTextconvStill(t *testing.T) {
	counts(t, []string{
		`git config diff.external ./d`, `git config diff.x.textconv ./t`, `git config diff.external 'sh -c id'`,
	}, []string{
		`git config diff.algorithm patience`, `git config diff.x.binary true`, `git config --get diff.external`, `git config --unset diff.x.textconv`,
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

// The same class as diff.external: settings git runs a program for.
func TestTheTripwireCountsTheOtherSettingsGitRunsAProgramFor(t *testing.T) {
	counts(t, []string{
		`git config pager.log ./p`, `git config pager.diff 'sh -c id'`, `git config pager.status /tmp/p`,
		`git config diff.x.command ./d`, `git config diff.x.command 'sh -c id'`, `git config interactive.diffFilter ./f`, `git config interactive.diffFilter 'diff-highlight; id'`,
		`git config core.gitProxy ./proxy`, `git config core.gitProxy './p for example.com'`,
		`git config remote.origin.uploadpack ./up`, `git config remote.origin.receivepack /tmp/rp`, `git config remote.x.uploadpack 'sh -c id'`,
		`git config remote.origin.url 'ext::sh -c id'`, `git config remote.origin.url 'EXT::./x'`, `git config remote.origin.pushurl 'ext::./x'`,
		`git config url.'ext::sh -c id'.insteadOf https://x/`,
		`git config core.alternateRefsCommand ./refs`, `git config gpg.ssh.defaultKeyCommand ./key`, `git config difftool.x.cmd ./d`, `git config mergetool.x.cmd 'sh -c id'`,
		`git config uploadpack.packObjectsHook ./hook`, `git config uploadpack.packObjectsHook 'sh -c id'`,
	}, []string{
		`git config pager.log false`, `git config pager.diff true`, `git config remote.origin.url https://example.com/x.git`, `git config remote.origin.url git@example.com:x/y.git`,
		`git config remote.origin.url /srv/git/x.git`, `git config remote.origin.uploadpack git-upload-pack`, `git config core.gitProxy ""`,
		`git config uploadpack.allowFilter true`, `git config difftool.x.path vimdiff`, `git config --unset pager.log`, `git config --get core.gitProxy`,
	})
}

func TestTheTripwireCountsCCGOCACHEPROGAndCXXSetToAPath(t *testing.T) {
	counts(t, []string{
		`go env -w GOCACHEPROG=./cacheprog`, `go env -w GOCACHEPROG=/tmp/cp`, `go env -w CC=./cc`, `go env -w CC=/tmp/cc`, `go env -w CXX=~/cxx`, `go env -w CC=../x/cc`,
		`go env -w CC='sh -c id'`, `CC=./cc go build .`, `env CC=./cc go build .`, `GOCACHEPROG=./p go build .`, `env GOCACHEPROG=/tmp/p go test .`, `export CC=./cc; go build .`,
		`CXX=/tmp/c++ go build .`, `go env -w CC=/opt/homebrew/bin/gcc-14`, `go env -w CC=/usr/local/bin/clang`, `go env -w CC='clang -B./x'`,
	}, []string{
		`go env -w CC=clang`, `go env -w CC=gcc`, `go env -w GOCACHEPROG=x`, `go env -w CC=/usr/bin/clang`, `go env -w CXX=clang++`,
		`CC=clang go build .`, `env CC=gcc go build .`, `CC=/usr/bin/cc go build .`, `go env CC`, `go env -u CC`, `go env -w GOPRIVATE=example.com`,
	})
}

// go env -w accepts these (checked with go env -w): the programs the toolchain runs, and linker flags that name where to find them.
func TestTheTripwireCountsPkgConfigArAndCgoLdflagsWithB(t *testing.T) {
	counts(t, []string{
		`go env -w PKG_CONFIG=./pc`, `go env -w PKG_CONFIG=/tmp/pc`, `go env -w AR=./ar`, `go env -w AR='sh -c id'`,
		`go env -w CGO_LDFLAGS=-B./x`, `go env -w CGO_LDFLAGS='-O2 -B /tmp/x'`, `go env -w CGO_LDFLAGS=-B../x`, `PKG_CONFIG=./pc go build .`, `env AR=./ar go build .`,
		`CGO_LDFLAGS=-B./x go build .`, `env CGO_LDFLAGS='-g -B.' go build .`,
	}, []string{
		`go env -w PKG_CONFIG=pkg-config`, `go env -w AR=ar`, `go env -w CGO_LDFLAGS=-O2`, `go env -w CGO_LDFLAGS='-L/usr/lib -lm'`, `go env -w CGO_LDFLAGS=-Wl,-rpath,/x`,
		`CGO_LDFLAGS=-O2 go build .`, `go env PKG_CONFIG`, `go env -u CGO_LDFLAGS`,
	})
}
