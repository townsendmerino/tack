package perms

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// valueClass says which values of a setting that runs a program are honest. No bare word is honest in itself: python3.14, just, pytest, rbash,
// gawk and busybox are bare words that run a file the repository holds. Each key has its own short list of known values, and every other
// value counts.
type valueClass int

const (
	classNone     valueClass = iota // nothing is honest but clearing the setting
	classEditor                     // vim, nano, code and the like, with plain flags
	classPager                      // less, delta, bat and the like, with plain flags; or a boolean (pager.<cmd>)
	classFilter                     // git-lfs and its own words
	classCredHelp                   // git's own credential helpers and the gh helper
	classSSH                        // ssh with plain options
	classGpg                        // gpg, gpgsm, ssh-keygen, 1Password's signer
	classDefKey                     // ssh-add -L
	classAskpass                    // the askpass programs
	classPack                       // git-upload-pack and git-receive-pack
	classBang                       // honest unless it starts with "!" (submodule.<n>.update)
	classPath                       // honest unless it names a path (sendemail.smtpServer)
	classNever                      // honest only as "never" (protocol.ext.allow)
)

// programKeyClass reports whether a lower-cased git config key names a program git runs (through a shell where git runs a command line), and
// which values of it are honest. A write of one is counted unless its value is (runsProgramFor). The keys counted whatever their value
// (an alias with a "!"), the includes and the ext:: URLs are decided in configWriteRuns.
func programKeyClass(key string) (valueClass, bool) {
	switch key {
	case "core.sshcommand":
		return classSSH, true
	case "core.askpass":
		return classAskpass, true
	case "core.editor", "sequence.editor":
		return classEditor, true
	case "core.pager", "interactive.difffilter":
		return classPager, true
	case "gpg.program":
		return classGpg, true
	case "credential.helper":
		return classCredHelp, true
	case "diff.external", "core.gitproxy", "core.alternaterefscommand", "uploadpack.packobjectshook", "sendemail.sendmailcmd", "init.templatedir",
		"instaweb.modulepath", "instaweb.httpd", "gitcvs.dbdriver":
		return classNone, true
	case "protocol.ext.allow":
		return classNever, true
	case "sendemail.smtpserver":
		return classPath, true
	}
	has := func(prefix, suffix string) bool {
		return strings.HasPrefix(key, prefix) && strings.HasSuffix(key, suffix)
	}
	switch {
	case has("credential.", ".helper"):
		return classCredHelp, true
	case has("gpg.", ".program"):
		return classGpg, true
	case has("gpg.", ".defaultkeycommand"):
		return classDefKey, true
	case has("filter.", ".clean"), has("filter.", ".smudge"), has("filter.", ".process"):
		return classFilter, true
	case strings.HasPrefix(key, "pager."):
		return classPager, true
	case has("remote.", ".uploadpack"), has("remote.", ".receivepack"):
		return classPack, true
	case has("submodule.", ".update"):
		return classBang, true
	case has("merge.", ".driver"), has("diff.", ".command"), has("tar.", ".command"), has("trailer.", ".cmd"), has("trailer.", ".command"),
		has("difftool.", ".cmd"), has("difftool.", ".path"), has("mergetool.", ".cmd"), has("mergetool.", ".path"),
		has("lfs.customtransfer.", ".path"), has("lfs.extension.", ".clean"), has("lfs.extension.", ".smudge"), has("gitcvs.", ".dbdriver"),
		has("browser.", ".cmd"), has("browser.", ".path"), has("man.", ".cmd"), has("man.", ".path"), has("guitool.", ".cmd"), has("remote.", ".vcs"):
		return classNone, true
	case has("sendemail.", ".smtpserver"):
		return classPath, true
	}
	// A key this code has not been told about is counted by the end of its name: a command, a hook, a program, a tunnel, a helper or a
	// pager (delta.pager, delta.<feature>.pager, bat.pager) names something git or a tool runs. Aliases are read in configWriteRuns: an
	// alias named hook or cmd is not one of these.
	if strings.HasPrefix(key, "alias.") {
		return 0, false
	}
	last := key[strings.LastIndexByte(key, '.')+1:]
	switch {
	case key == "instaweb.httpd":
		return classNone, true
	case last == "pager":
		return classPager, true
	case strings.HasSuffix(last, "cmd"), strings.HasSuffix(last, "command"), strings.HasSuffix(last, "hook"), strings.HasSuffix(last, "program"),
		strings.HasSuffix(last, "tunnel"), strings.HasSuffix(last, "helper"):
		return classNone, true
	}
	return 0, false
}

// programKey reports whether a lower-cased git config key names a program git runs.
func programKey(key string) bool {
	_, ok := programKeyClass(key)
	return ok
}

// runsProgramFor reports whether writing val to the program key is counted: it is when val is not on that key's list of honest values.
// Clearing the setting (an empty value) is honest for every key.
func runsProgramFor(key, val string) bool {
	class, ok := programKeyClass(key)
	v := strings.TrimSpace(val)
	if !ok || v == "" {
		return false
	}
	switch class {
	case classBang:
		return strings.HasPrefix(v, "!")
	case classPath:
		return strings.Contains(v, "/")
	case classNone:
		return true
	case classNever:
		return !strings.EqualFold(v, "never")
	}
	if class == classPager && boolWord(v) {
		return false
	}
	return !honestValue(class, v)
}

func boolWord(v string) bool {
	switch strings.ToLower(v) {
	case "true", "false", "yes", "no", "on", "off", "1", "0":
		return true
	}
	return false
}

// extURL reports whether a git config write makes a remote or a rewrite that runs a command: remote.<name>.url or .pushurl whose value
// starts with "ext::", or a url.<base>.insteadOf (or pushInsteadOf) whose base starts with "ext::".
func extURL(key, val string) bool {
	if strings.HasPrefix(key, "url.ext::") {
		return true
	}
	if strings.HasPrefix(key, "submodule.") && strings.HasSuffix(key, ".url") {
		return strings.HasPrefix(strings.ToLower(strings.TrimSpace(val)), "ext::")
	}
	return strings.HasPrefix(key, "remote.") && (strings.HasSuffix(key, ".url") || strings.HasSuffix(key, ".pushurl")) &&
		strings.HasPrefix(strings.ToLower(strings.TrimSpace(val)), "ext::")
}

var (
	pathRE      = regexp.MustCompile(`^/[A-Za-z0-9._+/-]+$`)
	sshOptionRE = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9]*)=[A-Za-z0-9._,:@+/-]*$`)
	timeoutRE   = regexp.MustCompile(`^--timeout=[0-9]+$`)
	sshHomeRE   = regexp.MustCompile(`^~/\.ssh/[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*$`)
	portRE      = regexp.MustCompile(`^[0-9]{1,5}$`)
	userRE      = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	compilerRE  = regexp.MustCompile(`^(gcc|g\+\+|clang|clang\+\+|cc|c\+\+|gfortran|flang|ifx|ifort)(-[0-9]+(\.[0-9]+)*)?$`)
)

// systemBinDirs are where the system's own programs live. A path directly under one is an ordinary setting (core.editor /usr/bin/vim);
// /usr/local and /opt/homebrew are not here, since a user can write to them.
var systemBinDirs = []string{"/usr/bin/", "/bin/", "/sbin/", "/usr/sbin/", "/usr/libexec/"}

// program reports whether w is one of names, as a bare name or as a path directly under a system bin directory.
func program(w string, names ...string) bool {
	if slices.Contains(names, w) {
		return true
	}
	if !pathRE.MatchString(w) || slices.Contains(strings.Split(w, "/"), "..") {
		return false
	}
	dir := filepath.ToSlash(filepath.Dir(w)) + "/"
	return slices.Contains(systemBinDirs, dir) && slices.Contains(names, filepath.Base(w))
}

// editorFlags are the flags, by editor, that only change how it opens or waits. A flag that takes a command or a file (vim -c, -S, -u, +cmd,
// emacs -f and --eval, emacsclient -e) is not here, so a value with one counts.
var editorFlags = map[string][]string{
	"vim": {"-f", "-p", "-o", "-O", "--nofork"}, "vi": {"-f", "-p", "-o", "-O"}, "nvim": {"-f", "-p", "-o", "-O", "--nofork"},
	"nano":  {"-w"},
	"emacs": {"-nw", "--no-window-system"}, "emacsclient": {"-t", "-nw", "-c", "-n", "--tty", "--no-wait", "--create-frame", "--reuse-frame", "--no-window-system"},
	"code": {"--wait", "-w", "-n", "--new-window", "-r", "--reuse-window"}, "subl": {"--wait", "-w", "-n", "--new-window"}, "mate": {"--wait", "-w"},
}

// pagerFlags are the flags, by pager, that only change how it draws. less -k reads a lesskey file (whose LESSOPEN runs a program) and -o,
// -O and -T write files, so they are not here; any flag not listed counts. less also takes bundles of its one-letter flags (-FRX) and
// -j<n>.
var pagerFlags = map[string][]string{
	"less": {"--RAW-CONTROL-CHARS", "--raw-control-chars", "--quit-if-one-screen", "--no-init", "--chop-long-lines", "--quit-at-eof", "--LONG-PROMPT",
		"--LINE-NUMBERS", "--ignore-case", "--IGNORE-CASE", "--clear-screen"},
	"more": {}, "cat": {}, "diff-so-fancy": {},
	"delta": {"--dark", "--light", "--color-only", "--side-by-side", "-s", "--line-numbers", "-n", "--navigate", "--raw", "--keep-plus-minus-markers", "--hyperlinks"},
	"bat":   {"--plain", "-p", "-pp", "-P", "--number", "-n"},
}

var (
	lessShortRE = regexp.MustCompile(`^-[RFXSKMNiIr]+$`)
	lessJumpRE  = regexp.MustCompile(`^-j[0-9]+$`)
)

// pagerFlagOK reports whether f is one of pager's known plain flags.
func pagerFlagOK(pager, f string) bool {
	if slices.Contains(pagerFlags[pager], f) {
		return true
	}
	return pager == "less" && (lessShortRE.MatchString(f) || lessJumpRE.MatchString(f))
}

// sshOptionsThatRun are ssh -o options that run a program, load a library or read another file of options.
var sshOptionsThatRun = []string{"proxycommand", "localcommand", "knownhostscommand", "permitlocalcommand", "include", "proxyjump", "pkcs11provider", "securitykeyprovider"}

// honestValue reports whether a non-empty value is one of the key class's known honest values.
func honestValue(class valueClass, v string) bool {
	toks := strings.Fields(v)
	switch class {
	case classEditor:
		if len(toks) == 0 {
			return false
		}
		base := filepath.Base(toks[0])
		flags, known := editorFlags[base]
		if !known || !program(toks[0], base) { // a bare name, or a path under a system bin directory
			return false
		}
		for i := 1; i < len(toks); i++ {
			f := toks[i]
			// emacsclient -a "" starts a server when there is none: an empty alternate editor, the one form of -a that is honest
			if base == "emacsclient" && f == "-a" && i+1 < len(toks) && (toks[i+1] == `""` || toks[i+1] == `''`) {
				i++
				continue
			}
			if !slices.Contains(flags, f) {
				return false
			}
		}
		return true
	case classPager:
		if len(toks) == 0 {
			return false
		}
		base := filepath.Base(toks[0])
		if _, known := pagerFlags[base]; !known || !program(toks[0], base) {
			return false
		}
		for _, f := range toks[1:] {
			if !pagerFlagOK(base, f) {
				return false
			}
		}
		return true
	case classFilter:
		if len(toks) < 2 || toks[0] != "git-lfs" {
			return false
		}
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
	case classCredHelp:
		switch v {
		case "store", "osxkeychain", "manager", "manager-core", "libsecret", "cache", "!gh auth git-credential", "!/usr/bin/gh auth git-credential",
			"/usr/lib/git-core/git-credential-libsecret", "/usr/lib/git-core/git-credential-store", "/usr/lib/git-core/git-credential-cache",
			"/usr/lib/git-core/git-credential-osxkeychain", "/usr/local/share/gcm-core/git-credential-manager", "/usr/share/gcm-core/git-credential-manager",
			"/usr/local/share/gcm-core/git-credential-manager-core", "/usr/local/bin/git-credential-manager", "!/opt/homebrew/bin/gh auth git-credential", "!/usr/local/bin/gh auth git-credential":
			return true
		}
		return len(toks) == 2 && toks[0] == "cache" && timeoutRE.MatchString(toks[1])
	case classSSH:
		return len(toks) > 0 && program(toks[0], "ssh") && sshArgsHonest(toks[1:])
	case classGpg:
		if v == "/Applications/1Password.app/Contents/MacOS/op-ssh-sign" || v == "/opt/1Password/op-ssh-sign" {
			return true
		}
		return len(toks) == 1 && program(toks[0], "gpg", "gpg2", "gpgsm", "ssh-keygen")
	case classDefKey:
		return v == "ssh-add -L"
	case classAskpass:
		return len(toks) == 1 && program(toks[0], "ssh-askpass", "ksshaskpass", "git-gui--askpass", "lxqt-openssh-askpass", "x11-ssh-askpass", "gnome-ssh-askpass")
	case classPack:
		return len(toks) == 1 && program(toks[0], "git-upload-pack", "git-receive-pack", "git-upload-archive")
	}
	return false
}

// sshArgsHonest: plain flags, -o Key=Value (or -oKey=Value) that does not run a program, -i and -F with a path under ~/.ssh, -p with a port,
// -l with a user name. Anything else (-J, a host and a command, -F elsewhere) counts.
func sshArgsHonest(toks []string) bool {
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		switch {
		case slices.Contains([]string{"-q", "-v", "-4", "-6", "-T", "-a", "-x", "-A", "-X", "-Y", "-C"}, t):
		case t == "-o" && i+1 < len(toks):
			i++
			if !sshOptionHonest(toks[i]) {
				return false
			}
		case strings.HasPrefix(t, "-o") && len(t) > 2:
			if !sshOptionHonest(t[2:]) {
				return false
			}
		case (t == "-i" || t == "-F") && i+1 < len(toks):
			i++
			if !sshHomeRE.MatchString(toks[i]) || strings.Contains(toks[i], "..") {
				return false
			}
		case t == "-p" && i+1 < len(toks):
			i++
			if !portRE.MatchString(toks[i]) {
				return false
			}
		case t == "-l" && i+1 < len(toks):
			i++
			if !userRE.MatchString(toks[i]) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func sshOptionHonest(opt string) bool {
	m := sshOptionRE.FindStringSubmatch(opt)
	return m != nil && !slices.Contains(sshOptionsThatRun, strings.ToLower(m[1]))
}

// programVarRuns reports whether setting a Go toolchain variable to value makes the toolchain run a program, or load code, that the
// repository, /tmp or the home directory may hold: a compiler or tool variable that is not an ordinary compiler, archiver or pkg-config,
// GOCACHEPROG whatever its value, or a CGO flags variable naming where the compiler finds its programs (-B) or a linker plugin or
// linker program.
func programVarRuns(name, value string) bool {
	v := strings.TrimSpace(value)
	if v == "" {
		return false
	}
	switch name {
	case "CC", "CXX", "FC":
		return !(len(strings.Fields(v)) == 1 && compilerWord(v))
	case "PKG_CONFIG":
		return !(len(strings.Fields(v)) == 1 && program(v, "pkg-config", "pkgconf"))
	case "AR":
		return !(len(strings.Fields(v)) == 1 && program(v, "ar", "llvm-ar", "gcc-ar"))
	case "GOCACHEPROG":
		return true
	case "CGO_CFLAGS", "CGO_CPPFLAGS", "CGO_CXXFLAGS", "CGO_FFLAGS", "CGO_LDFLAGS":
		return slices.ContainsFunc(strings.Fields(v), func(w string) bool { return !cgoFlagHonest(w) }) // counted unless every word is honest
	}
	return false
}

// compilerWord: an ordinary compiler name, with a version suffix, bare or directly under a system bin directory.
func compilerWord(w string) bool {
	if compilerRE.MatchString(w) {
		return true
	}
	return pathRE.MatchString(w) && !slices.Contains(strings.Split(w, "/"), "..") && slices.Contains(systemBinDirs, filepath.ToSlash(filepath.Dir(w))+"/") &&
		compilerRE.MatchString(filepath.Base(w))
}

var (
	cgoOptRE     = regexp.MustCompile(`^-(O[0-3sg]|g[0-3]?|ggdb)$`)
	cgoWarnRE    = regexp.MustCompile(`^-W[a-z][a-z0-9-]*$`) // -Wall, -Wextra, -Wno-x: no comma, so -Wl, -Wp, and -Wa, are not here
	cgoDefineRE  = regexp.MustCompile(`^-D[A-Za-z_][A-Za-z0-9_]*(=[A-Za-z0-9_.+-]*)?$`)
	cgoLibRE     = regexp.MustCompile(`^-l[A-Za-z0-9_.+-]+$`)
	cgoStdRE     = regexp.MustCompile(`^-std=[A-Za-z0-9+:.]+$`)
	cgoMachineRE = regexp.MustCompile(`^-m[a-z0-9_-]+(=[A-Za-z0-9_.+-]+)?$`)
	cgoFlagRE    = regexp.MustCompile(`^-f(no-)?[A-Za-z][A-Za-z0-9-]*$`) // -fPIC, -fno-omit-frame-pointer: no "=", so -fplugin=, -fuse-ld= and -fsanitize= are not
	cgoSysDirRE  = regexp.MustCompile(`^-[IL](/usr/(include|lib|lib64|libexec|local/(include|lib))|/opt/homebrew/(include|lib))(/[A-Za-z0-9._+-]+)*$`)
	cgoRpathRE   = regexp.MustCompile(`^-Wl,-rpath,(/usr/(lib|lib64|local/lib)|/opt/homebrew/lib)(/[A-Za-z0-9._+-]+)*$`)
)

// cgoFlagHonest reports whether one word of a CGO_*FLAGS value is on the short honest list: optimization, debug and warning flags, -D, -l,
// -std, -m<arch>, plain -f flags, -I and -L under the system's include and lib directories, a few link modes. Everything else counts:
// -B (where the compiler finds its programs), -wrapper, -specs=, -fplugin=, -fuse-ld=, --ld-path=, --prefix=, -Xlinker, -Wl, options that
// load a plugin or name a linker, and any path outside the system directories (-I. included).
//
// -Bstatic and -Bdynamic are not honest bare: gcc reads them as -B with the relative prefix "static" or "dynamic" and runs ./static/as; only
// -Wl,-Bstatic and -Wl,-Bdynamic choose how to link. -mllvm hands flags to LLVM. A ".." in a path leaves the directory it names.
func cgoFlagHonest(w string) bool {
	if w == "-mllvm" || slices.Contains(strings.Split(w, "/"), "..") {
		return false
	}
	switch w {
	case "-static", "-pthread", "-Wl,-Bstatic", "-Wl,-Bdynamic", "-shared", "-pie", "-Wl,--as-needed", "-Wl,--gc-sections", "-Wl,-s", "-Wl,-O1", "-Wl,-z,relro", "-Wl,-z,now":
		return true
	}
	for _, re := range []*regexp.Regexp{cgoOptRE, cgoWarnRE, cgoDefineRE, cgoLibRE, cgoStdRE, cgoMachineRE, cgoFlagRE, cgoSysDirRE, cgoRpathRE} {
		if re.MatchString(w) {
			return true
		}
	}
	return false
}

// envProgramVar: an argument of env that sets a toolchain variable to something that runs a program.
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
