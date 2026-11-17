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

// TestTrustShowsThenTrusts: without -yes, tack trust shows the file, escaped, and records nothing; with -yes it records the
// grant for exactly that content, in the state directory; a file that does not parse or names a secret is refused.
func TestTrustShowsThenTrusts(t *testing.T) {
	cfg := writeConfig(t, "http://cassette.invalid")
	st := trust.Open(filepath.Join(filepath.Dir(cfg), "state"))
	zwsp := string(rune(0x200B))
	ws := repoWithConfig(t, "[commands]\nenv = [\"MY_FLAG\"] # note"+zwsp+"\n")

	code, out, errs := trustRun(t, "-config", cfg, ws)
	if code != exitOK || !strings.Contains(out, `MY_FLAG`) || !strings.Contains(out, `\u{200B}`) || strings.Contains(out, zwsp) ||
		!strings.Contains(out, "tack trust -yes") {
		t.Fatalf("show: %d\n%s\n%s", code, out, errs)
	}
	if _, ok, _ := st.Lookup(ws); ok {
		t.Fatal("showing the file trusted it")
	}
	code, out, errs = trustRun(t, "-config", cfg, "-yes", ws)
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
		code, _, errs := trustRun(t, "-config", cfg, "-yes", bad)
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
	if code, out, _ := trustRun(t, "-config", cfg, bare); code != exitOK || !strings.Contains(out, "nothing to trust") {
		t.Errorf("no file: %d %q", code, out)
	}
}
