package perms

import (
	"os"
	"strings"
)

// commandEnv is the environment variables a command inherits from tack (DESIGN §6.6, "Commands get a short
// environment, not tack's"): the ones a build and a shell need, and Go's own settings by name. Go's are a list and not a
// GO prefix, which would let GOOGLE_APPLICATION_CREDENTIALS through.
var commandEnv = map[string]bool{
	"PATH": true, "HOME": true, "USER": true, "LANG": true, "TMPDIR": true, "TERM": true,
	"GO111MODULE": true, "GOAMD64": true, "GOARCH": true, "GOARM": true, "GOARM64": true, "GOAUTH": true, "GOBIN": true,
	"GOCACHE": true, "GOCACHEPROG": true, "GOCOVERDIR": true, "GODEBUG": true, "GOENV": true, "GOEXPERIMENT": true,
	"GOFIPS140": true, "GOFLAGS": true, "GOGC": true, "GOINSECURE": true, "GOMAXPROCS": true, "GOMEMLIMIT": true,
	"GOMODCACHE": true, "GONOPROXY": true, "GONOSUMDB": true, "GOOS": true, "GOPATH": true, "GOPRIVATE": true,
	"GOPROXY": true, "GOROOT": true, "GOSUMDB": true, "GOTELEMETRY": true, "GOTELEMETRYDIR": true, "GOTMPDIR": true,
	"GOTOOLCHAIN": true, "GOTRACEBACK": true, "GOVCS": true, "GOWORK": true, "CGO_ENABLED": true,
}

// secretLike are the fragments of a name that mark a secret. A name holding one never passes, from the list or from a
// repository's configuration.
var secretLike = []string{"TOKEN", "KEY", "SECRET", "PASSWORD"}

// CommandEnvAllowed reports whether the variable name reaches a command: it is on the list or in extra (the names a trusted
// repository's configuration adds), and is not named like a secret. Names are matched as written.
func CommandEnvAllowed(name string, extra []string) bool {
	up := strings.ToUpper(name)
	for _, s := range secretLike {
		if strings.Contains(up, s) {
			return false
		}
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

// CommandEnv returns the entries of environ (NAME=value, as os.Environ gives them) that CommandEnvAllowed passes, in order.
func CommandEnv(environ, extra []string) []string {
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		name, _, ok := strings.Cut(kv, "=")
		if ok && name != "" && CommandEnvAllowed(name, extra) {
			out = append(out, kv)
		}
	}
	return out
}

// InheritedEnv is the value a command inherits for name from tack's own environment: what os.Getenv says when the name
// reaches commands, and "" when it does not, since a command then starts without it. The guards that ask whether a
// command changed a variable compare against this, not against tack's own value.
func InheritedEnv(name string, extra []string) string {
	if !CommandEnvAllowed(name, extra) {
		return ""
	}
	return os.Getenv(name)
}
