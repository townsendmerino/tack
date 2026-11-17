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
