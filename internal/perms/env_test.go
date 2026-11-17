package perms

import (
	"reflect"
	"strings"
	"testing"
)

// TestCommandEnvAllowed: the list passes, Go's settings by name and not by prefix, nothing named like a secret passes even
// from the list's families or from a repository's extra names, and an extra name passes only when it is no secret.
func TestCommandEnvAllowed(t *testing.T) {
	extra := []string{"CI", "MY_API_KEY", "BUILD_TOKEN", "FEATURE_FLAGS"}
	for name, want := range map[string]bool{
		"PATH": true, "HOME": true, "USER": true, "LANG": true, "TMPDIR": true, "TERM": true,
		"LC_ALL": true, "LC_CTYPE": true, "GOFLAGS": true, "GOPATH": true, "GOCACHE": true, "GOMODCACHE": true, "CGO_ENABLED": true,
		"GOOGLE_APPLICATION_CREDENTIALS": false, "GOBBLE": false, "GH_TOKEN": false, "SSH_AUTH_SOCK": false, "AWS_SECRET_ACCESS_KEY": false,
		"LC_TOKEN": false, "GOPASSWORD": false, "TOKEN": false, "api_key": false, "": false, "LOGNAME": false, "SHELL": false,
		"CI": true, "FEATURE_FLAGS": true, "MY_API_KEY": false, "BUILD_TOKEN": false,
	} {
		if got := CommandEnvAllowed(name, extra); got != want {
			t.Errorf("CommandEnvAllowed(%q) = %v, want %v", name, got, want)
		}
	}
	if CommandEnvAllowed("CI", nil) {
		t.Error("an extra name passed without being given")
	}
}

func TestCommandEnvFiltersAnEnvironment(t *testing.T) {
	in := []string{"PATH=/bin", "GH_TOKEN=x", "HOME=/h", "LC_ALL=C", "noequals", "=x", "GOOGLE_APPLICATION_CREDENTIALS=/c", "A=b=c"}
	want := []string{"PATH=/bin", "HOME=/h", "LC_ALL=C"}
	if got := CommandEnv(in, nil); !reflect.DeepEqual(got, want) {
		t.Errorf("CommandEnv = %v, want %v", got, want)
	}
	if got := CommandEnv(in, []string{"A"}); !reflect.DeepEqual(got, append(want, "A=b=c")) {
		t.Errorf("with A added: %v", got)
	}
}

// What a command inherits is what the guards compare against: a variable the list drops reads "" however tack's own
// environment holds it.
func TestInheritedEnv(t *testing.T) {
	t.Setenv("EDITOR", "vim")
	t.Setenv("GOFLAGS", "-mod=mod")
	if got := InheritedEnv("EDITOR", nil); got != "" {
		t.Errorf("EDITOR inherited as %q, want none", got)
	}
	if got := InheritedEnv("GOFLAGS", nil); got != "-mod=mod" {
		t.Errorf("GOFLAGS inherited as %q", got)
	}
	if got := InheritedEnv("EDITOR", []string{"EDITOR"}); got != "vim" {
		t.Errorf("EDITOR added by a repository inherited as %q", got)
	}
}

func TestCheckExtraEnv(t *testing.T) {
	if err := CheckExtraEnv([]string{"CI", "FEATURE_FLAGS"}); err != nil {
		t.Errorf("ordinary names: %v", err)
	}
	for _, n := range []string{"GH_TOKEN", "my_api_key", "DB_PASSWORD", "AWS_SECRET_ACCESS_KEY"} {
		if err := CheckExtraEnv([]string{"CI", n}); err == nil || !strings.Contains(err.Error(), n) {
			t.Errorf("%s: %v", n, err)
		}
	}
}

// TestCommandEnvDeniesMoreSecretShapes: credentials, agent sockets, askpass and ssh hooks are named like secrets too, even
// when a repository's configuration asks for them; PAT counts as a whole word, so PATH and GOPATH are not caught by it.
func TestCommandEnvDeniesMoreSecretShapes(t *testing.T) {
	denied := []string{"GH_PAT", "NPM_PAT", "PAT", "SSH_AUTH_SOCK", "AUTH_HEADER", "BASIC_AUTH", "OAUTH_CODE", "AWS_CREDENTIALS",
		"GOOGLE_CRED_FILE", "DB_PASSPHRASE", "DB_PASSWD", "GIT_ASKPASS", "SSH_ASKPASS", "GIT_SSH", "GIT_SSH_COMMAND", "DOCKER_SOCK",
		"GOAUTH", "LC_AUTH", "gh_pat"}
	for _, n := range denied {
		if CommandEnvAllowed(n, []string{n}) {
			t.Errorf("%s passed from a repository's extra names", n)
		}
	}
	for _, n := range []string{"PATCH_LEVEL", "CI", "SPATH_X"} { // names a repository may add that only look like secrets
		if !CommandEnvAllowed(n, []string{n}) {
			t.Errorf("%s was refused though it holds no secret word", n)
		}
	}
	for _, n := range []string{"PATH", "GOPATH", "LC_ALL"} {
		if !CommandEnvAllowed(n, nil) {
			t.Errorf("%s was refused", n)
		}
	}
	for n := range commandEnv {
		if !CommandEnvAllowed(n, nil) {
			t.Errorf("%s is on the list and does not pass", n)
		}
	}
}

// TestCommandEnvBlocksWhatChangesPrograms: variables that make a program load or run something of the repository's never
// reach a command, from the list or from a trusted configuration.
func TestCommandEnvBlocksWhatChangesPrograms(t *testing.T) {
	for _, n := range []string{"LD_PRELOAD", "LD_LIBRARY_PATH", "LD_AUDIT", "DYLD_INSERT_LIBRARIES", "DYLD_LIBRARY_PATH", "BASH_ENV",
		"ENV", "PROMPT_COMMAND"} {
		if CommandEnvAllowed(n, []string{n}) {
			t.Errorf("%s passed from a repository's extra names", n)
		}
	}
	for _, n := range []string{"ENVIRONMENT_NAME", "MY_ENV", "LDFLAGS", "PROMPT"} {
		if !CommandEnvAllowed(n, []string{n}) {
			t.Errorf("%s was refused though it only looks like one", n)
		}
	}
}

// TestCommandEnvDropsAProxyWithCredentials: a GOPROXY value that carries user:token@ userinfo is dropped whole, in the
// environment commands get and in what they are taken to have inherited.
func TestCommandEnvDropsAProxyWithCredentials(t *testing.T) {
	for value, keep := range map[string]bool{
		"https://proxy.golang.org,direct":        true,
		"off":                                    true,
		"https://proxy.example/path@v1,direct":   true,
		"https://user:tok@proxy.example,direct":  false,
		"direct|https://u@proxy.example":         false,
		"https://proxy.golang.org,http://a:b@h/": false,
	} {
		got := CommandEnv([]string{"GOPROXY=" + value}, nil)
		if kept := len(got) == 1; kept != keep {
			t.Errorf("GOPROXY=%s: kept %v, want %v", value, kept, keep)
		}
	}
	t.Setenv("GOPROXY", "https://user:tok@proxy.example")
	if got := InheritedEnv("GOPROXY", nil); got != "" {
		t.Errorf("a proxy with credentials inherited as %q", got)
	}
}
