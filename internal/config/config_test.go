package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func load(t *testing.T, body string) (*Config, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(p)
}

func TestLoadDefaults(t *testing.T) {
	c, err := load(t, `
main = "mac"
[servers.mac]
kind = "goinfer-child"
binary = "/opt/goinfer-serve"
model_path = "/m/qwen2.5-7b-instruct-q4_k_m.gguf"
ctx = 16384
args = ["-backend", "metal"]
[servers.nobara]
kind = "goinfer-remote"
url = "http://nobara:8080"
model = "qwen2.5-7b-instruct-q4_k_m"
`)
	if err != nil {
		t.Fatal(err)
	}
	mac := c.Servers["mac"]
	if mac.Model != "qwen2.5-7b-instruct-q4_k_m" || mac.IdleUnload.Duration != 10*time.Minute || mac.IdleExit.Duration != 30*time.Minute {
		t.Fatalf("child defaults: %+v", mac)
	}
	if c.Run.MaxRequests != 40 || c.Run.MaxToolCalls != 100 || c.Run.MaxTokens != 2048 || c.Run.MaxWall.Duration != 30*time.Minute {
		t.Fatalf("run defaults: %+v", c.Run)
	}
	if c.Run.TurnTokens != 200_000 || c.Run.MaxErrors != 8 || c.Run.SessionTokens != 2_000_000 || c.Run.SessionToolCalls != 1000 {
		t.Fatalf("budget defaults: %+v", c.Run)
	}
}

func TestLoadFailsLoudly(t *testing.T) {
	cases := map[string]struct{ body, want string }{
		"typo":                  {"[servers.a]\nkind = \"goinfer-remote\"\nurl = \"http://x\"\nurll = \"y\"\n", "unknown keys: servers.a.urll"},
		"unknown kind":          {"[servers.a]\nkind = \"ollama\"\n", "kind"},
		"child no ctx":          {"[servers.a]\nkind = \"goinfer-child\"\nbinary = \"b\"\nmodel_path = \"m\"\n", "ctx"},
		"remote no url":         {"[servers.a]\nkind = \"goinfer-remote\"\n", "url"},
		"mixed":                 {"[servers.a]\nkind = \"goinfer-remote\"\nurl = \"u\"\nctx = 4\n", "for goinfer-child"},
		"main missing":          {"main = \"z\"\n[servers.a]\nkind = \"goinfer-remote\"\nurl = \"u\"\n", "names no server"},
		"no servers":            {"main = \"z\"\n", "no [servers"},
		"bad duration":          {"[servers.a]\nkind = \"goinfer-child\"\nbinary = \"b\"\nmodel_path = \"m\"\nctx = 1\nidle_unload = \"soon\"\n", "duration"},
		"negative temperature":  {"[servers.a]\nkind = \"goinfer-remote\"\nurl = \"u\"\ntemperature = -0.1\n", "temperature must be"},
		"top_p over 1":          {"[servers.a]\nkind = \"goinfer-remote\"\nurl = \"u\"\ntop_p = 1.5\n", "top_p in (0, 1]"},
		"top_k zero":            {"[servers.a]\nkind = \"goinfer-remote\"\nurl = \"u\"\ntop_k = 0\n", "top_k at least 1"},
		"negative budget":       {"[servers.a]\nkind = \"goinfer-remote\"\nurl = \"u\"\n[run]\nmax_errors = -1\n", "negative"},
		"best_of over the cap":  {"[servers.a]\nkind = \"goinfer-remote\"\nurl = \"u\"\n[run]\nbest_of = 6\n", "best_of is 6"},
		"best_of negative":      {"[servers.a]\nkind = \"goinfer-remote\"\nurl = \"u\"\n[run]\nbest_of = -1\n", "best_of is -1"},
		"sandbox read relative": {"[servers.a]\nkind = \"goinfer-remote\"\nurl = \"u\"\n[sandbox]\nread = [\"models\"]\n", "sandbox.read[0]"},
		"sandbox read root":     {"[servers.a]\nkind = \"goinfer-remote\"\nurl = \"u\"\n[sandbox]\nread = [\"/\"]\n", "not ~ or a directory holding it"},
		"sandbox read home":     {"[servers.a]\nkind = \"goinfer-remote\"\nurl = \"u\"\n[sandbox]\nread = [\"~/\"]\n", "not ~ or a directory holding it"},
	}
	for name, tc := range cases {
		if _, err := load(t, tc.body); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want an error containing %q", name, err, tc.want)
		}
	}
}

func TestReadTokenRefusesAReadableFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "tok")
	os.WriteFile(p, []byte("secret\n"), 0o644)
	if _, err := ReadToken(p); err == nil {
		t.Fatal("a 0644 token file was read")
	}
	os.Chmod(p, 0o600)
	if tok, err := ReadToken(p); err != nil || tok != "secret" {
		t.Fatalf("ReadToken = %q, %v", tok, err)
	}
}

func TestKenSection(t *testing.T) {
	base := "[servers.a]\nkind = \"goinfer-remote\"\nurl = \"u\"\n"
	c, err := load(t, base)
	if err != nil || c.Ken != nil {
		t.Fatalf("no [ken]: %+v, %v", c.Ken, err)
	}
	c, err = load(t, base+"[ken]\nmodel_dir = \"/m/ken\"\n")
	if err != nil || c.Ken.Mode != "hybrid" || c.Ken.ModelDir != "/m/ken" {
		t.Fatalf("[ken]: %+v, %v", c.Ken, err)
	}
	if c, err = load(t, base+"[ken]\nmode = \"bm25\"\n"); err != nil || c.Ken.ModelDir != "" {
		t.Fatalf("bm25 needs no model: %+v, %v", c.Ken, err)
	}
	for body, want := range map[string]string{
		"[ken]\n":                   "model_dir is required",
		"[ken]\nmode = \"fuzzy\"\n": "ken.mode",
	} {
		if _, err := load(t, base+body); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v, want %q", body, err, want)
		}
	}
}

func TestServerSampling(t *testing.T) {
	c, err := load(t, "[servers.a]\nkind = \"goinfer-remote\"\nurl = \"u\"\ntemperature = 0.7\ntop_p = 0.8\ntop_k = 20\n")
	if err != nil {
		t.Fatal(err)
	}
	s := c.Servers["a"]
	if s.Temperature == nil || *s.Temperature != 0.7 || s.TopP == nil || *s.TopP != 0.8 || s.TopK == nil || *s.TopK != 20 {
		t.Fatalf("sampling %+v", s)
	}
	if c, _ := load(t, "[servers.a]\nkind = \"goinfer-remote\"\nurl = \"u\"\n"); c.Servers["a"].TopK != nil {
		t.Fatal("unset top_k is not nil")
	}
}

// [sandbox] read takes ~/ paths, expanded; with no [sandbox], nothing is added.
func TestSandboxRead(t *testing.T) {
	base := "[servers.a]\nkind = \"goinfer-remote\"\nurl = \"u\"\n"
	home, _ := os.UserHomeDir()
	c, err := load(t, base+"[sandbox]\nread = [\"~/models\", \"/opt/data/\"]\n")
	if err != nil || len(c.Sandbox.Read) != 2 || c.Sandbox.Read[0] != filepath.Join(home, "models") || c.Sandbox.Read[1] != "/opt/data" {
		t.Fatalf("read %q, %v", c.Sandbox.Read, err)
	}
	if c, err := load(t, base); err != nil || c.Sandbox.Read != nil {
		t.Fatalf("no [sandbox]: %q, %v", c.Sandbox.Read, err)
	}
	// A directory holding the home directory is refused; a symlink is stored as its target.
	if _, err := load(t, base+"[sandbox]\nread = ["+strconv.Quote(filepath.Dir(home))+"]\n"); err == nil || !strings.Contains(err.Error(), "holding it") {
		t.Errorf("%s, which holds ~, was accepted: %v", filepath.Dir(home), err)
	}
	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "models")
	os.Symlink(target, link)
	real, _ := filepath.EvalSymlinks(target)
	if c, err := load(t, base+"[sandbox]\nread = ["+strconv.Quote(link)+"]\n"); err != nil || c.Sandbox.Read[0] != real {
		t.Errorf("a symlink: %q, %v; want its target %s", c.Sandbox.Read, err, real)
	}
}

// lean_results is a [run] setting, off by default.
func TestLeanResultsSetting(t *testing.T) {
	base := "[servers.a]\nkind = \"goinfer-remote\"\nurl = \"u\"\n"
	if c, err := load(t, base); err != nil || c.Run.LeanResults {
		t.Fatalf("default: %v, %v", c.Run.LeanResults, err)
	}
	if c, err := load(t, base+"[run]\nlean_results = true\n"); err != nil || !c.Run.LeanResults {
		t.Fatalf("on: %v, %v", c.Run.LeanResults, err)
	}
}

// compact_at_cliff is a [run] setting, off by default.
func TestCompactAtCliffSetting(t *testing.T) {
	base := "[servers.a]\nkind = \"goinfer-remote\"\nurl = \"u\"\n"
	if c, err := load(t, base); err != nil || c.Run.CompactAtCliff {
		t.Fatalf("default: %v, %v", c.Run.CompactAtCliff, err)
	}
	if c, err := load(t, base+"[run]\ncompact_at_cliff = true\n"); err != nil || !c.Run.CompactAtCliff {
		t.Fatalf("on: %v, %v", c.Run.CompactAtCliff, err)
	}
}

// widen_repeats is a [run] setting, off by default, apart from compact_at_cliff.
func TestWidenRepeatsSetting(t *testing.T) {
	base := "[servers.a]\nkind = \"goinfer-remote\"\nurl = \"u\"\n"
	if c, err := load(t, base+"[run]\ncompact_at_cliff = true\n"); err != nil || c.Run.WidenRepeats {
		t.Fatalf("compact_at_cliff alone: widen_repeats %v, %v", c.Run.WidenRepeats, err)
	}
	if c, err := load(t, base+"[run]\nwiden_repeats = true\n"); err != nil || !c.Run.WidenRepeats || c.Run.CompactAtCliff {
		t.Fatalf("widen_repeats alone: %+v, %v", c.Run, err)
	}
}

func TestSnapshotSettings(t *testing.T) {
	load := func(run string) (*Config, error) {
		p := filepath.Join(t.TempDir(), "config.toml")
		os.WriteFile(p, []byte("main = \"a\"\n[servers.a]\nkind = \"goinfer-child\"\nbinary = \"/g\"\nmodel_path = \"/m/x.gguf\"\nctx = 16384\n[run]\n"+run), 0o600)
		return Load(p)
	}
	c, err := load("")
	if err != nil {
		t.Fatal(err)
	}
	if !*c.Run.Snapshots || c.Run.SnapshotMaxBytes != 512<<20 {
		t.Errorf("defaults: %v %d", *c.Run.Snapshots, c.Run.SnapshotMaxBytes)
	}
	if c, err = load("snapshots = false\nsnapshot_max_bytes = 7\n"); err != nil {
		t.Fatal(err)
	}
	if *c.Run.Snapshots || c.Run.SnapshotMaxBytes != 7 {
		t.Errorf("set: %v %d", *c.Run.Snapshots, c.Run.SnapshotMaxBytes)
	}
	if _, err = load("snapshot_max_bytes = -1\n"); err == nil || !strings.Contains(err.Error(), "negative") {
		t.Errorf("negative: %v", err)
	}
}

// TestDaemonSettings: [daemon] shutdown_grace defaults to 10s, is read as a duration, and must be positive.
func TestDaemonSettings(t *testing.T) {
	const server = "[servers.a]\nkind = \"goinfer-remote\"\nurl = \"http://x\"\n"
	c, err := load(t, server)
	if err != nil || c.Daemon.ShutdownGrace.Duration != 10*time.Second {
		t.Fatalf("default: %+v, %v; want a 10s grace", c, err)
	}
	if c, err = load(t, server+"[daemon]\nshutdown_grace = \"3s\"\n"); err != nil || c.Daemon.ShutdownGrace.Duration != 3*time.Second {
		t.Fatalf("set: %+v, %v; want 3s", c, err)
	}
	if _, err = load(t, server+"[daemon]\nshutdown_grace = \"-1s\"\n"); err == nil || !strings.Contains(err.Error(), "shutdown_grace") {
		t.Fatalf("negative: %v, want refused", err)
	}
}
