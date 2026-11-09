package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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
	for _, flags := range [][]string{{"--edit"}, {"--instructions", "whole"}, {"--server", "x"}, {"--record", "c.json"}, {"--replay", "c.json"}} {
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

// TestReleasedWaitsForTheLock: tack daemon stop waits until the daemon it stopped has let go of the lock itself: not
// only until its socket stops answering, which a shutdown takes away first, nor until its record is gone.
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
	// Release removes the record just before it unlocks, and a record may be unreadable: the flock is what says.
	if err := os.Remove(filepath.Join(dir, "s", daemon.HolderName)); err != nil {
		t.Fatal(err)
	}
	if released(filepath.Join(dir, "s"), os.Getpid(), 200*time.Millisecond) {
		t.Fatal("released while the lock is held, its record gone")
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

// TestTheDaemonsStderrTakesItsGoroutinesWrites: the daemon writes stderr from Run (serving, shutting down) and from
// the core's callbacks on each connection's goroutine (here ken opening for a new session), unordered with each other;
// daemon run serializes them, so a stderr that is not safe for that, as this buffer, is not raced (run under -race).
func TestTheDaemonsStderrTakesItsGoroutinesWrites(t *testing.T) {
	ts := httptest.NewServer(goinfertest.NewScripted("m", 16384))
	defer ts.Close()
	dir, err := os.MkdirTemp("/tmp", "tack-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	stateDir := filepath.Join(dir, "s")
	cfg := filepath.Join(dir, "config.toml")
	body := "state_dir = \"" + stateDir + "\"\n[daemon]\nshutdown_grace = \"1s\"\n[ken]\nmode = \"bm25\"\n" +
		"[servers.local]\nkind = \"goinfer-remote\"\nurl = \"" + ts.URL + "\"\n"
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	ran := make(chan int, 1)
	go func() { ran <- dispatch([]string{"daemon", "run", "-config", cfg}, &bytes.Buffer{}, &stderr) }()
	var c *daemon.Conn
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if c, err = daemon.Dial(stateDir, cliClient, nil, 0); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the daemon never served: %v", err)
		}
	}
	ws := t.TempDir()
	os.WriteFile(filepath.Join(ws, "a.go"), []byte("package a\n"), 0o644)
	if err := c.Call(proto.MethodSessionNew, proto.SessionNewParams{Workspace: ws, Model: "main", Mode: proto.ModePlan}, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(proto.MethodDaemonShutdown, proto.DaemonShutdownParams{}, nil); err != nil {
		t.Fatal(err)
	}
	c.Close()
	select {
	case <-ran:
	case <-time.After(10 * time.Second):
		t.Fatal("daemon run did not return")
	}
	if out := stderr.String(); !strings.Contains(out, "serving on") || !strings.Contains(out, "opening ken") {
		t.Fatalf("stderr %q; want Run's lines and the core's", out)
	}
}

// TestStatusAndStopAfterTheDaemonDies: a daemon killed outright (a second signal, a crash) leaves its holder record and
// its socket behind, but not its lock, which the kernel drops with the process. Its record names a process that is
// gone, whether it was serving or draining, so status says no daemon is running and stop has nothing to wait for.
func TestStatusAndStopAfterTheDaemonDies(t *testing.T) {
	for _, draining := range []bool{false, true} {
		t.Run(map[bool]string{false: "killed while serving", true: "killed while draining"}[draining], func(t *testing.T) {
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
			body := "state_dir = \"" + stateDir + "\"\n[daemon]\nshutdown_grace = \"30s\"\n" +
				"[servers.local]\nkind = \"goinfer-remote\"\nurl = \"" + ts.URL + "\"\n"
			if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(os.Args[0], "daemon", "run", "-config", cfg) // this binary, standing in for tack (TestMain)
			cmd.Env = append(os.Environ(), "TACK_TEST_AS_TACK=1")
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			exited := make(chan struct{})
			go func() { cmd.Wait(); close(exited) }()
			t.Cleanup(func() { cmd.Process.Kill(); <-exited })
			var c *daemon.Conn
			for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
				if c, err = daemon.Dial(stateDir, cliClient, nil, 0); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("the daemon never served: %v", err)
				}
			}
			if draining {
				var sess proto.SessionNewResult
				if err := c.Call(proto.MethodSessionNew, proto.SessionNewParams{Workspace: t.TempDir(), Model: "main", Mode: proto.ModePlan}, &sess); err != nil {
					t.Fatal(err)
				}
				if err := c.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: sess.Session, Content: []proto.ContentBlock{proto.Text("q")}}, nil); err != nil {
					t.Fatal(err)
				}
				<-inRequest
				if err := c.Call(proto.MethodDaemonShutdown, proto.DaemonShutdownParams{}, nil); err != nil {
					t.Fatal(err)
				}
				for deadline := time.Now().Add(5 * time.Second); !daemon.ReadHolder(stateDir).Stopping; time.Sleep(10 * time.Millisecond) {
					if time.Now().After(deadline) {
						t.Fatal("the record never said stopping")
					}
				}
			}
			c.Close()
			cmd.Process.Kill()
			<-exited
			var out, errb bytes.Buffer
			if code := dispatch([]string{"daemon", "status", "-config", cfg}, &out, &errb); code != exitNotRunning || !strings.Contains(out.String(), "not running") {
				t.Fatalf("status after the daemon died: exit %d, %q %q; want %d, not running", code, out.String(), errb.String(), exitNotRunning)
			}
			out.Reset()
			began := time.Now()
			if code := dispatch([]string{"daemon", "stop", "-config", cfg}, &out, &errb); code != exitOK || !strings.Contains(out.String(), "not running") || time.Since(began) > 2*time.Second {
				t.Fatalf("stop after the daemon died: exit %d in %s, %q %q; want 0 at once, not running", code, time.Since(began), out.String(), errb.String())
			}
		})
	}
}

// TestStatusAndStopSeeALockWithoutARecord: a lock held with no holder record (a daemon starting, which records itself
// next, or one at the end of Release, which removes its record just before unlocking) is not "not running". status
// says the lock is held; stop waits for it to be let go.
func TestStatusAndStopSeeALockWithoutARecord(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "tack-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	stateDir := filepath.Join(dir, "s")
	cfg := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(cfg, []byte("state_dir = \""+stateDir+"\"\n[servers.local]\nkind = \"goinfer-remote\"\nurl = \"http://127.0.0.1:1\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := daemon.Acquire(stateDir, "1")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	if err := os.Remove(filepath.Join(stateDir, daemon.HolderName)); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := dispatch([]string{"daemon", "status", "-config", cfg}, &out, &errb); code != exitNotServing || !strings.Contains(out.String(), "holds the lock") {
		t.Fatalf("status with the lock held and no record: exit %d, %q %q; want %d, the lock held", code, out.String(), errb.String(), exitNotServing)
	}
	go func() { time.Sleep(300 * time.Millisecond); l.Release() }()
	out.Reset()
	began := time.Now()
	if code := dispatch([]string{"daemon", "stop", "-config", cfg}, &out, &errb); code != exitOK || time.Since(began) < 250*time.Millisecond {
		t.Fatalf("stop with the lock held and no record: exit %d after %s, %q %q; want 0, once the lock was let go", code, time.Since(began), out.String(), errb.String())
	}
}

// TestDaemonStartSpawnsOne: tack daemon start, with no daemon running, spawns one detached (this test binary standing
// in for tack, TestMain) and prints its status once it serves; stop then ends it.
func TestDaemonStartSpawnsOne(t *testing.T) {
	ts := httptest.NewServer(goinfertest.NewScripted("m", 16384))
	defer ts.Close()
	dir, err := os.MkdirTemp("/tmp", "tack-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	stateDir := filepath.Join(dir, "s")
	cfg := filepath.Join(dir, "config.toml")
	body := "state_dir = \"" + stateDir + "\"\n[daemon]\nshutdown_grace = \"1s\"\n[servers.local]\nkind = \"goinfer-remote\"\nurl = \"" + ts.URL + "\"\n"
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TACK_TEST_AS_TACK", "1") // the spawned daemon is this binary, and inherits it
	t.Cleanup(func() { dispatch([]string{"daemon", "stop", "-config", cfg}, &bytes.Buffer{}, &bytes.Buffer{}) })
	var out, errb bytes.Buffer
	if code := dispatch([]string{"daemon", "start", "-config", cfg}, &out, &errb); code != exitOK || !strings.Contains(out.String(), "tack daemon "+version) {
		t.Fatalf("start: exit %d, %q %q; want a daemon started and its status", code, out.String(), errb.String())
	}
	if h := daemon.ReadHolder(stateDir); h.PID == 0 || h.PID == os.Getpid() {
		t.Fatalf("the holder after start: %+v; want another process, the spawned daemon", h)
	}
	out.Reset()
	if code := dispatch([]string{"daemon", "stop", "-config", cfg}, &out, &errb); code != exitOK || !strings.Contains(out.String(), "stopped") {
		t.Fatalf("stop: exit %d, %q %q", code, out.String(), errb.String())
	}
}

// TestAnUnknownDaemonCommandIsUsage: a subcommand that is not run, start, stop or status is bad usage, said before
// the config is read, so a missing config does not turn it into another error.
func TestAnUnknownDaemonCommandIsUsage(t *testing.T) {
	var out, errb bytes.Buffer
	if code := dispatch([]string{"daemon", "frobnicate", "-config", filepath.Join(t.TempDir(), "absent.toml")}, &out, &errb); code != exitUsage {
		t.Fatalf("tack daemon frobnicate: exit %d, %q; want %d", code, errb.String(), exitUsage)
	}
}

// TestStopWhenTheDaemonGoesAndAnotherStarts: a stop whose daemon finishes shutting down as stop reaches it finds the
// connection closed before its status call returns, so it never learned that daemon's pid. If the next daemon has
// taken the lock and recorded itself by then, stop must not take the record's pid for the one it asked, and wait
// for the next daemon to let go: the one it asked has gone, and that is stopped.
func TestStopWhenTheDaemonGoesAndAnotherStarts(t *testing.T) {
	models := goinfertest.NewScripted("m", 16384)
	ts := httptest.NewServer(models)
	defer ts.Close()
	dir, err := os.MkdirTemp("/tmp", "tack-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	stateDir := filepath.Join(dir, "s")
	cfg := filepath.Join(dir, "config.toml")
	body := "state_dir = \"" + stateDir + "\"\n[servers.local]\nkind = \"goinfer-remote\"\nurl = \"" + ts.URL + "\"\n"
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := daemon.Acquire(stateDir, "1") // the daemon stop reaches: this process, its lock, its record, its socket
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	ln, err := l.Listen()
	if err != nil {
		t.Fatal(err)
	}
	next := make(chan int, 1)
	go func() {
		nc, err := ln.Accept()
		if err != nil {
			return
		}
		defer nc.Close()
		sc, enc := bufio.NewScanner(nc), json.NewEncoder(nc)
		for sc.Scan() {
			var req proto.Request
			if json.Unmarshal(sc.Bytes(), &req) != nil {
				return
			}
			if req.Method == proto.MethodInitialize {
				r, _ := json.Marshal(proto.InitializeResult{Protocol: proto.Version})
				enc.Encode(proto.Response{JSONRPC: "2.0", ID: req.ID, Result: r})
				continue
			}
			// Any other call: this daemon finishes shutting down, and the next one starts behind it and records
			// itself, all before this connection closes unanswered.
			ln.Close()
			cmd := exec.Command(os.Args[0], "daemon", "run", "-config", cfg) // this binary, standing in for tack (TestMain)
			cmd.Env = append(os.Environ(), "TACK_TEST_AS_TACK=1")
			if err := cmd.Start(); err != nil {
				t.Error(err)
				return
			}
			go cmd.Wait()
			t.Cleanup(func() { cmd.Process.Kill() })
			next <- cmd.Process.Pid
			l.Release()
			for deadline := time.Now().Add(10 * time.Second); daemon.ReadHolder(stateDir).PID != cmd.Process.Pid; time.Sleep(10 * time.Millisecond) {
				if time.Now().After(deadline) {
					t.Error("the next daemon never recorded itself")
					return
				}
			}
			return
		}
	}()
	var out, errb bytes.Buffer
	began := time.Now()
	code := daemonStop(stateDir, time.Second, &out, &errb)
	if code != exitOK || !strings.Contains(out.String(), "stopped") {
		t.Fatalf("stop as its daemon went and the next started: exit %d after %s, %q %q; want 0 and stopped", code, time.Since(began), out.String(), errb.String())
	}
	if pid := <-next; daemon.ReadHolder(stateDir).PID != pid {
		t.Fatalf("the next daemon (pid %d) no longer holds the lock; stop should have left it", pid)
	}
}
