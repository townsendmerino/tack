package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/townsendmerino/tack/internal/trust"
)

func trustRun(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := dispatch(append([]string{"trust"}, args...), &out, &errb)
	return code, out.String(), errb.String()
}

func repoWithConfig(t *testing.T, body string) string {
	t.Helper()
	ws, _ := filepath.EvalSymlinks(t.TempDir())
	if body != "" {
		os.MkdirAll(filepath.Join(ws, ".tack"), 0o755)
		os.WriteFile(filepath.Join(ws, ".tack", "config.toml"), []byte(body), 0o644)
	}
	return ws
}

// asTheOwner makes the checks of who is asking pass: a terminal on stdin, outside any sandbox. Tests that exercise a
// refusal set one of them back.
func asTheOwner(t *testing.T) {
	t.Helper()
	tty, in := stdinIsTerminal, inSandboxNow
	stdinIsTerminal, inSandboxNow = func() bool { return true }, func() bool { return false }
	t.Cleanup(func() { stdinIsTerminal, inSandboxNow = tty, in })
}

// shownDigest is the digest prefix tack trust printed for -yes to take.
func shownDigest(t *testing.T, out string) string {
	t.Helper()
	_, rest, ok := strings.Cut(out, "tack trust -yes ")
	if !ok {
		t.Fatalf("the output names no digest for -yes:\n%s", out)
	}
	return strings.Fields(rest)[0]
}

// TestTrustShowsThenTrusts: without -yes, tack trust shows the file, escaped, and the digest to give back, and records
// nothing; with -yes and that digest it records the grant for exactly that content, in the state directory; a file that does
// not parse or names a secret is refused.
func TestTrustShowsThenTrusts(t *testing.T) {
	asTheOwner(t)
	cfg := writeConfig(t, "http://cassette.invalid")
	st := trust.Open(filepath.Join(filepath.Dir(cfg), "state"))
	zwsp := string(rune(0x200B))
	ws := repoWithConfig(t, "[commands]\nenv = [\"MY_FLAG\"] # note"+zwsp+"\n")

	code, out, errs := trustRun(t, "-config", cfg, ws)
	if code != exitOK || !strings.Contains(out, `MY_FLAG`) || !strings.Contains(out, `\u{200B}`) || strings.Contains(out, zwsp) {
		t.Fatalf("show: %d\n%s\n%s", code, out, errs)
	}
	shown := shownDigest(t, out)
	if _, ok, _ := st.Lookup(ws); ok {
		t.Fatal("showing the file trusted it")
	}
	code, out, errs = trustRun(t, "-config", cfg, "-yes", shown, ws)
	if code != exitOK || !strings.Contains(out, "trusted "+ws) {
		t.Fatalf("trust: %d\n%s\n%s", code, out, errs)
	}
	_, digest, _ := trust.ReadRepo(ws)
	if g, ok, err := st.Lookup(ws); err != nil || !ok || g.Config != digest {
		t.Fatalf("grant %+v %v %v; want one for the file as it is", g, ok, err)
	}
	if got := st.Check(ws); !got.Applied {
		t.Errorf("the grant does not apply: %+v", got)
	}

	for name, body := range map[string]string{"a typo": "[commands]\nenvs = [\"CI\"]\n", "a secret": "[commands]\nenv = [\"GH_TOKEN\"]\n"} {
		bad := repoWithConfig(t, body)
		code, _, errs := trustRun(t, "-config", cfg, "-yes", "deadbeef", bad)
		if code == exitOK || !strings.Contains(errs, "nothing was trusted") {
			t.Errorf("%s: %d %q", name, code, errs)
		}
		if _, ok, _ := st.Lookup(bad); ok {
			t.Errorf("%s: a grant was recorded", name)
		}
	}
	if code, _, _ := trustRun(t, "-config", cfg, filepath.Join(ws, "missing")); code == exitOK {
		t.Error("a missing directory was accepted")
	}
	bare := repoWithConfig(t, "")
	code, out, _ = trustRun(t, "-config", cfg, bare)
	if code != exitOK || !strings.Contains(out, "nothing to trust") {
		t.Errorf("no file: %d %q", code, out)
	}
	if code, out, errs := trustRun(t, "-config", cfg, "-yes", shownDigest(t, out), bare); code != exitOK {
		t.Errorf("trusting a repository with no file: %d %s %s", code, out, errs)
	}
}

// TestTrustYesIsForTheFileThatWasShown: -yes takes the digest the owner was shown and refuses when the file's digest is not
// that one, whether the file changed since or the digest is short, wrong or missing; and it refuses without a terminal on
// stdin and inside a sandbox, where a command could be the one asking. Each refusal records nothing.
func TestTrustYesIsForTheFileThatWasShown(t *testing.T) {
	cfg := writeConfig(t, "http://cassette.invalid")
	st := trust.Open(filepath.Join(filepath.Dir(cfg), "state"))
	ws := repoWithConfig(t, "[commands]\nenv = [\"MY_FLAG\"]\n")
	asTheOwner(t)
	_, out, _ := trustRun(t, "-config", cfg, ws)
	shown := shownDigest(t, out)
	refused := func(name, wantErr string, args ...string) {
		t.Helper()
		code, out, errs := trustRun(t, append([]string{"-config", cfg}, args...)...)
		if code == exitOK || !strings.Contains(errs, wantErr) || strings.Contains(out, "trusted "+ws) {
			t.Errorf("%s: %d %q %q; want a refusal saying %q", name, code, out, errs, wantErr)
		}
		if _, ok, _ := st.Lookup(ws); ok {
			t.Errorf("%s: a grant was recorded", name)
		}
	}
	refused("the wrong digest", "not the file that was shown", "-yes", "0123456789abcdef", ws)
	if len(shown) < 16 {
		t.Fatalf("the digest shown is %d characters; -yes takes 16 (64 bits), so it must show at least that", len(shown))
	}
	refused("a digest too short to mean it", "too short", "-yes", shown[:4], ws)
	refused("32 bits is within brute force", "too short", "-yes", shown[:8], ws)
	refused("48 bits is too", "too short", "-yes", shown[:12], ws)
	refused("no digest", "digest", "-yes", ws)

	// The file changes between the showing and the -yes.
	os.WriteFile(filepath.Join(ws, trust.ConfigPath), []byte("[commands]\nenv = [\"MY_FLAG\", \"MORE\"]\n"), 0o644)
	refused("a file changed since it was shown", "not the file that was shown", "-yes", shown, ws)
	os.WriteFile(filepath.Join(ws, trust.ConfigPath), []byte("[commands]\nenv = [\"MY_FLAG\"]\n"), 0o644)

	stdinIsTerminal = func() bool { return false }
	refused("no terminal on stdin", "terminal", "-yes", shown, ws)
	stdinIsTerminal = func() bool { return true }
	inSandboxNow = func() bool { return true }
	refused("inside a sandbox", "sandbox", "-yes", shown, ws)
	inSandboxNow = func() bool { return false }

	if code, out, errs := trustRun(t, "-config", cfg, "-yes", strings.ToUpper(shown), ws); code != exitOK || !strings.Contains(out, "trusted "+ws) {
		t.Errorf("the shown digest, in capitals: %d %s %s", code, out, errs)
	}
}
