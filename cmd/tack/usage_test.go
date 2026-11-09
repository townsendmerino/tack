package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/config"
	"github.com/townsendmerino/tack/internal/daemon"
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

// usageDaemon answers on stateDir's socket as a daemon would: initialize, then usage/summary with rows, or, with rows
// nil, method not found, as a daemon from before usage/summary does. It counts the summaries asked for.
func usageDaemon(t *testing.T, stateDir string, rows []proto.UsageRow) *int {
	t.Helper()
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", daemon.SocketPath(stateDir))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	asked := new(int)
	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer nc.Close()
				sc, enc := bufio.NewScanner(nc), json.NewEncoder(nc)
				for sc.Scan() {
					var req proto.Request
					if json.Unmarshal(sc.Bytes(), &req) != nil {
						return
					}
					resp := proto.Response{JSONRPC: "2.0", ID: req.ID}
					switch {
					case req.Method == proto.MethodInitialize:
						resp.Result, _ = json.Marshal(proto.InitializeResult{Protocol: proto.Version})
					case req.Method == proto.MethodUsageSummary && rows != nil:
						*asked++
						resp.Result, _ = json.Marshal(proto.UsageSummaryResult{Rows: rows})
					default:
						resp.Error = &proto.Error{Code: proto.CodeMethodNotFound, Message: "method not found: " + req.Method}
					}
					enc.Encode(resp)
				}
			}()
		}
	}()
	return asked
}

// TestUsageAsksTheDaemon: with a daemon running, tack usage prints the table usage/summary answers with. With a
// daemon from before usage/summary, or none, it reads the logs itself.
func TestUsageAsksTheDaemon(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "tack-") // short: a socket path
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	state := filepath.Join(dir, "s")
	cfg := filepath.Join(dir, "config.toml")
	os.WriteFile(cfg, []byte("state_dir = \""+state+"\"\n[servers.local]\nkind = \"goinfer-remote\"\nurl = \"http://127.0.0.1:1\"\n"), 0o600)
	st, err := store.Open(state)
	if err != nil {
		t.Fatal(err)
	}
	l, err := st.Create("s_local")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(proto.EvUsage, 0, proto.Usage{Request: 1, Server: "from-the-logs", Prompt: 5, Output: 1}); err != nil {
		t.Fatal(err)
	}
	l.Close()

	asked := usageDaemon(t, state, []proto.UsageRow{{Day: "2026-11-09", Server: "from-the-daemon", Sessions: 1, Requests: 3, Prompt: 900}})
	var out, errb bytes.Buffer
	if code := dispatch([]string{"usage", "-config", cfg, "-since", "all"}, &out, &errb); code != exitOK || *asked != 1 ||
		!strings.Contains(out.String(), "from-the-daemon") || strings.Contains(out.String(), "from-the-logs") {
		t.Fatalf("with a daemon: exit %d, asked %d:\n%s%s", code, *asked, out.String(), errb.String())
	}
}

// TestUsageFallsBackToTheLogs: a daemon that does not know usage/summary (one from before it) leaves tack usage to
// read the logs itself, as it does with no daemon at all.
func TestUsageFallsBackToTheLogs(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "tack-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	state := filepath.Join(dir, "s")
	cfg := filepath.Join(dir, "config.toml")
	os.WriteFile(cfg, []byte("state_dir = \""+state+"\"\n[servers.local]\nkind = \"goinfer-remote\"\nurl = \"http://127.0.0.1:1\"\n"), 0o600)
	st, err := store.Open(state)
	if err != nil {
		t.Fatal(err)
	}
	l, err := st.Create("s_local")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(proto.EvUsage, 0, proto.Usage{Request: 1, Server: "from-the-logs", Prompt: 5, Output: 1}); err != nil {
		t.Fatal(err)
	}
	l.Close()
	usageDaemon(t, state, nil)
	var out, errb bytes.Buffer
	if code := dispatch([]string{"usage", "-config", cfg, "-since", "all"}, &out, &errb); code != exitOK || !strings.Contains(out.String(), "from-the-logs") {
		t.Fatalf("with an older daemon: exit %d:\n%s%s", code, out.String(), errb.String())
	}
}
