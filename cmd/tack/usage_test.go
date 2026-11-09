package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/config"
	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/store"
)

// TestUsageCommand: tack usage reads every session in the config's state directory and prints the day's row; a bad
// -since is a usage error, and a config that is missing where it was named fails.
func TestUsageCommand(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	cfg := filepath.Join(dir, "config.toml")
	os.WriteFile(cfg, []byte("state_dir = \""+state+"\"\n[servers.local]\nkind = \"goinfer-remote\"\nurl = \"http://127.0.0.1:1\"\n"), 0o600)
	st, err := store.Open(state)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []proto.SessionID{"s_a", "s_b"} {
		l, err := st.Create(id)
		if err != nil {
			t.Fatal(err)
		}
		r := 700
		if _, err := l.Append(proto.EvUsage, 0, proto.Usage{Request: 1, Server: "goinfer@nobara", Prompt: 1000, Reused: &r, Output: 40, WallMS: 1200}); err != nil {
			t.Fatal(err)
		}
		l.Close()
	}
	// The day is the logged event's own, and the clock is pinned to it, so the test reads the same across midnight.
	evs, err := st.Events("s_a", 0)
	if err != nil {
		t.Fatal(err)
	}
	at := evs[0].Time
	usageNow = func() time.Time { return at }
	t.Cleanup(func() { usageNow = time.Now })
	var out, errb bytes.Buffer
	if code := dispatch([]string{"usage", "-config", cfg, "-since", "1d"}, &out, &errb); code != exitOK {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), at.Local().Format(time.DateOnly)) || !strings.Contains(out.String(), "goinfer@nobara") ||
		!strings.Contains(out.String(), "70% (2/2)") || !strings.Contains(out.String(), "2000") {
		t.Errorf("table:\n%s", out.String())
	}
	out.Reset()
	if code := dispatch([]string{"usage", "-config", cfg, "-utc", "-since", "all"}, &out, &errb); code != exitOK ||
		!strings.Contains(out.String(), at.UTC().Format(time.DateOnly)+"  goinfer@nobara") {
		t.Errorf("-utc: exit %d:\n%s", code, out.String())
	}
	usageNow = func() time.Time { return at.AddDate(0, 0, 3) }
	out.Reset()
	if code := dispatch([]string{"usage", "-config", cfg, "-since", "1d"}, &out, &errb); code != exitOK ||
		!strings.Contains(out.String(), "(no requests in range)") {
		t.Errorf("three days on, -since 1d: exit %d:\n%s", code, out.String())
	}
	if code := dispatch([]string{"usage", "-config", cfg, "-since", "soon"}, &out, &errb); code != exitUsage {
		t.Errorf("bad -since: exit %d", code)
	}
	if code := dispatch([]string{"usage", "-config", filepath.Join(dir, "none.toml")}, &out, &errb); code != exitError {
		t.Errorf("a named config that is missing: exit %d", code)
	}
}

// TestUsageHelpNamesItsFlags: tack help shows every flag tack usage takes.
func TestUsageHelpNamesItsFlags(t *testing.T) {
	var out bytes.Buffer
	dispatch([]string{"help"}, &out, &out)
	if !strings.Contains(out.String(), "tack usage [-since 7d|DATE|all] [-utc] [-config PATH]") {
		t.Errorf("help:\n%s", out.String())
	}
}

func TestParseSince(t *testing.T) {
	now := time.Date(2026, 11, 8, 15, 4, 0, 0, time.UTC)
	for s, want := range map[string]time.Time{"1d": time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC), "7d": time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC),
		"2026-10-31": time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC), "all": {}} {
		if got, err := parseSince(s, now); err != nil || !got.Equal(want) {
			t.Errorf("%s: %v %v, want %v", s, got, err, want)
		}
	}
	for _, s := range []string{"0d", "-2d", "x", "2026-13-01"} {
		if _, err := parseSince(s, now); err == nil {
			t.Errorf("%s parsed", s)
		}
	}
}

// TestUsageWithNoConfigReadsTheDefaultState: with no config file at the default path, tack usage reads the default
// state directory, where a seeded session's row appears; a default config that is there but wrong fails loudly.
func TestUsageWithNoConfigReadsTheDefaultState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir, err := config.StateDir()
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	l, err := st.Create("s_default")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(proto.EvUsage, 0, proto.Usage{Request: 1, Server: "in-the-default-state", Prompt: 5, Output: 1}); err != nil {
		t.Fatal(err)
	}
	l.Close()
	var out, errb bytes.Buffer
	if code := dispatch([]string{"usage", "-since", "all"}, &out, &errb); code != exitOK || !strings.Contains(out.String(), "in-the-default-state") {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	os.MkdirAll(filepath.Join(home, ".config", "tack"), 0o700)
	os.WriteFile(filepath.Join(home, ".config", "tack", "config.toml"), []byte("stat_dir = \"typo\"\n"), 0o600)
	if code := dispatch([]string{"usage"}, &out, &errb); code != exitError {
		t.Errorf("a default config with a typo: exit %d", code)
	}
}
