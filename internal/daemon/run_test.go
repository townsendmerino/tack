package daemon

import (
	"context"
	"errors"
	"io/fs"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/core"
	"github.com/townsendmerino/tack/internal/loop"
	"github.com/townsendmerino/tack/internal/models"
	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
	"github.com/townsendmerino/tack/internal/store"
	"golang.org/x/sys/unix"
)

var testClient = proto.ClientInfo{Name: "test", Version: "0"}

// runOptions is a daemon over dir whose core keeps its sessions in dir and asks a scripted model.
func runOptions(t *testing.T, dir string) RunOptions {
	t.Helper()
	ts := httptest.NewServer(goinfertest.NewScripted("m", 16384, goinfertest.Says("hi")))
	t.Cleanup(ts.Close)
	return RunOptions{StateDir: dir, Version: "1.2.3", Grace: time.Second, NewCore: func(token string, shutdown func()) (*core.Core, error) {
		st, err := store.Open(dir)
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		return core.New(ctx, core.Options{Store: st, Version: "1.2.3", ClientToken: token, Shutdown: shutdown,
			Budget: loop.Budget{MaxRequests: 4, MaxToolCalls: 4, MaxWall: time.Minute, MaxTokens: 64, TurnTokens: 1 << 20, MaxErrors: 8,
				SessionTokens: 1 << 20, SessionToolCalls: 100},
			Source: func(string) (models.Source, error) { return models.NewRemote("local", ts.URL, "", "", nil), nil }}), nil
	}}
}

// start runs a daemon and returns what Run returned, once it has.
func start(t *testing.T, o RunOptions) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- Run(context.Background(), o) }()
	t.Cleanup(func() {
		if c, err := Dial(o.StateDir, testClient, nil, 0); err == nil {
			c.Call(proto.MethodDaemonShutdown, proto.DaemonShutdownParams{}, nil)
			c.Close()
		}
	})
	return done
}

// dialSoon dials until the daemon answers.
func dialSoon(t *testing.T, dir string) *Conn {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		c, err := Dial(dir, testClient, nil, 0)
		if err == nil {
			t.Cleanup(func() { c.Close() })
			return c
		}
		if time.Now().After(deadline) {
			t.Fatalf("the daemon did not answer: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestRunServesUntilShutdown: a daemon answers a client that reads its token with a privileged connection, and a
// daemon/shutdown from that client ends Run cleanly, leaving no socket and no holder, and the lock free.
func TestRunServesUntilShutdown(t *testing.T) {
	dir := shortDir(t)
	done := start(t, runOptions(t, dir))
	c := dialSoon(t, dir)
	if !c.Init.Privileged || c.Init.Daemon.Version != "1.2.3" || c.Warning != "" {
		t.Fatalf("initialize = %+v (warning %q); want a privileged connection to 1.2.3", c.Init, c.Warning)
	}
	var st proto.DaemonStatusResult
	if err := c.Call(proto.MethodDaemonStatus, proto.DaemonStatusParams{}, &st); err != nil || st.PID != os.Getpid() {
		t.Fatalf("status %+v, %v", st, err)
	}
	if err := c.Call(proto.MethodDaemonShutdown, proto.DaemonShutdownParams{}, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after daemon/shutdown")
	}
	for _, name := range []string{SocketName, HolderName} {
		if _, err := os.Stat(filepath.Join(dir, name)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s is still there after the daemon stopped: %v", name, err)
		}
	}
	l, err := Acquire(dir, "next")
	if err != nil {
		t.Fatalf("the lock after the daemon stopped: %v", err)
	}
	l.Release()
}

// TestRunRecoversAtStart: a session a crash left inside a turn is marked interrupted before the daemon serves.
func TestRunRecoversAtStart(t *testing.T) {
	dir := shortDir(t)
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	id := store.NewSessionID(time.Now())
	log, _ := st.Create(id)
	log.Append(proto.EvSessionCreated, 0, proto.SessionCreated{Workspace: dir, Model: "main", Mode: proto.ModePlan})
	log.Append(proto.EvUserMessage, 0, proto.UserMessage{Content: []proto.ContentBlock{proto.Text("cut off")}})
	log.Close()
	start(t, runOptions(t, dir))
	dialSoon(t, dir)
	evs, _ := st.Events(id, 0)
	if v, _ := evs[len(evs)-1].Decode(); v != (proto.TurnEnd{StopReason: proto.StopInterrupted}) {
		t.Fatalf("the crashed session ends %+v; want turn.end interrupted", v)
	}
}

// TestRunFindsTheLockHeld: a second daemon returns a *HeldError naming the first, and touches nothing of it.
func TestRunFindsTheLockHeld(t *testing.T) {
	dir := shortDir(t)
	start(t, runOptions(t, dir))
	first := dialSoon(t, dir)
	err := Run(context.Background(), runOptions(t, dir))
	var he *HeldError
	if !errors.As(err, &he) || he.Holder.Version != "1.2.3" {
		t.Fatalf("a second Run = %v, want a *HeldError naming 1.2.3", err)
	}
	if err := first.Call(proto.MethodDaemonStatus, proto.DaemonStatusParams{}, nil); err != nil {
		t.Fatalf("the first daemon after a second tried to start: %v", err)
	}
}

// TestRunStopsWhenItsContextEnds: the first signal ends Run's context; it drains and returns.
func TestRunStopsWhenItsContextEnds(t *testing.T) {
	dir := shortDir(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, runOptions(t, dir)) }()
	dialSoon(t, dir)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return when its context ended")
	}
}

// TestDialSpawnsADaemonOnce: with no daemon, Dial calls spawn once and connects to what it started.
func TestDialSpawnsADaemonOnce(t *testing.T) {
	dir := shortDir(t)
	o := runOptions(t, dir)
	var spawns atomic.Int32
	spawn := func() error {
		spawns.Add(1)
		start(t, o)
		return nil
	}
	c, err := Dial(dir, testClient, spawn, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if n := spawns.Load(); n != 1 || !c.Init.Privileged {
		t.Fatalf("spawned %d times, privileged %v; want once, and a privileged connection", n, c.Init.Privileged)
	}
	if c2, err := Dial(dir, testClient, spawn, 5*time.Second); err != nil || spawns.Load() != 1 {
		t.Fatalf("a second Dial: %v, %d spawns; want it to find the running daemon", err, spawns.Load())
	} else {
		c2.Close()
	}
}

// TestDialWithoutADaemon: with no daemon and nothing to start one, Dial says so.
func TestDialWithoutADaemon(t *testing.T) {
	if _, err := Dial(shortDir(t), testClient, nil, 0); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("Dial = %v, want ErrNotRunning", err)
	}
}

// TestDialWithoutTheTokenIsUnprivileged: a client that cannot read the token (here: the file is not private) still
// connects, unprivileged, and daemon/shutdown is refused to it.
func TestDialWithoutTheTokenIsUnprivileged(t *testing.T) {
	dir := shortDir(t)
	start(t, runOptions(t, dir))
	dialSoon(t, dir)
	if err := os.Chmod(filepath.Join(dir, TokenName), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Dial(dir, testClient, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var pe *proto.Error
	if err := c.Call(proto.MethodDaemonShutdown, proto.DaemonShutdownParams{}, nil); c.Init.Privileged || !errors.As(err, &pe) || pe.Code != proto.CodeUnauthorized {
		t.Fatalf("privileged %v, shutdown %v; want an unprivileged connection refused the shutdown", c.Init.Privileged, err)
	}
	os.Chmod(filepath.Join(dir, TokenName), 0o600)
}

// TestVersionWarning: a daemon at the client's version needs no word; an older one is named, with the fix.
func TestVersionWarning(t *testing.T) {
	init := func(v int) proto.InitializeResult {
		return proto.InitializeResult{Daemon: proto.DaemonInfo{Version: "0.9.0"}, Protocol: v}
	}
	if w := versionWarning(3, init(3)); w != "" {
		t.Errorf("same version: %q", w)
	}
	if w := versionWarning(3, init(2)); !strings.Contains(w, "0.9.0") || !strings.Contains(w, "working at 2") || !strings.Contains(w, "tack daemon stop") {
		t.Errorf("older daemon: %q", w)
	}
}

// TestSpawnDetached: a spawned daemon runs in a session of its own, so the client's terminal and process group do not
// reach it, with its output going to the log.
func TestSpawnDetached(t *testing.T) {
	dir := shortDir(t)
	logPath := filepath.Join(dir, "logs", "daemon.log")
	pid, err := SpawnDetached("/bin/sh", []string{"-c", "echo started; exec sleep 30"}, logPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { syscall.Kill(pid, syscall.SIGKILL) }) // the pid this test started
	sid, err := unix.Getsid(pid)
	if err != nil || sid != pid {
		t.Fatalf("the spawned process's session is %d (%v); want its own, %d", sid, err, pid)
	}
	if mine, _ := unix.Getsid(0); sid == mine {
		t.Fatal("the spawned process shares this process's session")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		b, _ := os.ReadFile(logPath)
		if strings.Contains(string(b), "started") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the log has %q, want the child's output", b)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if st, _ := os.Stat(logPath); st.Mode().Perm() != 0o600 {
		t.Errorf("the log is %v, want 0600", st.Mode().Perm())
	}
}
