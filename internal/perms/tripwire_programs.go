package perms

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// programKey reports whether a git config key names a program git runs, through a shell where git runs a command line: an ssh command, an
// askpass or credential helper, an editor, a gpg program (gpg.program, gpg.<format>.program, gpg.ssh.defaultKeyCommand), a filter's clean,
// smudge or process command, a merge driver, a pager for one command, a diff command, the interactive diff filter, a git proxy, a
// remote's upload-pack and receive-pack, the alternate-refs command, a difftool or mergetool command, the pack-objects hook. A write of
// one is counted unless its whole value is on the allowlist of honest forms (runsProgram). The keys counted whatever their value
// (core.pager, diff.external, a textconv, an alias with a "!"), the includes and the ext:: URLs are decided in configWriteRuns.
func programKey(key string) bool {
	switch key {
	case "core.sshcommand", "core.askpass", "credential.helper", "core.editor", "sequence.editor", "gpg.program", "interactive.difffilter",
		"core.gitproxy", "core.alternaterefscommand", "uploadpack.packobjectshook":
		return true
	}
	return strings.HasPrefix(key, "credential.") && strings.HasSuffix(key, ".helper") ||
		strings.HasPrefix(key, "gpg.") && (strings.HasSuffix(key, ".program") || strings.HasSuffix(key, ".defaultkeycommand")) ||
		strings.HasPrefix(key, "filter.") && (strings.HasSuffix(key, ".clean") || strings.HasSuffix(key, ".smudge") || strings.HasSuffix(key, ".process")) ||
		strings.HasPrefix(key, "merge.") && strings.HasSuffix(key, ".driver") ||
		strings.HasPrefix(key, "pager.") ||
		strings.HasPrefix(key, "diff.") && strings.HasSuffix(key, ".command") ||
		strings.HasPrefix(key, "remote.") && (strings.HasSuffix(key, ".uploadpack") || strings.HasSuffix(key, ".receivepack")) ||
		(strings.HasPrefix(key, "difftool.") || strings.HasPrefix(key, "mergetool.")) && strings.HasSuffix(key, ".cmd")
}

// extURL reports whether a git config write makes a remote or a rewrite that runs a command: remote.<name>.url or .pushurl whose value
// starts with "ext::", or a url.<base>.insteadOf (or pushInsteadOf) whose base starts with "ext::".
func extURL(key, val string) bool {
	if strings.HasPrefix(key, "url.ext::") {
		return true
	}
	return strings.HasPrefix(key, "remote.") && (strings.HasSuffix(key, ".url") || strings.HasSuffix(key, ".pushurl")) &&
		strings.HasPrefix(strings.ToLower(strings.TrimSpace(val)), "ext::")
}

// programVar reports whether a Go toolchain variable names a program it runs: the compiler drivers, the archiver, pkg-config and the
// cache program.
func programVar(name string) bool {
	switch name {
	case "GOCACHEPROG", "CC", "CXX", "PKG_CONFIG", "AR":
		return true
	}
	return false
}

// programVarRuns reports whether setting name to value makes the Go toolchain run a program the repository, /tmp or the home directory may
// hold: a program variable not on the allowlist, or CGO_LDFLAGS holding -B (the directory the linker driver finds its programs in).
func programVarRuns(name, value string) bool {
	switch {
	case programVar(name):
		return runsProgram(value)
	case name == "CGO_LDFLAGS":
		return hasBWord(value)
	}
	return false
}

// hasBWord reports whether a flags string has a word -B or -B<dir>, which names where gcc and ld look for their programs (-Bstatic and
// -Bdynamic, which choose how to link, do not).
func hasBWord(v string) bool {
	for _, w := range strings.Fields(v) {
		if strings.HasPrefix(w, "-B") && !slices.Contains([]string{"-Bstatic", "-Bdynamic", "-Bsymbolic", "-Bsymbolic-functions"}, w) {
			return true
		}
	}
	return false
}

// envProgramVar: an argument of env that sets a program variable (or CGO_LDFLAGS) to something that runs a program.
func envProgramVar(prog, arg string) bool {
	name, v, ok := strings.Cut(arg, "=")
	return prog == "env" && ok && programVarRuns(name, v)
}

// programAssign is programVarRuns of the word an assignment gives a variable, as bash decodes it.
func programAssign(name string, w *syntax.Word) bool {
	if v, ok := literal(w); ok {
		return programVarRuns(name, v)
	}
	cands, ok := decodeCandidates(source(w))
	if !ok {
		return false
	}
	return slices.ContainsFunc(cands, func(c string) bool { return programVarRuns(name, c) })
}

// interpreters are programs that run what they are handed as code, or another program: a setting that is only one of them, or starts
// one, is a shell command in all but name. A bare word on this list is not honest.
var interpreters = []string{
	"sh", "bash", "zsh", "dash", "ksh", "csh", "tcsh", "fish", "env", "xargs", "nohup", "python", "python3", "perl", "ruby", "node", "php", "lua",
	"osascript", "awk", "sed", "open", "sudo", "doas", "eval", "exec", "source", "time", "timeout", "command", "builtin", "nice", "make",
	"go", "npx", "npm", "cargo", "java", "watch", "find", "script", "expect",
}

// systemBinDirs are where the system's own programs live. A path directly under one is an ordinary setting (core.editor /usr/bin/vim);
// /usr/local and /opt/homebrew are not here, since a user can write to them.
var systemBinDirs = []string{"/usr/bin/", "/bin/", "/sbin/", "/usr/sbin/", "/usr/libexec/"}

// sshOptionsThatRun are ssh -o options that run a program, load a library or read another file of options.
var sshOptionsThatRun = []string{"proxycommand", "localcommand", "knownhostscommand", "permitlocalcommand", "include", "proxyjump", "pkcs11provider", "securitykeyprovider"}

var (
	bareWordRE   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]*$`)
	pathWordRE   = regexp.MustCompile(`^/[A-Za-z0-9._+/-]+$`)
	sshOptionRE  = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9]*)=[A-Za-z0-9._,:@+/-]*$`)
	timeoutArgRE = regexp.MustCompile(`^--timeout=[0-9]+$`)
	valueCharsRE = regexp.MustCompile(`^[A-Za-z0-9 _.,:@+=%/~-]*$`)
)

// runsProgram reports whether the value of a setting that names a program is not on the allowlist of honest forms. A value is honest
// only when the whole of it is one of:
//   - empty (the setting is being cleared);
//   - one bare word with no metacharacter that is not an interpreter (vim, nano, code, ssh, store, osxkeychain, manager, gpg, clang);
//   - one path directly under /usr/bin, /bin, /sbin, /usr/sbin or /usr/libexec, whose program is not an interpreter;
//   - a bare word and --wait or -w (code --wait);
//   - git-lfs and its own words (clean, smudge, filter-process, --, %f, --skip);
//   - cache --timeout=N, a credential helper;
//   - ssh and plain -o Key=Value options, none of which runs a program or reads another file (ProxyCommand, LocalCommand, KnownHostsCommand,
//     PermitLocalCommand, Include, ProxyJump, PKCS11Provider, SecurityKeyProvider).
//
// Anything else counts: a quote, "=" in the first word, "!", ";", a path outside the system directories, a second word that is not one of
// the above. git runs these values through /bin/sh, so the first word alone says too little.
func runsProgram(val string) bool {
	v := strings.TrimSpace(val)
	return v != "" && !honestProgramValue(v)
}

func honestProgramValue(v string) bool {
	if !valueCharsRE.MatchString(v) {
		return false
	}
	toks := strings.Fields(v)
	switch {
	case len(toks) == 1:
		return bareProgram(toks[0]) || systemProgram(toks[0])
	case toks[0] == "git-lfs":
		ok := false
		for _, t := range toks[1:] {
			switch t {
			case "clean", "smudge", "filter-process":
				ok = true
			case "--", "%f", "--skip":
			default:
				return false
			}
		}
		return ok
	case toks[0] == "cache":
		return len(toks) == 2 && timeoutArgRE.MatchString(toks[1])
	case toks[0] == "ssh":
		return sshOptionsHonest(toks[1:])
	case len(toks) == 2 && (toks[1] == "--wait" || toks[1] == "-w"):
		return bareProgram(toks[0])
	}
	return false
}

// bareProgram: one word with no path and no metacharacter, that is not an interpreter.
func bareProgram(w string) bool {
	return bareWordRE.MatchString(w) && !slices.Contains(interpreters, w)
}

// systemProgram: a path directly under a system bin directory whose program is not an interpreter.
func systemProgram(w string) bool {
	if !pathWordRE.MatchString(w) || slices.Contains(strings.Split(w, "/"), "..") {
		return false
	}
	dir := filepath.ToSlash(filepath.Dir(w)) + "/"
	return slices.Contains(systemBinDirs, dir) && !slices.Contains(interpreters, filepath.Base(w))
}

// sshOptionsHonest: only -o Key=Value (or -oKey=Value) words, and none of them one that runs a program.
func sshOptionsHonest(toks []string) bool {
	for i := 0; i < len(toks); i++ {
		opt := ""
		switch {
		case toks[i] == "-o" && i+1 < len(toks):
			i++
			opt = toks[i]
		case strings.HasPrefix(toks[i], "-o") && len(toks[i]) > 2:
			opt = toks[i][2:]
		default:
			return false
		}
		m := sshOptionRE.FindStringSubmatch(opt)
		if m == nil || slices.Contains(sshOptionsThatRun, strings.ToLower(m[1])) {
			return false
		}
	}
	return true
}
