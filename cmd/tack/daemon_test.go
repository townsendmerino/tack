package main

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/daemon"
	"github.com/townsendmerino/tack/internal/proto"
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
	if h := daemon.ReadHolder(filepath.Join(dir, "s")); h.PID != 0 {
		t.Fatalf("stop returned with the lock still held by %+v", h)
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

// TestReleasedWaitsForTheLock: tack daemon stop waits until the daemon it stopped has let go of the lock, not only
// until its socket stops answering, which a shutdown takes away first.
func TestReleasedWaitsForTheLock(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "tack-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	l, err := daemon.Acquire(filepath.Join(dir, "s"), "1")
	if err != nil {
		t.Fatal(err)
	}
	if released(filepath.Join(dir, "s"), os.Getpid(), 200*time.Millisecond) {
		t.Fatal("released while the lock is held")
	}
	l.Release()
	if !released(filepath.Join(dir, "s"), os.Getpid(), time.Second) {
		t.Fatal("not released after Release")
	}
}

// TestStatusAndStopSeeADrainingDaemon: a daemon shutting down has taken its socket away (so a new client starts the
// next one) but still holds the lock while its turns drain. status reports it shutting down, not "not running"; stop
// waits for it to let go of the lock.
func TestStatusAndStopSeeADrainingDaemon(t *testing.T) {
	inRequest := make(chan struct{}, 1)
	models := goinfertest.NewScripted("m", 16384)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			models.ServeHTTP(w, r)
			return
		}
		io.Copy(io.Discard, r.Body)
		select {
		case inRequest <- struct{}{}:
		default:
		}
		<-r.Context().Done()
	}))
	defer ts.Close()
	dir, err := os.MkdirTemp("/tmp", "tack-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	stateDir := filepath.Join(dir, "s")
	cfg := filepath.Join(dir, "config.toml")
	body := "state_dir = \"" + stateDir + "\"\n[daemon]\nshutdown_grace = \"2s\"\n" +
		"[servers.local]\nkind = \"goinfer-remote\"\nurl = \"" + ts.URL + "\"\n"
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cli := func(args ...string) (int, string, string) {
		var out, errb bytes.Buffer
		code := dispatch(args, &out, &errb)
		return code, out.String(), errb.String()
	}
	ran := make(chan int, 1)
	go func() { ran <- dispatch([]string{"daemon", "run", "-config", cfg}, &bytes.Buffer{}, &bytes.Buffer{}) }()
	var c *daemon.Conn
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if c, err = daemon.Dial(stateDir, cliClient, nil, 0); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the daemon never served: %v", err)
		}
	}
	var sess proto.SessionNewResult
	if err := c.Call(proto.MethodSessionNew, proto.SessionNewParams{Workspace: t.TempDir(), Model: "main", Mode: proto.ModePlan}, &sess); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: sess.Session, Content: []proto.ContentBlock{proto.Text("q")}}, nil); err != nil {
		t.Fatal(err)
	}
	<-inRequest // the turn is at the model, so the drain lasts its grace
	if err := c.Call(proto.MethodDaemonShutdown, proto.DaemonShutdownParams{}, nil); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(2 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat(daemon.SocketPath(stateDir)); errors.Is(err, fs.ErrNotExist) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the socket stayed after the shutdown")
		}
	}
	if code, out, errs := cli("daemon", "status", "-config", cfg); code != exitNotServing || !strings.Contains(out, "shutting down") {
		t.Fatalf("status while it drains: exit %d, %q %q; want %d and \"shutting down\"", code, out, errs, exitNotServing)
	}
	if code, out, errs := cli("daemon", "stop", "-config", cfg); code != exitOK || !strings.Contains(out, "stopped") {
		t.Fatalf("stop while it drains: exit %d, %q %q; want it to wait and say stopped", code, out, errs)
	}
	if h := daemon.ReadHolder(stateDir); h.PID != 0 {
		t.Fatalf("stop returned with the lock still held by %+v", h)
	}
	select {
	case <-ran:
	case <-time.After(10 * time.Second):
		t.Fatal("daemon run did not return")
	}
}
