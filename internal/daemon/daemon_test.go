package daemon

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/core"
	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/store"
)

// shortDir is a state directory short enough for a socket path: t.TempDir is under /var/folders on macOS, often past
// the 103 bytes a socket path may have (DESIGN §9.1).
func shortDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("/tmp", "tack-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return filepath.Join(d, "state")
}

func acquire(t *testing.T, dir string) *Lock {
	t.Helper()
	l, err := Acquire(dir, "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Release() })
	return l
}

// TestASecondDaemonFindsTheLockHeld: while one daemon holds the lock, a second's Acquire fails at once with a
// *HeldError naming the holder's version and pid; once the lock is released, it succeeds.
func TestASecondDaemonFindsTheLockHeld(t *testing.T) {
	dir := shortDir(t)
	l := acquire(t, dir)
	_, err := Acquire(dir, "9.9.9")
	var he *HeldError
	if !errors.As(err, &he) {
		t.Fatalf("second Acquire = %v, want a *HeldError", err)
	}
	if he.Holder.PID != os.Getpid() || he.Holder.Version != "1.2.3" || !strings.Contains(err.Error(), "1.2.3") {
		t.Fatalf("HeldError names %+v (%v); want pid %d, version 1.2.3", he.Holder, err, os.Getpid())
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	l2, err := Acquire(dir, "9.9.9")
	if err != nil {
		t.Fatalf("Acquire after Release: %v", err)
	}
	l2.Release()
}

// TestConcurrentStartsElectOneDaemon: CLIs that all find no daemon and start one at once end with exactly one holding
// the lock (DESIGN §6.1).
func TestConcurrentStartsElectOneDaemon(t *testing.T) {
	dir := shortDir(t)
	const n = 8
	var wg sync.WaitGroup
	var mu sync.Mutex
	var won []*Lock
	held := 0
	for range n {
		wg.Go(func() {
			l, err := Acquire(dir, "1")
			mu.Lock()
			defer mu.Unlock()
			var he *HeldError
			switch {
			case err == nil:
				won = append(won, l)
			case errors.As(err, &he):
				held++
			default:
				t.Error(err)
			}
		})
	}
	wg.Wait()
	for _, l := range won {
		defer l.Release()
	}
	if len(won) != 1 || held != n-1 {
		t.Fatalf("%d acquired and %d found it held; want 1 and %d", len(won), held, n-1)
	}
}

// TestListenReplacesAStaleSocket: a daemon that crashed leaves its socket file; the next daemon, holding the lock,
// removes it and binds, and connections reach the new one.
func TestListenReplacesAStaleSocket(t *testing.T) {
	dir := shortDir(t)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	stale, err := net.ListenUnix("unix", &net.UnixAddr{Name: SocketPath(dir), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	stale.SetUnlinkOnClose(false)
	stale.Close() // as a crash leaves it: the file, and nobody listening
	if _, err := os.Stat(SocketPath(dir)); err != nil {
		t.Fatalf("the stale socket is not there: %v", err)
	}
	ln, err := acquire(t, dir).Listen()
	if err != nil {
		t.Fatalf("Listen over a stale socket: %v", err)
	}
	go func() {
		if c, err := ln.Accept(); err == nil {
			c.Write([]byte("new\n"))
			c.Close()
		}
	}()
	c, err := net.Dial("unix", SocketPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if b, _ := io.ReadAll(c); string(b) != "new\n" {
		t.Fatalf("read %q from the socket, want the new daemon's %q", b, "new\n")
	}
}

// TestModes: the state directory is 0700 even when it existed wider, and the socket and the token are 0600.
func TestModes(t *testing.T) {
	dir := shortDir(t)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	l := acquire(t, dir)
	if _, err := l.Listen(); err != nil {
		t.Fatal(err)
	}
	if _, err := l.NewToken(); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]os.FileMode{dir: 0o700, SocketPath(dir): 0o600,
		filepath.Join(dir, TokenName): 0o600, filepath.Join(dir, LockName): 0o600, filepath.Join(dir, HolderName): 0o600} {
		st, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := st.Mode().Perm(); got != want {
			t.Errorf("%s is %v, want %v", filepath.Base(path), got, want)
		}
	}
}

// TestListenRefusesALongSocketPath: a state directory whose socket path is past MaxSocketPath fails with the length
// and the fix, not bind's bare EINVAL.
func TestListenRefusesALongSocketPath(t *testing.T) {
	dir := filepath.Join(shortDir(t), strings.Repeat("d", MaxSocketPath))
	_, err := acquire(t, dir).Listen()
	if err == nil || !strings.Contains(err.Error(), "bytes") || !strings.Contains(err.Error(), "state_dir") {
		t.Fatalf("Listen = %v, want the path's length and state_dir", err)
	}
}

// TestPeerUIDIsTheCaller: the peer credential of a connection this process made is this process's uid.
func TestPeerUIDIsTheCaller(t *testing.T) {
	ln, err := acquire(t, shortDir(t)).Listen()
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan int, 1)
	go func() {
		c, err := ln.ul.AcceptUnix()
		if err != nil {
			got <- -1
			return
		}
		defer c.Close()
		uid, err := peerUID(c)
		if err != nil {
			t.Error(err)
		}
		got <- uid
	}()
	c, err := net.Dial("unix", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if uid := <-got; uid != os.Getuid() {
		t.Fatalf("peer uid %d, want %d", uid, os.Getuid())
	}
}

// TestAcceptRefusesAnotherUser: a connection whose peer is not the daemon's user is closed unanswered, and Refused is
// told; the same Accept goes on to return the next connection, this user's.
func TestAcceptRefusesAnotherUser(t *testing.T) {
	ln, err := acquire(t, shortDir(t)).Listen()
	if err != nil {
		t.Fatal(err)
	}
	var n atomic.Int32
	real := ln.peer
	ln.peer = func(c *net.UnixConn) (int, error) { // the first connection is another user's; the rest are real
		if n.Add(1) == 1 {
			return os.Getuid() + 1, nil
		}
		return real(c)
	}
	refused := make(chan error, 1)
	ln.Refused = func(err error) { refused <- err }
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept() // one call: it must refuse the first and return the second
		if err != nil {
			t.Error(err)
			return
		}
		accepted <- c
	}()
	other, err := net.Dial("unix", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	select {
	case err := <-refused:
		if !strings.Contains(err.Error(), "uid") {
			t.Errorf("Refused told %v, want the uids", err)
		}
	case c := <-accepted:
		c.Close()
		t.Fatal("a connection from another uid was accepted")
	case <-time.After(5 * time.Second):
		t.Fatal("the connection was neither refused nor accepted")
	}
	other.SetReadDeadline(time.Now().Add(5 * time.Second))
	if n, err := other.Read(make([]byte, 1)); n != 0 || err != io.EOF {
		t.Fatalf("read on the refused connection: %d bytes, %v; want EOF", n, err)
	}
	mine, err := net.Dial("unix", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer mine.Close()
	select {
	case c := <-accepted:
		c.Close()
	case <-time.After(5 * time.Second):
		t.Fatal("after refusing one connection, the listener did not accept the next")
	}
}

// TestTokenIsFreshAndPrivate: every start makes a new token, which ReadToken returns; a token file others can read is
// refused.
func TestTokenIsFreshAndPrivate(t *testing.T) {
	dir := shortDir(t)
	l := acquire(t, dir)
	a, err := l.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	b, err := l.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if a == b || len(b) != 64 {
		t.Fatalf("tokens %q then %q; want two different 64-hex-digit tokens", a, b)
	}
	if got, err := ReadToken(dir); err != nil || got != b {
		t.Fatalf("ReadToken = %q, %v; want the latest, %q", got, err, b)
	}
	if err := os.Chmod(filepath.Join(dir, TokenName), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadToken(dir); err == nil {
		t.Fatal("ReadToken read a token file other users can read")
	}
}

// TestServe: a client on the socket that presents the token from client.token gets a privileged connection; when the
// context ends, Serve returns and the connection is closed.
func TestServe(t *testing.T) {
	dir := shortDir(t)
	l := acquire(t, dir)
	ln, err := l.Listen()
	if err != nil {
		t.Fatal(err)
	}
	tok, err := l.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(dir, "store"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := core.New(ctx, core.Options{Store: st, Version: "1.2.3", ClientToken: tok})
	served := make(chan error, 1)
	go func() { served <- Serve(ctx, ln, c) }()

	conn, err := net.Dial("unix", SocketPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	read, err := ReadToken(dir)
	if err != nil {
		t.Fatal(err)
	}
	var r proto.InitializeResult
	if err := core.NewClient(conn).Call(proto.MethodInitialize, proto.InitializeParams{Client: proto.ClientInfo{Name: "t", Version: "0"},
		Protocol: proto.Version, Token: read}, &r); err != nil {
		t.Fatal(err)
	}
	if !r.Privileged || r.Daemon.Version != "1.2.3" {
		t.Fatalf("initialize = %+v; want a privileged connection to daemon 1.2.3", r)
	}
	cancel()
	select {
	case err := <-served:
		if err != nil {
			t.Fatalf("Serve = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return when its context ended")
	}
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("read after Serve returned: %v, want EOF", err)
	}
}

// TestADaemonStartedDuringAShutdownWaits: when Serve's context ends, the socket goes before the lock is released,
// and a client that finds it gone starts the next daemon. That start must wait for the stopping daemon to release the
// lock and take it, not find its record and give up: closing the listener marks the record stopping first.
func TestADaemonStartedDuringAShutdownWaits(t *testing.T) {
	dir := shortDir(t)
	watchBusy(t)
	l := acquire(t, dir)
	ln, err := l.Listen()
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(dir, "store"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	c := core.New(context.Background(), core.Options{Store: st, Version: "1.2.3"})
	served := make(chan error, 1)
	go func() { served <- Serve(ctx, ln, c) }()
	cancel() // the daemon is shutting down
	<-served
	if _, err := os.Stat(SocketPath(dir)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the socket is still there after Serve returned: %v", err)
	}
	next := make(chan error, 1)
	go func() { // a client found the socket gone and started a daemon
		n, err := Acquire(dir, "next")
		if err == nil {
			defer n.Release()
		}
		next <- err
	}()
	waitBusy(t)                         // the start has found the lock held, and is waiting
	time.Sleep(1500 * time.Millisecond) // the stopping daemon drains: longer than a start waits on a holder it cannot name
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-next:
		if err != nil {
			t.Fatalf("a daemon started during the shutdown: %v, want it to wait and take the lock", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the next daemon never took the lock")
	}
}
