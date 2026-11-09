package daemon

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// runOptions is a daemon over dir whose core keeps its sessions in dir and asks a scripted model, or the source given.
func runOptions(t *testing.T, dir string, source ...func(string) (models.Source, error)) RunOptions {
	t.Helper()
	ts := httptest.NewServer(goinfertest.NewScripted("m", 16384, goinfertest.Says("hi")))
	t.Cleanup(ts.Close)
	src := func(string) (models.Source, error) { return models.NewRemote("local", ts.URL, "", "", nil), nil }
	if len(source) > 0 {
		src = source[0]
	}
	return RunOptions{StateDir: dir, Version: "1.2.3", Grace: time.Second, NewCore: func(ctx context.Context, token string, shutdown func()) (*core.Core, error) {
		st, err := store.Open(dir)
		if err != nil {
			return nil, err
		}
		return core.New(ctx, core.Options{Store: st, Version: "1.2.3", ClientToken: token, Shutdown: shutdown,
			Budget: loop.Budget{MaxRequests: 4, MaxToolCalls: 4, MaxWall: time.Minute, MaxTokens: 64, TurnTokens: 1 << 20, MaxErrors: 8,
				SessionTokens: 1 << 20, SessionToolCalls: 100},
			Source: src}), nil
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

// tickingSource is a remote source that counts the idle ticks it is given, as a supervised child would apply them.
type tickingSource struct {
	*models.Remote
	ticks *atomic.Int32
}

func (s tickingSource) Tick(context.Context) error { s.ticks.Add(1); return nil }

// TestRunTicksTheCore: the daemon applies its model sources' idle rules on an interval, so a supervised child unloads
// and exits when idle (DESIGN §8.1).
func TestRunTicksTheCore(t *testing.T) {
	dir := shortDir(t)
	ts := httptest.NewServer(goinfertest.NewScripted("m", 16384))
	t.Cleanup(ts.Close)
	var ticks atomic.Int32
	o := runOptions(t, dir, func(string) (models.Source, error) {
		return tickingSource{models.NewRemote("local", ts.URL, "", "", nil), &ticks}, nil
	})
	o.TickEvery = 10 * time.Millisecond
	start(t, o)
	c := dialSoon(t, dir)
	ws := t.TempDir()
	if err := c.Call(proto.MethodSessionNew, proto.SessionNewParams{Workspace: ws, Model: "main", Mode: proto.ModePlan}, nil); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for ticks.Load() < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("%d ticks in 5s at a 10ms interval", ticks.Load())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// stuckModel serves /v1/models and answers no /v1/messages until the request is cancelled.
func stuckModel() http.Handler {
	models := goinfertest.NewScripted("m", 16384)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/messages" {
			io.Copy(io.Discard, r.Body) // the server sees the client go only once the body is read
			<-r.Context().Done()
			return
		}
		models.ServeHTTP(w, r)
	})
}

// TestAShutdownStopsAcceptingFirst: a shutdown takes the socket away at once, while its turns are still draining, so
// new clients start the next daemon instead of reaching one that refuses them; that daemon waits for the lock and
// takes it. The connection already open still sees its turn end, as shutdown (DESIGN §6.1).
func TestAShutdownStopsAcceptingFirst(t *testing.T) {
	dir := shortDir(t)
	ts := httptest.NewServer(stuckModel())
	t.Cleanup(ts.Close)
	o := runOptions(t, dir, func(string) (models.Source, error) { return models.NewRemote("local", ts.URL, "", "", nil), nil })
	o.Grace = 500 * time.Millisecond
	done := make(chan error, 1)
	go func() { done <- Run(context.Background(), o) }()
	c := dialSoon(t, dir)
	events := make(chan proto.Event, 64)
	c.OnEvent = func(p proto.SessionEventParams) { events <- p.Event }
	var sess proto.SessionNewResult
	if err := c.Call(proto.MethodSessionNew, proto.SessionNewParams{Workspace: t.TempDir(), Model: "main", Mode: proto.ModePlan}, &sess); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: sess.Session}, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: sess.Session, Content: []proto.ContentBlock{proto.Text("hang")}}, nil); err != nil {
		t.Fatal(err)
	}
	for ev := range events {
		if ev.Type == proto.EvRequestSent {
			break
		}
	}
	if err := c.Call(proto.MethodDaemonShutdown, proto.DaemonShutdownParams{}, nil); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(o.Grace / 2)
	for {
		if _, err := os.Stat(SocketPath(dir)); errors.Is(err, fs.ErrNotExist) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the socket was still there half way through the grace window; a shutdown stops accepting first")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// The stopping record says how long the drain may take, so the next daemon waits that long.
	if h := ReadHolder(dir); !h.Stopping || time.Until(h.Until) < o.Grace {
		t.Fatalf("the record while it drains: %+v; want it stopping, with an Until past the grace window", h)
	}
	next := make(chan error, 1)
	go func() { // a client found the socket gone and started the next daemon
		l, err := Acquire(dir, "next")
		if err == nil {
			l.Release()
		}
		next <- err
	}()
	var end proto.Event
	for ev := range events {
		if ev.Type == proto.EvTurnEnd {
			end = ev
			break
		}
	}
	if v, _ := end.Decode(); v != (proto.TurnEnd{StopReason: proto.StopShutdown}) {
		t.Fatalf("the open connection saw the turn end %+v, want shutdown", v)
	}
	if err := <-done; err != nil {
		t.Fatalf("Run = %v", err)
	}
	if err := <-next; err != nil {
		t.Fatalf("the daemon started during the shutdown: %v, want it to wait and take the lock", err)
	}
}

// TestRunEndsItsWatcherBeforeLettingGo: the core's context ends, and Run waits for the watcher beside the core to
// return, before it closes the core and releases the lock; nothing of the daemon's runs on after Run returns.
func TestRunEndsItsWatcherBeforeLettingGo(t *testing.T) {
	dir := shortDir(t)
	o := runOptions(t, dir)
	var ended, heldAtEnd atomic.Bool
	o.Watch = func(ctx context.Context, _ *core.Core) {
		<-ctx.Done()
		time.Sleep(50 * time.Millisecond) // a watcher that takes a moment to stop
		heldAtEnd.Store(ReadHolder(dir).PID == os.Getpid())
		ended.Store(true)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, o) }()
	dialSoon(t, dir).Close()
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !ended.Load() {
		t.Fatal("Run returned while the watcher beside the core still ran")
	}
	if !heldAtEnd.Load() {
		t.Fatal("the lock was released before the watcher beside the core ended")
	}
}

// TestTheDaemonsLogTakesWritesAtOnce: Run, its serve loop's refusals, its idle ticks and the core's callbacks all write
// the one log; SyncWriter serializes them, so the writer need not be safe itself (run under -race).
func TestTheDaemonsLogTakesWritesAtOnce(t *testing.T) {
	var buf bytes.Buffer
	w := SyncWriter(&buf)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 100 {
				fmt.Fprintln(w, "tack daemon: a line")
			}
		})
	}
	wg.Wait()
	if n := strings.Count(buf.String(), "\n"); n != 800 {
		t.Fatalf("%d lines; want 800, none torn", n)
	}
	if SyncWriter(w) != w {
		t.Fatal("SyncWriter wrapped a writer it had made already")
	}
}

// TestAShutdownRefusesNewConnectionsBeforeItStopsAccepting: a connection the daemon accepted just before its shutdown
// cannot be initialized in the moment between the listener closing and the drain: the core refuses it from before
// the listener closes, so the client goes on to the next daemon instead of starting work on this one.
func TestAShutdownRefusesNewConnectionsBeforeItStopsAccepting(t *testing.T) {
	dir := shortDir(t)
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, runOptions(t, dir)) }()
	dialSoon(t, dir).Close()
	nc, err := net.Dial("unix", SocketPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	late := core.NewClient(nc)
	if err := late.Call(proto.MethodDaemonStatus, proto.DaemonStatusParams{}, nil); err != nil {
		t.Fatal(err) // the daemon has accepted it and serves it
	}
	var initErr error
	stopHook = func() { // the listener has just closed; the drain has not begun
		initErr = late.Call(proto.MethodInitialize, proto.InitializeParams{Client: testClient, Protocol: proto.Version}, nil)
	}
	t.Cleanup(func() { stopHook = nil })
	stop()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var pe *proto.Error
	if !errors.As(initErr, &pe) || pe.Code != proto.CodeShuttingDown {
		t.Fatalf("an initialize as the daemon stopped accepting: %v; want code %d", initErr, proto.CodeShuttingDown)
	}
}

// slowTicker is a model source whose idle rules take a while to apply, and which records a Close that comes while
// they are being applied.
type slowTicker struct {
	models.Source
	ticking, closedMidTick *atomic.Bool
}

func (s slowTicker) Tick(context.Context) error {
	s.ticking.Store(true)
	time.Sleep(100 * time.Millisecond)
	s.ticking.Store(false)
	return nil
}

func (s slowTicker) Close() error {
	if s.ticking.Load() {
		s.closedMidTick.Store(true)
	}
	return s.Source.Close()
}

// TestRunEndsItsTicksBeforeClosingTheCore: a tick of the model sources' idle rules still running when the daemon shuts
// down ends before the core closes those sources (Run waits for its ticker as for its watcher).
func TestRunEndsItsTicksBeforeClosingTheCore(t *testing.T) {
	dir := shortDir(t)
	ts := httptest.NewServer(goinfertest.NewScripted("m", 16384, goinfertest.Says("hi")))
	t.Cleanup(ts.Close)
	var ticking, closedMidTick atomic.Bool
	o := runOptions(t, dir, func(string) (models.Source, error) {
		return slowTicker{models.NewRemote("local", ts.URL, "", "", nil), &ticking, &closedMidTick}, nil
	})
	o.TickEvery = 5 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, o) }()
	c := dialSoon(t, dir)
	if err := c.Call(proto.MethodSessionNew, proto.SessionNewParams{Workspace: t.TempDir(), Model: "main", Mode: proto.ModePlan}, &proto.SessionNewResult{}); err != nil {
		t.Fatal(err) // the core opens the source, which it ticks from now on
	}
	c.Close()
	for deadline := time.Now().Add(5 * time.Second); !ticking.Load(); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the daemon never ticked its model source")
		}
	}
	cancel() // mid-tick
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if closedMidTick.Load() {
		t.Fatal("the core closed its model source while a tick of its idle rules still ran")
	}
}
