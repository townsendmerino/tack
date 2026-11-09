package core

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"testing"

	"github.com/townsendmerino/tack/internal/proto"
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
	cn := newConn(io.Discard)
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
