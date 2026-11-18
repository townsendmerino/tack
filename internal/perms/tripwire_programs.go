package perms

import (
	"path/filepath"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// programKey reports whether a git config key names a program git runs: an ssh command, an askpass or credential helper, an editor, a
// gpg program (gpg.program, gpg.<format>.program), a filter's clean, smudge or process command, a merge driver. A write of one is counted
// when its value is not an ordinary program name (runsProgram). The keys that are counted whatever their value (core.pager,
// diff.external, a textconv, an alias with a "!") and the includes are decided in configWriteRuns.
func programKey(key string) bool {
	switch key {
	case "core.sshcommand", "core.askpass", "credential.helper", "core.editor", "sequence.editor", "gpg.program":
		return true
	}
	return strings.HasPrefix(key, "credential.") && strings.HasSuffix(key, ".helper") ||
		strings.HasPrefix(key, "gpg.") && strings.HasSuffix(key, ".program") ||
		strings.HasPrefix(key, "filter.") && (strings.HasSuffix(key, ".clean") || strings.HasSuffix(key, ".smudge") || strings.HasSuffix(key, ".process")) ||
		strings.HasPrefix(key, "merge.") && strings.HasSuffix(key, ".driver")
}

// programVar reports whether a Go toolchain variable names a program it runs: the compiler drivers and the cache program.
func programVar(name string) bool {
	return name == "GOCACHEPROG" || name == "CC" || name == "CXX"
}

// envProgramVar: an argument of env that sets one of those to something that runs a program the repository could hold.
func envProgramVar(prog, arg string) bool {
	name, v, ok := strings.Cut(arg, "=")
	return prog == "env" && ok && programVar(name) && runsProgram(v)
}

// programRuns is runsProgram of a word an assignment gives a variable, as bash decodes it.
func programRuns(w *syntax.Word) bool {
	if v, ok := literal(w); ok {
		return runsProgram(v)
	}
	cands, ok := decodeCandidates(source(w))
	if !ok {
		return false
	}
	return slices.ContainsFunc(cands, runsProgram)
}

// interpreters are programs that run what they are handed as code: a setting that starts one is a shell command in all but name.
var interpreters = []string{"sh", "bash", "zsh", "dash", "ksh", "csh", "tcsh", "fish", "env", "xargs", "nohup", "python", "python3", "perl", "ruby", "node", "osascript", "awk", "open", "sudo"}

// systemDirs are where a program a person set up on purpose lives. A path under one is an ordinary setting (core.editor
// /usr/bin/vim, CC=/usr/bin/clang); a path anywhere else, or relative, is a program the repository or /tmp may hold.
var systemDirs = []string{"/usr/", "/bin/", "/sbin/", "/opt/homebrew/", "/Applications/", "/Library/"}

// runsProgram reports whether the value of a setting that names a program names one that is not an ordinary installed program: a value
// with shell syntax or a leading "!" (git runs it through a shell), one whose first word is a shell or an interpreter, or one whose first
// word is a path to a file the repository, /tmp or the home directory may hold (relative with a slash, "~", a ".." segment, an absolute
// path outside the system directories). A bare name (vim, ssh, store, osxkeychain, gpg, clang) and a path under a system directory are
// honest, as are an empty value and `git-lfs clean -- %f`.
func runsProgram(val string) bool {
	v := strings.TrimSpace(val)
	if v == "" {
		return false
	}
	if strings.HasPrefix(v, "!") || strings.ContainsAny(v, ";|&$`<>()\n\\") {
		return true
	}
	first := strings.Fields(v)[0]
	if slices.Contains(interpreters, baseName(first)) {
		return true
	}
	return pathWord(first)
}

// pathWord reports whether a program word is a path that may lead into the repository or a place a command writes.
func pathWord(w string) bool {
	switch {
	case strings.HasPrefix(w, "~"):
		return true
	case slices.Contains(strings.Split(filepath.ToSlash(w), "/"), ".."):
		return true
	case filepath.IsAbs(w):
		clean := filepath.ToSlash(filepath.Clean(w))
		return !slices.ContainsFunc(systemDirs, func(d string) bool { return strings.HasPrefix(clean, d) })
	}
	return strings.Contains(w, "/") // relative: ./x, scripts/x
}
