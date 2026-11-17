package perms

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// commandEnv is the environment variables a command inherits from tack (DESIGN §6.6, "Commands get a short
// environment, not tack's"): the ones a build and a shell need, and Go's own settings by name. Go's are a list and not a
// GO prefix, which would let GOOGLE_APPLICATION_CREDENTIALS through. GOAUTH is not on it: it names a program that supplies
// credentials, and a name with AUTH in it never passes.
var commandEnv = map[string]bool{
	"PATH": true, "HOME": true, "USER": true, "LANG": true, "TMPDIR": true, "TERM": true,
	"GO111MODULE": true, "GOAMD64": true, "GOARCH": true, "GOARM": true, "GOARM64": true, "GOBIN": true,
	"GOCACHE": true, "GOCACHEPROG": true, "GOCOVERDIR": true, "GODEBUG": true, "GOENV": true, "GOEXPERIMENT": true,
	"GOFIPS140": true, "GOFLAGS": true, "GOGC": true, "GOINSECURE": true, "GOMAXPROCS": true, "GOMEMLIMIT": true,
	"GOMODCACHE": true, "GONOPROXY": true, "GONOSUMDB": true, "GOOS": true, "GOPATH": true, "GOPRIVATE": true,
	"GOPROXY": true, "GOROOT": true, "GOSUMDB": true, "GOTELEMETRY": true, "GOTELEMETRYDIR": true, "GOTMPDIR": true,
	"GOTOOLCHAIN": true, "GOTRACEBACK": true, "GOVCS": true, "GOWORK": true, "CGO_ENABLED": true,
}

// secretLike are the fragments of a name that mark a secret or a way to one: tokens, keys, passwords and passphrases
// (PASS also covers ASKPASS), credentials and authentication, agent sockets, and ssh's own hooks. A name holding one never
// passes, from the list or from a repository's configuration.
var secretLike = []string{"TOKEN", "KEY", "SECRET", "PASS", "AUTH", "CRED", "SOCK", "ASKPASS", "GIT_SSH"}

// secretWords are secret names that are short enough to appear inside ordinary ones (PATH, GOPATH, PATCH), so they count
// only as a whole word between underscores: GH_PAT is one, PATH is not.
var secretWords = []string{"PAT"}

// blockedPrefixes and blockedNames are what makes a program load or run something else: the dynamic loader's variables,
// and the files a shell reads or the command it runs on its own. They never pass, whatever a configuration says.
var (
	blockedPrefixes = []string{"LD_", "DYLD_"}
	blockedNames    = map[string]bool{"BASH_ENV": true, "ENV": true, "PROMPT_COMMAND": true}
)

// proxyUserinfo matches a URL whose authority holds userinfo (user:token@host) anywhere in a GOPROXY list.
var proxyUserinfo = regexp.MustCompile(`://[^/?#,|\s]*@`)

func secretNamed(name string) bool {
	up := strings.ToUpper(name)
	for _, s := range secretLike {
		if strings.Contains(up, s) {
			return true
		}
	}
	for w := range strings.SplitSeq(up, "_") {
		for _, s := range secretWords {
			if w == s {
				return true
			}
		}
	}
	return false
}

func blocked(name string) bool {
	up := strings.ToUpper(name)
	if blockedNames[up] {
		return true
	}
	for _, p := range blockedPrefixes {
		if strings.HasPrefix(up, p) {
			return true
		}
	}
	return false
}

// CommandEnvAllowed reports whether the variable name reaches a command: it is on the list or in extra (the names a trusted
// repository's configuration adds), and is neither named like a secret nor one that changes what a program loads or runs.
// Names are matched as written, but the secret and blocked tests ignore case.
func CommandEnvAllowed(name string, extra []string) bool {
	if secretNamed(name) || blocked(name) {
		return false
	}
	if commandEnv[name] || strings.HasPrefix(name, "LC_") {
		return true
	}
	for _, e := range extra {
		if e == name {
			return true
		}
	}
	return false
}

// commandEnvKeep is CommandEnvAllowed for a variable and its value: a GOPROXY whose value carries user:token@ userinfo is
// dropped whole, since the list it holds is the one place a credential rides in a variable the list passes.
func commandEnvKeep(name, value string, extra []string) bool {
	if !CommandEnvAllowed(name, extra) {
		return false
	}
	return !(name == "GOPROXY" && proxyUserinfo.MatchString(value))
}

// CommandEnv returns the entries of environ (NAME=value, as os.Environ gives them) that commandEnvKeep passes, in order.
func CommandEnv(environ, extra []string) []string {
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		name, value, ok := strings.Cut(kv, "=")
		if ok && name != "" && commandEnvKeep(name, value, extra) {
			out = append(out, kv)
		}
	}
	return out
}

// InheritedEnv is the value a command inherits for name from tack's own environment: what os.Getenv says when the name
// reaches commands, and "" when it does not, since a command then starts without it. The guards that ask whether a
// command changed a variable compare against this, not against tack's own value.
func InheritedEnv(name string, extra []string) string {
	v := os.Getenv(name)
	if !commandEnvKeep(name, v, extra) {
		return ""
	}
	return v
}

// CheckExtraEnv refuses names a repository's configuration may not add to its commands' environment: one named like a
// secret never passes (CommandEnvAllowed). It is checked when the owner trusts the file and again when a session reads it.
func CheckExtraEnv(names []string) error {
	for _, n := range names {
		if !CommandEnvAllowed(n, []string{n}) {
			return fmt.Errorf("[commands] env names %s, which is named like a secret and never reaches commands", n)
		}
	}
	return nil
}
