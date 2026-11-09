package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/models"
	"github.com/townsendmerino/tack/internal/proto"
)

// fakeDaemon answers JSON-RPC on dir's socket: answer(n, req) is the n'th connection's (from 1) response to req. It
// returns the count of connections taken.
func fakeDaemon(t *testing.T, dir string, answer func(n int, req proto.Request) proto.Response) *atomic.Int32 {
	t.Helper()
	ln, err := net.Listen("unix", SocketPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	var conns atomic.Int32
	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			n := int(conns.Add(1))
			go func() {
				defer nc.Close()
				sc, enc := bufio.NewScanner(nc), json.NewEncoder(nc)
				for sc.Scan() {
					var req proto.Request
					if json.Unmarshal(sc.Bytes(), &req) != nil || len(req.ID) == 0 {
						continue
					}
					resp := answer(n, req)
					resp.JSONRPC, resp.ID = "2.0", req.ID
					enc.Encode(resp)
				}
			}()
		}
	}()
	return &conns
}

// TestDialStartsOverWhenTheDaemonGoes: a client that connected to a daemon just as it shut down finds the connection
// closed when it initializes: a daemon closes its connections before it lets go of the lock a successor needs. Dial
// then starts over, once, as it began: it reaches a successor another client started, or spawns one itself.
func TestDialStartsOverWhenTheDaemonGoes(t *testing.T) {
	for _, mine := range []bool{false, true} {
		name := map[bool]string{false: "a successor another client started", true: "a successor this client spawns"}[mine]
		t.Run(name, func(t *testing.T) {
			dir := shortDir(t)
			ctx, stop := context.WithCancel(context.Background())
			t.Cleanup(stop)
			first := make(chan error, 1)
			go func() { first <- Run(ctx, runOptions(t, dir)) }()
			dialSoon(t, dir).Close()
			startNext := func() {
				start(t, runOptions(t, dir))
				for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
					if nc, err := net.Dial("unix", SocketPath(dir)); err == nil {
						nc.Close()
						return
					}
				}
				t.Error("the successor never served")
			}
			fired := false
			dialHook = func() {
				if fired {
					return
				}
				fired = true
				stop()
				<-first // the daemon this client reached is gone: its connections closed, its lock let go
				if !mine {
					startNext()
				}
			}
			t.Cleanup(func() { dialHook = nil })
			var spawn func() error
			spawns := 0
			if mine {
				spawn = func() error { spawns++; startNext(); return nil }
			}
			c, err := Dial(dir, testClient, spawn, 5*time.Second)
			if err != nil {
				t.Fatalf("Dial as its daemon went: %v; want it to reach the next one", err)
			}
			defer c.Close()
			if err := c.Call(proto.MethodDaemonStatus, proto.DaemonStatusParams{}, nil); err != nil {
				t.Fatalf("status on the connection Dial returned: %v", err)
			}
			if mine && spawns != 1 {
				t.Fatalf("%d spawns; want this client to start the next daemon once", spawns)
			}
		})
	}
}

// TestDialTellsAnUnreachableSocketFromNoDaemon: a socket this client may not reach is an error of its own, not "no
// daemon is running", which would have tack daemon stop report success and status report none.
func TestDialTellsAnUnreachableSocketFromNoDaemon(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reaches a socket through any directory")
	}
	dir := filepath.Join(shortDir(t), "s")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	fakeDaemon(t, dir, func(int, proto.Request) proto.Response { return proto.Response{} })
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	_, err := Dial(dir, testClient, nil, 0)
	if err == nil || errors.Is(err, ErrNotRunning) {
		t.Fatalf("Dial to a socket behind a directory this user may not search: %v; want an error that is not ErrNotRunning", err)
	}
}

// TestDialPassesOverADrainingDaemon: a connection a daemon accepted just before its shutdown stays open through the
// drain, but that daemon takes no new turn. Its initialize is refused (CodeShuttingDown), and Dial starts over: it
// reaches the next daemon, spawned here, once the draining one has let go.
func TestDialPassesOverADrainingDaemon(t *testing.T) {
	dir := shortDir(t)
	ts := httptest.NewServer(stuckModel())
	t.Cleanup(ts.Close)
	o := runOptions(t, dir, func(string) (models.Source, error) { return models.NewRemote("local", ts.URL, "", "", nil), nil })
	o.Grace = 300 * time.Millisecond
	ctx, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	first := make(chan error, 1)
	go func() { first <- Run(ctx, o) }()
	c := dialSoon(t, dir)
	var sess proto.SessionNewResult
	if err := c.Call(proto.MethodSessionNew, proto.SessionNewParams{Workspace: t.TempDir(), Model: "main", Mode: proto.ModePlan}, &sess); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: sess.Session, Content: []proto.ContentBlock{proto.Text("q")}}, nil); err != nil {
		t.Fatal(err) // a turn at the model, so the drain lasts its grace
	}
	fired := false
	dialHook = func() {
		if fired {
			return
		}
		fired = true
		time.Sleep(50 * time.Millisecond) // the daemon has accepted this connection
		stop()
		for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
			if _, err := os.Stat(SocketPath(dir)); errors.Is(err, fs.ErrNotExist) {
				return // draining, this connection still open
			}
		}
		t.Error("the daemon never stopped accepting")
	}
	t.Cleanup(func() { dialHook = nil })
	spawns := 0
	spawn := func() error {
		spawns++
		start(t, runOptions(t, dir))
		return nil
	}
	d, err := Dial(dir, testClient, spawn, 5*time.Second)
	if err != nil {
		t.Fatalf("Dial as its daemon drained: %v; want it to reach the next one", err)
	}
	defer d.Close()
	var st proto.DaemonStatusResult
	if err := d.Call(proto.MethodDaemonStatus, proto.DaemonStatusParams{}, &st); err != nil || st.Draining {
		t.Fatalf("the daemon Dial reached: %+v, %v; want one that is not draining", st, err)
	}
	if spawns != 1 {
		t.Fatalf("%d spawns; want the next daemon started once", spawns)
	}
	<-first
}
