package core

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/townsendmerino/tack/internal/perms"
	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/sandbox"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
	"github.com/townsendmerino/tack/internal/store"
)

// TestInitializeChecksTheClientToken: initialize is where the client token is checked, once per connection (DESIGN
// §9.2). The right token makes the connection privileged, no token leaves it unprivileged, and a wrong one is refused
// and leaves the connection unprivileged, even one the right token had made privileged. A Core with no token refuses
// any token presented.
func TestInitializeChecksTheClientToken(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	connect := func(c *Core) *Client {
		a, b := net.Pipe()
		t.Cleanup(func() { a.Close() })
		go c.Serve(ctx, b)
		return NewClient(a)
	}
	initialize := func(cl *Client, token string) (proto.InitializeResult, error) {
		var r proto.InitializeResult
		err := cl.Call(proto.MethodInitialize, proto.InitializeParams{Client: proto.ClientInfo{Name: "t", Version: "0"},
			Protocol: proto.Version, Token: token}, &r)
		return r, err
	}
	withToken := New(ctx, Options{Store: st, Version: "test", ClientToken: "0123456789abcdef"})

	if r, err := initialize(connect(withToken), "0123456789abcdef"); err != nil || !r.Privileged {
		t.Errorf("the right token: privileged %v, err %v; want privileged", r.Privileged, err)
	}
	if r, err := initialize(connect(withToken), ""); err != nil || r.Privileged {
		t.Errorf("no token: privileged %v, err %v; want an unprivileged connection", r.Privileged, err)
	}
	var pe *proto.Error
	if _, err := initialize(connect(withToken), "0123456789abcdeX"); !errors.As(err, &pe) || pe.Code != proto.CodeUnauthorized {
		t.Errorf("a wrong token: %v, want code %d", err, proto.CodeUnauthorized)
	}
	// What a connection is privileged for is its own state, read here right after each initialize on one connection.
	cn := newConn(io.Discard, nil)
	initOn := func(token string) *proto.Error {
		p, _ := json.Marshal(proto.InitializeParams{Client: proto.ClientInfo{Name: "t", Version: "0"}, Protocol: proto.Version, Token: token})
		_, perr := withToken.dispatch(cn, proto.Request{JSONRPC: "2.0", ID: json.RawMessage("1"), Method: proto.MethodInitialize, Params: p})
		return perr
	}
	if perr := initOn("0123456789abcdeX"); perr == nil || cn.privileged {
		t.Errorf("a wrong token on a new connection: %v, privileged %v; want refused and unprivileged", perr, cn.privileged)
	}
	if perr := initOn("0123456789abcdef"); perr != nil || !cn.privileged {
		t.Fatalf("the right token: %v, privileged %v", perr, cn.privileged)
	}
	if perr := initOn("0123456789abcdeX"); perr == nil || perr.Code != proto.CodeUnauthorized || cn.privileged {
		t.Errorf("a wrong token on a privileged connection: %v, privileged %v; want refused, and the privilege gone", perr, cn.privileged)
	}
	// A re-initialize that fails for any other reason drops the privilege too: here, a protocol two versions on.
	if perr := initOn("0123456789abcdef"); perr != nil || !cn.privileged {
		t.Fatalf("the right token again: %v, privileged %v", perr, cn.privileged)
	}
	p, _ := json.Marshal(proto.InitializeParams{Protocol: proto.Version + 2, Token: "0123456789abcdef"})
	if _, perr := withToken.dispatch(cn, proto.Request{JSONRPC: "2.0", ID: json.RawMessage("1"), Method: proto.MethodInitialize, Params: p}); perr == nil || cn.privileged {
		t.Errorf("a re-initialize refused for its version: %v, privileged %v; want the privilege gone", perr, cn.privileged)
	}
	noToken := New(ctx, Options{Store: st, Version: "test"})
	if _, err := initialize(connect(noToken), "0123456789abcdef"); !errors.As(err, &pe) || pe.Code != proto.CodeUnauthorized {
		t.Errorf("a token to a Core without one: %v, want code %d", err, proto.CodeUnauthorized)
	}
}

// TestEveryMethodButInitializeNeedsTheToken: a connection that did not present the client token, as a command the model ran and
// that reached the socket would be, is refused by every method in the protocol except initialize, and a session it names is not
// made. The methods come from the protocol's own table, so a method added later is held to it too.
func TestEveryMethodButInitializeNeedsTheToken(t *testing.T) {
	f := newFixture(t)
	un := f.unprivileged(t)
	n := 0
	for method, spec := range proto.Methods {
		if method == proto.MethodInitialize || spec.Result == nil { // initialize, and notifications, which a client does not call
			continue
		}
		n++
		err := un.Call(method, map[string]any{}, nil)
		var pe *proto.Error
		if !errors.As(err, &pe) || pe.Code != proto.CodeUnauthorized {
			t.Errorf("%s without the token: %v; want unauthorized", method, err)
		}
	}
	if n < 10 {
		t.Errorf("only %d methods checked; the protocol's table is not being read", n)
	}
	err := un.Call(proto.MethodSessionNew, proto.SessionNewParams{Workspace: f.workspace(t), Model: "main", Mode: proto.ModePlan}, nil)
	var pe *proto.Error
	if !errors.As(err, &pe) || pe.Code != proto.CodeUnauthorized {
		t.Errorf("session/new without the token: %v", err)
	}
	if ids, _ := f.core.opt.Store.Sessions(); len(ids) != 0 {
		t.Errorf("a session was made without the token: %v", ids)
	}
	// A wrong token is refused and leaves the connection unprivileged too.
	a, b := net.Pipe()
	t.Cleanup(func() { a.Close() })
	go f.core.Serve(f.core.ctx, b)
	wrong := NewClient(a)
	if err := wrong.Call(proto.MethodInitialize, proto.InitializeParams{Client: proto.ClientInfo{Name: "x", Version: "0"}, Protocol: proto.Version, Token: "not-it"}, nil); errCode(err) != proto.CodeUnauthorized {
		t.Errorf("a wrong token: %v", err)
	}
	if err := wrong.Call(proto.MethodDaemonStatus, proto.DaemonStatusParams{}, nil); errCode(err) != proto.CodeUnauthorized {
		t.Errorf("after a wrong token: %v", err)
	}
}

// TestAutoNeedsTheOwnersConfirmation: a session is not made in auto unless session/new says the owner chose it; the other modes
// need no such word, and neither does anything else about a session in auto once it exists.
func TestAutoNeedsTheOwnersConfirmation(t *testing.T) {
	f := newFixture(t)
	f.core.opt.Edit = func(root string, _ sandbox.Target) (EditKit, error) {
		return EditKit{Shell: noShell{}, Engine: &perms.Engine{Root: root, Sandboxed: true}}, nil
	}
	call := func(mode proto.Mode, confirmed bool) error {
		return f.client.Call(proto.MethodSessionNew, proto.SessionNewParams{Workspace: gitWorkspace(t, ""), Model: "main", Mode: mode, Confirmed: confirmed}, nil)
	}
	if err := call(proto.ModeAuto, false); errCode(err) != proto.CodeInvalidParams || !strings.Contains(err.Error(), "confirmation") {
		t.Errorf("auto, unconfirmed: %v", err)
	}
	if ids, _ := f.core.opt.Store.Sessions(); len(ids) != 0 {
		t.Errorf("an unconfirmed auto session left a log: %v", ids)
	}
	if err := call(proto.ModeAuto, true); err != nil {
		t.Errorf("auto, confirmed: %v", err)
	}
	for _, m := range []proto.Mode{proto.ModePlan, proto.ModeAsk, proto.ModeEdits} {
		if err := call(m, false); err != nil {
			t.Errorf("%s without confirmation: %v", m, err)
		}
	}
}

func errCode(err error) int {
	var pe *proto.Error
	if errors.As(err, &pe) {
		return pe.Code
	}
	return 0
}

// TestOnlyAnAttendedPrivilegedClientIsAWatcher: a client that presents the token but does not say a person is at it (tack run, in
// process or through the daemon) is not someone a question can wait for: it is denied at once as no_user, as it was when such a client
// was unprivileged. Only a client that is both privileged and attended holds a question open. A connection with no token cannot claim
// to be attended.
func TestOnlyAnAttendedPrivilegedClientIsAWatcher(t *testing.T) {
	f := newFixture(t, goinfertest.Calls("", "w1", "write", `{"path":"AGENTS.md","content":"x\n"}`), goinfertest.Says("done"))
	f.core.opt.Edit = func(root string, _ sandbox.Target) (EditKit, error) {
		return EditKit{Shell: noShell{}, Engine: &perms.Engine{Root: root, Sandboxed: true}}, nil
	}
	sid, err := f.sessionIn(t, gitWorkspace(t, ""), proto.ModeAuto)
	if err != nil {
		t.Fatal(err)
	}
	// f.client has the token and no attendant: subscribed, it is not a watcher, so the question ends at once.
	f.client.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: sid}, nil)
	f.prompt(t, sid, "add notes")
	for _, ev := range f.waitTurnEnd(t) {
		if v, _ := ev.Decode(); ev.Type == proto.EvPermissionResolved && v.(proto.PermissionResolved).By != proto.DecidedByNoUser {
			t.Fatalf("resolved %+v; want no_user", v)
		}
	}
	// A connection without the token claiming to be attended is neither.
	a, b := net.Pipe()
	t.Cleanup(func() { a.Close() })
	go f.core.Serve(f.core.ctx, b)
	cl := NewClient(a)
	if err := cl.Call(proto.MethodInitialize, proto.InitializeParams{Client: proto.ClientInfo{Name: "x", Version: "0", Attended: true}, Protocol: proto.Version}, nil); err != nil {
		t.Fatal(err)
	}
	cn := &conn{}
	p, _ := json.Marshal(proto.InitializeParams{Client: proto.ClientInfo{Name: "x", Version: "0", Attended: true}, Protocol: proto.Version})
	f.core.dispatch(cn, proto.Request{JSONRPC: "2.0", ID: json.RawMessage("1"), Method: proto.MethodInitialize, Params: p})
	if cn.privileged || cn.attended {
		t.Errorf("a connection with no token: privileged %v, attended %v", cn.privileged, cn.attended)
	}
}
