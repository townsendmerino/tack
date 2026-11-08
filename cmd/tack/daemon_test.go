package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
)

// TestDaemonCommands drives the daemon through the CLI: tack daemon run serves; a second run finds it and exits 0;
// status describes it; tack run --daemon asks a question through it; stop drains it, and the first run returns 0;
// status then says it is not running. The state directory is short, under /tmp, as a socket path must be.
func TestDaemonCommands(t *testing.T) {
	ts := httptest.NewServer(goinfertest.NewScripted("m", 16384, goinfertest.Says("answered through the daemon")))
	defer ts.Close()
	dir, err := os.MkdirTemp("/tmp", "tack-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	cfg := filepath.Join(dir, "config.toml")
	body := "state_dir = \"" + filepath.Join(dir, "s") + "\"\n[daemon]\nshutdown_grace = \"1s\"\n" +
		"[servers.local]\nkind = \"goinfer-remote\"\nurl = \"" + ts.URL + "\"\n"
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	os.WriteFile(filepath.Join(ws, "a.go"), []byte("package a\n"), 0o644)
	cli := func(args ...string) (int, string, string) {
		var out, errb bytes.Buffer
		code := dispatch(args, &out, &errb)
		return code, out.String(), errb.String()
	}

	var runErr bytes.Buffer
	ran := make(chan int, 1)
	go func() { ran <- dispatch([]string{"daemon", "run", "-config", cfg}, &bytes.Buffer{}, &runErr) }()
	t.Cleanup(func() { cli("daemon", "stop", "-config", cfg) })
	deadline := time.Now().Add(5 * time.Second)
	var code int
	var out, errs string
	for {
		if code, out, errs = cli("daemon", "status", "-config", cfg); code == exitOK || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if code != exitOK || !strings.Contains(out, "tack daemon "+version) || !strings.Contains(out, "0 sessions") {
		t.Fatalf("status: exit %d, %q %q", code, out, errs)
	}
	if code, _, errs = cli("daemon", "run", "-config", cfg); code != exitOK || !strings.Contains(errs, "holds the lock") {
		t.Fatalf("a second daemon run: exit %d, %q; want 0 and the first named", code, errs)
	}
	if code, out, errs = cli("run", "--daemon", "--config", cfg, "--workspace", ws, "what is here?"); code != exitOK || !strings.Contains(out, "answered through the daemon") {
		t.Fatalf("run --daemon: exit %d, %q %q", code, out, errs)
	}
	if code, out, _ = cli("daemon", "status", "-config", cfg); !strings.Contains(out, "1 sessions") {
		t.Fatalf("status after the question: exit %d, %q; want the session counted", code, out)
	}
	if code, out, errs = cli("daemon", "stop", "-config", cfg); code != exitOK || !strings.Contains(out, "stopped") {
		t.Fatalf("stop: exit %d, %q %q", code, out, errs)
	}
	select {
	case code := <-ran:
		if code != exitOK {
			t.Fatalf("daemon run exited %d: %s", code, runErr.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("daemon run did not return after stop")
	}
	if code, out, _ = cli("daemon", "status", "-config", cfg); code != exitNotRunning || !strings.Contains(out, "not running") {
		t.Fatalf("status after stop: exit %d, %q", code, out)
	}
}

// TestRunDaemonRefusesInProcessFlags: what only an in-process run can do is refused with --daemon, before anything
// starts.
func TestRunDaemonRefusesInProcessFlags(t *testing.T) {
	for _, flags := range [][]string{{"--edit"}, {"--instructions", "whole"}, {"--server", "x"}, {"--replay", "c.json"}} {
		var out, errb bytes.Buffer
		args := append(append([]string{"run", "--daemon"}, flags...), "q")
		if code := dispatch(args, &out, &errb); code != exitUsage || !strings.Contains(errb.String(), "in this process only") {
			t.Errorf("run --daemon %v: exit %d, %q", flags, code, errb.String())
		}
	}
}

// TestRunSaysTheModelIsLoading: tack run on an on-demand server waits for its load and says so on stderr, as the
// status line will: "loading model on <server>".
func TestRunSaysTheModelIsLoading(t *testing.T) {
	model := goinfertest.NewScripted("m", 16384, goinfertest.Says("loaded"))
	var mu sync.Mutex
	state := "idle"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/tack/start":
			state = "loading"
		case "/tack/status":
			if state == "loading" {
				state = "ready"
				w.Write([]byte(`{"state":"loading"}`))
				return
			}
		default:
			model.ServeHTTP(w, r)
			return
		}
		w.Write([]byte(`{"state":"` + state + `"}`))
	}))
	defer ts.Close()
	cfg := writeConfig(t, ts.URL)
	f, _ := os.OpenFile(cfg, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("on_demand = true\n")
	f.Close()
	ws := t.TempDir()
	var out, errb bytes.Buffer
	if code := dispatch([]string{"run", "--config", cfg, "--workspace", ws, "hi"}, &out, &errb); code != exitOK {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "tack: loading model on local") || !strings.Contains(out.String(), "loaded") {
		t.Fatalf("stderr %q, stdout %q; want the load announced, then the answer", errb.String(), out.String())
	}
}
