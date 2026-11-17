package trust

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func repoWith(t *testing.T, config string) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if config != "" {
		if err := os.MkdirAll(filepath.Join(root, ".tack"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, ConfigPath), []byte(config), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestReadRepo(t *testing.T) {
	r, digest, err := ReadRepo(repoWith(t, "[commands]\nenv = [\"CI\", \"MY_FLAG\"]\n"))
	if err != nil || len(r.Commands.Env) != 2 || r.Commands.Env[1] != "MY_FLAG" || len(digest) != 64 {
		t.Fatalf("ReadRepo = %+v, %q, %v", r, digest, err)
	}
	if r, digest, err := ReadRepo(repoWith(t, "")); err != nil || digest != "" || len(r.Commands.Env) != 0 {
		t.Errorf("no file: %+v, %q, %v; want nothing", r, digest, err)
	}
	for name, body := range map[string]string{
		"a typo":            "[commands]\nenvs = [\"CI\"]\n",
		"an unknown table":  "[hooks]\nbefore = \"x\"\n",
		"not toml":          "commands = [\n",
		"not a name":        "[commands]\nenv = [\"A B\"]\n",
		"an empty name":     "[commands]\nenv = [\"\"]\n",
		"a name with a dot": "[commands]\nenv = [\"A.B\"]\n",
	} {
		if _, digest, err := ReadRepo(repoWith(t, body)); err == nil {
			t.Errorf("%s: no error", name)
		} else if strings.Contains(name, "not a name") && digest == "" {
			t.Errorf("%s: no digest with the error", name)
		}
	}
	// A symbolic link, a directory and an oversized file are not read.
	root := repoWith(t, "")
	os.MkdirAll(filepath.Join(root, ".tack"), 0o755)
	target := filepath.Join(t.TempDir(), "elsewhere.toml")
	os.WriteFile(target, []byte("[commands]\nenv = [\"X\"]\n"), 0o644)
	os.Symlink(target, filepath.Join(root, ConfigPath))
	if _, _, err := ReadRepo(root); err == nil {
		t.Error("a symlinked config was read")
	}
	os.Remove(filepath.Join(root, ConfigPath))
	os.WriteFile(filepath.Join(root, ConfigPath), []byte("# "+strings.Repeat("x", maxConfig)+"\n"), 0o644)
	if _, _, err := ReadRepo(root); err == nil {
		t.Error("an oversized config was read")
	}
}

// TestCheck: only a file whose exact content the owner trusted is applied; no grant, a grant for other content, a grant for
// a repository with no file that later gained one, and a store that cannot be read all leave it unapplied, with a notice
// that names the command to run. A repository with no file needs nothing.
func TestCheck(t *testing.T) {
	state := t.TempDir()
	st := Open(state)
	root := repoWith(t, "[commands]\nenv = [\"CI\"]\n")

	if got := st.Check(root); got.Applied || !strings.Contains(got.Notice, "not trusted") || !strings.Contains(got.Notice, "tack trust "+root) {
		t.Errorf("untrusted: %+v", got)
	}
	_, digest, _ := ReadRepo(root)
	if err := st.Trust(root, digest, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := st.Check(root); !got.Applied || got.Notice != "" || len(got.Repo.Commands.Env) != 1 || got.Digest != digest {
		t.Errorf("trusted: %+v", got)
	}
	// The same repository reached through a symbolic link is the same repository.
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	if got := st.Check(link); !got.Applied {
		t.Errorf("trusted, by a link to it: %+v", got)
	}
	// The file changes after the grant: a commit, a pull, a tool.
	os.WriteFile(filepath.Join(root, ConfigPath), []byte("[commands]\nenv = [\"CI\", \"GH_PAT\"]\n"), 0o644)
	if got := st.Check(root); got.Applied || !strings.Contains(got.Notice, "changed since you trusted") {
		t.Errorf("changed: %+v", got)
	}
	// No file, and so nothing to trust; a grant made then does not cover a file that appears.
	bare := repoWith(t, "")
	if got := st.Check(bare); got.Applied || got.Notice != "" {
		t.Errorf("no file: %+v", got)
	}
	if err := st.Trust(bare, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(bare, ".tack"), 0o755)
	os.WriteFile(filepath.Join(bare, ConfigPath), []byte("[commands]\nenv = [\"CI\"]\n"), 0o644)
	if got := st.Check(bare); got.Applied || !strings.Contains(got.Notice, "changed since") {
		t.Errorf("a file that appeared after a grant for none: %+v", got)
	}
	// A store that cannot be read grants nothing.
	os.WriteFile(filepath.Join(state, "trust.json"), []byte("{not json"), 0o600)
	if got := st.Check(root); got.Applied || !strings.Contains(got.Notice, "cannot be read") {
		t.Errorf("a broken store: %+v", got)
	}
}

func TestTrustWritesOwnerOnlyAndKeepsOthers(t *testing.T) {
	state := t.TempDir()
	st := Open(state)
	a, b := repoWith(t, "x = 1\n"), repoWith(t, "")
	if err := st.Trust(a, "da", time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	if err := st.Trust(b, "", time.Unix(2, 0)); err != nil {
		t.Fatal(err)
	}
	if g, ok, err := st.Lookup(a); err != nil || !ok || g.Config != "da" {
		t.Errorf("first grant lost: %+v %v %v", g, ok, err)
	}
	fi, err := os.Stat(filepath.Join(state, "trust.json"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("trust.json: %v, %v; want mode 0600", fi, err)
	}
	if ents, _ := os.ReadDir(state); len(ents) != 1 {
		t.Errorf("the state directory holds %d entries, want trust.json alone", len(ents))
	}
}
