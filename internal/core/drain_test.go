package core

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
	"github.com/townsendmerino/tack/internal/store"
)

// stuckModel serves /v1/models and never answers /v1/messages until the request is cancelled: a turn on it stays
// running until something cancels it.
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

// TestDrainCancelsATurnPastItsGrace: once Drain begins, new turns are refused with CodeShuttingDown and status says
// draining; a turn still running when the grace window ends is cancelled, ends with stop reason shutdown, and is
// reported.
func TestDrainCancelsATurnPastItsGrace(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(stuckModel())
	t.Cleanup(ts.Close)
	c, cl, events := daemonOn(t, st, ts.URL)
	ws := repo(t)
	var a, b proto.SessionNewResult
	for _, r := range []*proto.SessionNewResult{&a, &b} {
		if err := cl.Call(proto.MethodSessionNew, proto.SessionNewParams{Workspace: ws, Model: "main", Mode: proto.ModePlan}, r); err != nil {
			t.Fatal(err)
		}
	}
	if err := cl.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: a.Session}, nil); err != nil {
		t.Fatal(err)
	}
	if err := cl.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: a.Session, Content: []proto.ContentBlock{proto.Text("hang")}}, nil); err != nil {
		t.Fatal(err)
	}
	for ev := range events { // the request is in flight, on a model that will not answer
		if ev.Type == proto.EvRequestSent {
			break
		}
	}
	var stat proto.DaemonStatusResult
	if err := cl.Call(proto.MethodDaemonStatus, proto.DaemonStatusParams{}, &stat); err != nil || stat.Sessions != 2 || stat.Running != 1 || stat.Draining {
		t.Fatalf("status before the drain: %+v, %v; want 2 sessions, 1 running, not draining", stat, err)
	}
	drained := make(chan []proto.SessionID, 1)
	go func() { drained <- c.Drain(100 * time.Millisecond) }()
	deadline := time.Now().Add(5 * time.Second)
	for !stat.Draining && time.Now().Before(deadline) {
		cl.Call(proto.MethodDaemonStatus, proto.DaemonStatusParams{}, &stat)
	}
	var pe *proto.Error
	err = cl.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: b.Session, Content: []proto.ContentBlock{proto.Text("new")}}, nil)
	if !errors.As(err, &pe) || pe.Code != proto.CodeShuttingDown {
		t.Fatalf("a prompt while draining: %v, want code %d", err, proto.CodeShuttingDown)
	}
	var cancelled []proto.SessionID
	select {
	case cancelled = <-drained:
	case <-time.After(10 * time.Second):
		t.Fatal("Drain did not return")
	}
	if !slices.Equal(cancelled, []proto.SessionID{a.Session}) {
		t.Errorf("Drain cancelled %v, want [%s]", cancelled, a.Session)
	}
	evs := untilTurnEnd(t, events)
	if v, _ := evs[len(evs)-1].Decode(); v != (proto.TurnEnd{StopReason: proto.StopShutdown}) {
		t.Errorf("the turn ended %+v, want stop reason shutdown", v)
	}
	if cl.Call(proto.MethodDaemonStatus, proto.DaemonStatusParams{}, &stat); stat.Running != 0 {
		t.Errorf("after the drain %d turns are running", stat.Running)
	}
}

// countConn is the daemon's end of a connection, counting the responses written on it. Serve writes each response
// in one Write, and a net.Pipe write returns once the client has read it, so the count is of responses the client
// has.
type countConn struct {
	net.Conn
	written *atomic.Int32
}

func (c countConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if err == nil {
		c.written.Add(1)
	}
	return n, err
}

// TestShutdownNeedsThePrivilege: daemon/shutdown is refused on a connection without the client token; on one with it,
// it is answered first, and then Options.Shutdown is called, once.
func TestShutdownNeedsThePrivilege(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var calls, written atomic.Int32 // written: responses on the privileged connection
	atCall := make(chan int32, 4)   // written, when Shutdown was called
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := New(ctx, Options{Store: st, Version: "test", ClientToken: "tok", Shutdown: func() {
		calls.Add(1)
		atCall <- written.Load()
	}})
	connect := func(token string, wrap func(net.Conn) net.Conn) *Client {
		x, y := net.Pipe()
		t.Cleanup(func() { x.Close() })
		go c.Serve(ctx, wrap(y))
		cl := NewClient(x)
		if err := cl.Call(proto.MethodInitialize, proto.InitializeParams{Client: proto.ClientInfo{Name: "t", Version: "0"}, Protocol: proto.Version, Token: token}, nil); err != nil {
			t.Fatal(err)
		}
		return cl
	}
	plain := func(y net.Conn) net.Conn { return y }
	var pe *proto.Error
	if err := connect("", plain).Call(proto.MethodDaemonShutdown, proto.DaemonShutdownParams{}, nil); !errors.As(err, &pe) || pe.Code != proto.CodeUnauthorized {
		t.Fatalf("shutdown without the token: %v, want code %d", err, proto.CodeUnauthorized)
	}
	if calls.Load() != 0 {
		t.Fatal("an unprivileged shutdown called Shutdown")
	}
	priv := connect("tok", func(y net.Conn) net.Conn { return countConn{y, &written} })
	for range 2 {
		if err := priv.Call(proto.MethodDaemonShutdown, proto.DaemonShutdownParams{}, nil); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case n := <-atCall:
		if n < 2 { // initialize's response, then the shutdown's
			t.Errorf("Shutdown was called with %d responses written on its connection; want 2, the shutdown's own answered first", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a privileged shutdown did not call Shutdown")
	}
	time.Sleep(50 * time.Millisecond)
	if n := calls.Load(); n != 1 {
		t.Errorf("Shutdown called %d times, want once", n)
	}
}

// TestShutdownWithoutADaemonIsRefused: a Core that no daemon hosts (tack run without --daemon) has no Shutdown, and
// refuses a privileged daemon/shutdown rather than answering that it will shut down.
func TestShutdownWithoutADaemonIsRefused(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, cl, _ := daemonOn(t, st, "http://127.0.0.1:1", func(o *Options) { o.ClientToken = "tok" })
	if err := cl.Call(proto.MethodInitialize, proto.InitializeParams{Client: proto.ClientInfo{Name: "t", Version: "0"}, Protocol: proto.Version, Token: "tok"}, nil); err != nil {
		t.Fatal(err)
	}
	var pe *proto.Error
	if err := cl.Call(proto.MethodDaemonShutdown, proto.DaemonShutdownParams{}, nil); !errors.As(err, &pe) || pe.Code != proto.CodeInvalidRequest {
		t.Fatalf("shutdown with no Shutdown: %v, want code %d", err, proto.CodeInvalidRequest)
	}
}

// TestInitializeNegotiatesTheVersion: a client one version newer is answered with the daemon's version, which it then
// speaks (DESIGN §6.1: it warns and works at the daemon's version); two newer is refused, and the refusal names only
// versions a client can use.
func TestInitializeNegotiatesTheVersion(t *testing.T) {
	f := newFixture(t)
	var r proto.InitializeResult
	if err := f.client.Call(proto.MethodInitialize, proto.InitializeParams{Protocol: proto.Version + 1}, &r); err != nil || r.Protocol != proto.Version {
		t.Fatalf("a client at %d: %+v, %v; want protocol %d", proto.Version+1, r, err, proto.Version)
	}
	var pe *proto.Error
	if err := f.client.Call(proto.MethodInitialize, proto.InitializeParams{Protocol: proto.Version + 2}, nil); !errors.As(err, &pe) || pe.Code != proto.CodeInvalidParams {
		t.Fatalf("a client at %d: %v, want refused", proto.Version+2, err)
	}
	_, named, ok := strings.Cut(pe.Message, "speaks ")
	if !ok {
		t.Fatalf("the refusal %q names no version the daemon speaks", pe.Message)
	}
	for _, w := range strings.Split(named, " and ") {
		v, err := strconv.Atoi(w)
		if err != nil {
			t.Fatalf("the refusal %q: %q is not a version", pe.Message, w)
		}
		if got, ok := proto.Negotiate(v, proto.Version); !ok || got != v {
			t.Errorf("the refusal %q names protocol %d, which this daemon does not speak", pe.Message, v)
		}
	}
}

// TestADrainingCoreRefusesNewConnections: once Drain begins, an initialize is refused with CodeShuttingDown, so a
// client that reached a daemon in its last moments goes on to the next one; connections initialized before keep
// working through the drain.
func TestADrainingCoreRefusesNewConnections(t *testing.T) {
	f := newFixtureOn(t, stuckModel())
	id := f.session(t)
	if err := f.client.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: id, Content: []proto.ContentBlock{proto.Text("q")}}, nil); err != nil {
		t.Fatal(err)
	}
	drained := make(chan struct{})
	go func() { f.core.Drain(500 * time.Millisecond); close(drained) }()
	for deadline := time.Now().Add(2 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		var st proto.DaemonStatusResult
		if err := f.client.Call(proto.MethodDaemonStatus, proto.DaemonStatusParams{}, &st); err == nil && st.Draining {
			break // the connection from before still answers
		}
		if time.Now().After(deadline) {
			t.Fatal("the core never said it was draining")
		}
	}
	a, b := net.Pipe()
	t.Cleanup(func() { a.Close() })
	go f.core.Serve(context.Background(), b)
	var pe *proto.Error
	err := NewClient(a).Call(proto.MethodInitialize, proto.InitializeParams{Client: proto.ClientInfo{Name: "late", Version: "0"}, Protocol: proto.Version}, nil)
	if !errors.As(err, &pe) || pe.Code != proto.CodeShuttingDown {
		t.Fatalf("an initialize while draining: %v; want code %d", err, proto.CodeShuttingDown)
	}
	<-drained
}
