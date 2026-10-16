package config

import (
	"os"
	"path/filepath"
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
}

func TestLoadFailsLoudly(t *testing.T) {
	cases := map[string]struct{ body, want string }{
		"typo":          {"[servers.a]\nkind = \"goinfer-remote\"\nurl = \"http://x\"\nurll = \"y\"\n", "unknown keys: servers.a.urll"},
		"unknown kind":  {"[servers.a]\nkind = \"ollama\"\n", "kind"},
		"child no ctx":  {"[servers.a]\nkind = \"goinfer-child\"\nbinary = \"b\"\nmodel_path = \"m\"\n", "ctx"},
		"remote no url": {"[servers.a]\nkind = \"goinfer-remote\"\n", "url"},
		"mixed":         {"[servers.a]\nkind = \"goinfer-remote\"\nurl = \"u\"\nctx = 4\n", "for goinfer-child"},
		"main missing":  {"main = \"z\"\n[servers.a]\nkind = \"goinfer-remote\"\nurl = \"u\"\n", "names no server"},
		"no servers":    {"main = \"z\"\n", "no [servers"},
		"bad duration":  {"[servers.a]\nkind = \"goinfer-child\"\nbinary = \"b\"\nmodel_path = \"m\"\nctx = 1\nidle_unload = \"soon\"\n", "duration"},
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
