//go:build darwin && sandboxtest

// The escape tests (DESIGN §6.6). They start sandbox-exec, which macOS refuses inside an already sandboxed process, so
// they carry the sandboxtest tag and run from the day-loop checker or CI, never from tack's own bash:
//
//	go test -tags sandboxtest ./internal/sandbox/
package sandbox

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type fixture struct {
	cfg             Config
	ws, home, state string
	ownerGoCache    string
	ownerModCache   string
	env             []string
}

func setup(t *testing.T) fixture {
	t.Helper()
	dir := func() string { d, _ := filepath.EvalSymlinks(t.TempDir()); return d }
	f := fixture{ws: dir(), home: dir(), state: dir()}
	write := func(p, body string) {
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range [][]string{{"git", "init", "-q"}} {
		cmd := exec.Command(c[0], c[1:]...)
		cmd.Dir = f.ws
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}
	write(filepath.Join(f.ws, "go.mod"), "module x\n\ngo 1.27\n")
	write(filepath.Join(f.ws, "x_test.go"), "package x\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) {}\n")
	write(filepath.Join(f.ws, ".env"), "SECRET=ws-dotenv\n")
	write(filepath.Join(f.home, ".ssh", "id_test"), "SECRET=ssh-key\n")
	write(filepath.Join(f.home, ".config", "gh", "hosts.yml"), "github.com:\n    oauth_token: SECRET=gh-token\n")
	write(filepath.Join(f.home, "Library", "Keychains", "login.keychain-db"), "SECRET=keychain\n")
	write(filepath.Join(f.home, ".git-credentials"), "https://x:SECRET=git-cred.com\n")
	write(filepath.Join(f.home, ".netrc"), "machine x password SECRET=netrc\n")
	write(filepath.Join(f.home, ".docker", "config.json"), "{\"auths\":\"SECRET=docker\"}\n")
	write(filepath.Join(f.home, "Library", "Messages", "chat.db"), "SECRET=messages\n")
	write(filepath.Join(f.home, ".zshrc"), "# owner's\n")
	write(filepath.Join(f.state, "token"), "SECRET=tack-token\n")
	f.ownerGoCache = filepath.Join(f.home, "Library", "Caches", "go-build")
	f.ownerModCache = filepath.Join(f.home, "go", "pkg", "mod")
	os.MkdirAll(f.ownerGoCache, 0o755)
	os.MkdirAll(f.ownerModCache, 0o755)
	f.cfg = Config{Workspace: f.ws, Temp: dir(), GoCache: dir(), Home: f.home, Tack: []string{f.state}}
	f.env = []string{"HOME=" + f.home, "TMPDIR=" + f.cfg.Temp, "GOCACHE=" + f.cfg.GoCache, "PATH=" + os.Getenv("PATH"),
		"GOTOOLCHAIN=local", "GOFLAGS=-mod=mod", "GOPATH=" + filepath.Join(f.home, "go"), "STATE=" + f.state}
	return f
}

// run runs script with /bin/sh in the workspace: under cfg's profile, or with no sandbox when cfg is nil.
func (f fixture) run(cfg *Config, script string) string {
	var cmd *exec.Cmd
	if cfg == nil {
		cmd = exec.Command("/bin/sh", "-c", script)
	} else {
		cmd = Command(*cfg, "/bin/sh", "-c", script)
	}
	cmd.Dir, cmd.Env = f.ws, f.env
	out, _ := cmd.CombinedOutput()
	return string(out)
}

func (f fixture) without(sections ...string) *Config {
	c := f.cfg
	c.Without = sections
	return &c
}

// TestEscapes: each escape gets out with no sandbox (so the test can see it), is stopped by the full profile, and gets
// out again with only its own section removed, so that section is what stops it.
func TestEscapes(t *testing.T) {
	f := setup(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "ESCAPED-network") }))
	url := "http://" + ln.Addr().String() + "/"

	cases := []struct {
		name     string
		sections []string // removing these together lets the escape out
		script   string
		mark     string
	}{
		{"osascript runs AppleScript", secs(SecLaunch), `/usr/bin/osascript -e 'return "ESCAPED-" & "osascript"'`, "ESCAPED-osascript"},
		{"launchctl reaches launchd", secs(SecLaunch), `/bin/launchctl print-disabled gui/$(id -u) >/dev/null && echo ESCAPED-launchctl`, "ESCAPED-launchctl"},
		{"open reaches LaunchServices", secs(SecLaunch), `/usr/bin/open -a NoSuchAppForTackEscapeTest 2>&1`, "Unable to find application"},
		{"lsappinfo, not on the exec list, reaches LaunchServices", secs(SecLaunch), `/usr/bin/lsappinfo front`, "ASN:"},
		{"hdiutil reaches disk images", secs(SecMount), `/usr/bin/hdiutil info >/dev/null && echo ESCAPED-hdiutil`, "ESCAPED-hdiutil"},
		{"network to a local server", secs(SecNetwork), `/usr/bin/curl -s -m 5 ` + url, "ESCAPED-network"},
		{"reading tack's token", secs(SecTack), `cat "$STATE/token"`, "tack-token"},
		{"writing into tack's state", secs(SecTack + "+" + SecWrite), `echo x > "$STATE/approve" && echo ESCAPED-tack-write`, "ESCAPED-tack-write"},
		{"writing a git hook", secs(SecLater), `echo x > .git/hooks/pre-commit && echo ESCAPED-hook`, "ESCAPED-hook"},
		{"writing git config", secs(SecLater), `git config core.fsmonitor /evil && echo ESCAPED-gitconfig`, "ESCAPED-gitconfig"},
		{"writing .claude settings", secs(SecLater), `mkdir -p .claude && echo x > .claude/settings.json && echo ESCAPED-claude`, "ESCAPED-claude"},
		{"writing .vscode tasks", secs(SecLater), `mkdir -p .vscode && echo x > .vscode/tasks.json && echo ESCAPED-vscode`, "ESCAPED-vscode"},
		{"writing .mcp.json", secs(SecLater), `echo x > .mcp.json && echo ESCAPED-mcp`, "ESCAPED-mcp"},
		{"writing a shell startup file", secs(SecLater + "+" + SecWrite), `echo x >> "$HOME/.zshrc" && echo ESCAPED-zshrc`, "ESCAPED-zshrc"},
		{"planting in the owner's Go build cache", secs(SecWrite), `echo x > "$HOME/Library/Caches/go-build/planted" && echo ESCAPED-gocache`, "ESCAPED-gocache"},
		{"planting in the owner's module cache", secs(SecWrite), `echo x > "$HOME/go/pkg/mod/planted" && echo ESCAPED-modcache`, "ESCAPED-modcache"},
		{"reading .env", secs(SecSecrets), `cat .env`, "ws-dotenv"},
		{"reading an ssh key", secs(SecSecrets), `cat "$HOME/.ssh/id_test"`, "ssh-key"},
		{"reading gh's token", secs(SecSecrets), `cat "$HOME/.config/gh/hosts.yml"`, "gh-token"},
		{"reading a keychain file", secs(SecSecrets), `cat "$HOME/Library/Keychains/login.keychain-db"`, "SECRET=keychain"},
		{"listing the keychain with security", secs(SecSecrets), `/usr/bin/security list-keychains && echo ESCAPED-security`, "ESCAPED-security"},
		{"reading git credentials", secs(SecSecrets), `cat "$HOME/.git-credentials"`, "SECRET=git-cred"},
		{"reading .netrc", secs(SecSecrets), `cat "$HOME/.netrc"`, "SECRET=netrc"},
		{"reading docker's config", secs(SecSecrets), `cat "$HOME/.docker/config.json"`, "SECRET=docker"},
		{"reading Messages", secs(SecSecrets), `cat "$HOME/Library/Messages/chat.db"`, "SECRET=messages"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if out := f.run(nil, tc.script); !strings.Contains(out, tc.mark) {
				t.Fatalf("with no sandbox the escape did not get out, so this test cannot see it: %q", out)
			}
			if out := f.run(&f.cfg, tc.script); strings.Contains(out, tc.mark) {
				t.Errorf("the profile let it out: %q", out)
			}
			if out := f.run(f.without(tc.sections...), tc.script); !strings.Contains(out, tc.mark) {
				t.Errorf("with %v removed it still did not get out, so they are not what stops it: %q", tc.sections, out)
			}
			// Leave nothing behind for the next case.
			os.Remove(filepath.Join(f.ws, ".git", "hooks", "pre-commit"))
			exec.Command("git", "-C", f.ws, "config", "--unset", "core.fsmonitor").Run()
		})
	}
}

// TestWorkStillRuns: what a coding session needs works inside the profile: go test with tack's own cache, git, and
// writes to the workspace and the session's temp directory.
func TestWorkStillRuns(t *testing.T) {
	f := setup(t)
	for _, script := range []string{
		`go version`,
		`go test ./... && echo OK-gotest`,
		`git status --short && git add x_test.go && echo OK-git`,
		`echo hello > note.txt && cat note.txt`,
		`echo t > "$TMPDIR/scratch" && cat "$TMPDIR/scratch"`,
	} {
		out := f.run(&f.cfg, script)
		if strings.Contains(out, "Operation not permitted") || strings.Contains(out, "not permitted") ||
			strings.Contains(out, "FAIL") {
			t.Errorf("%q inside the profile: %s", script, out)
		}
		t.Logf("%s → %s", script, strings.TrimSpace(out))
	}
	if out := f.run(&f.cfg, `go test ./... && echo OK-gotest`); !strings.Contains(out, "OK-gotest") {
		t.Errorf("go test did not pass inside the profile: %s", out)
	}
}

// secs splits "a+b" into the sections whose removal together opens an escape.
func secs(s string) []string { return strings.Split(s, "+") }
