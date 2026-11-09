package daemon

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

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

// TestDialRedialsADaemonThatRestarted: a client that connected to a daemon on its way out and then read its
// successor's token is refused the token there; Dial connects again, to the successor, rather than presenting the
// token again on the connection that refused it.
func TestDialRedialsADaemonThatRestarted(t *testing.T) {
	dir := shortDir(t)
	os.MkdirAll(dir, 0o700)
	if err := os.WriteFile(filepath.Join(dir, TokenName), []byte("successor's\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ok, _ := json.Marshal(proto.InitializeResult{Protocol: proto.Version, Privileged: true})
	conns := fakeDaemon(t, dir, func(n int, req proto.Request) proto.Response {
		if n == 1 { // the daemon that was going
			return proto.Response{Error: &proto.Error{Code: proto.CodeUnauthorized, Message: "initialize: the client token is wrong"}}
		}
		return proto.Response{Result: ok}
	})
	c, err := Dial(dir, testClient, nil, time.Second)
	if err != nil {
		t.Fatalf("Dial across a restart: %v; want it to connect again, to the daemon the token is for", err)
	}
	defer c.Close()
	if n := conns.Load(); n != 2 || !c.Init.Privileged {
		t.Fatalf("%d connections, privileged %v; want a second connection, privileged", n, c.Init.Privileged)
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
