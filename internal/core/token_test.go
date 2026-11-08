package core

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/store"
)

// TestInitializeChecksTheClientToken: initialize is where the client token is checked, once per connection (DESIGN
// §9.2). The right token makes the connection privileged, no token leaves it unprivileged, and a wrong one is refused
// and leaves the connection unprivileged. A Core with no token refuses any token presented.
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
	cl := connect(withToken)
	var pe *proto.Error
	if _, err := initialize(cl, "0123456789abcdeX"); !errors.As(err, &pe) || pe.Code != proto.CodeUnauthorized {
		t.Errorf("a wrong token: %v, want code %d", err, proto.CodeUnauthorized)
	}
	if r, err := initialize(cl, ""); err != nil || r.Privileged {
		t.Errorf("after a wrong token, initialize without one: privileged %v, err %v; want unprivileged", r.Privileged, err)
	}
	noToken := New(ctx, Options{Store: st, Version: "test"})
	if _, err := initialize(connect(noToken), "0123456789abcdef"); !errors.As(err, &pe) || pe.Code != proto.CodeUnauthorized {
		t.Errorf("a token to a Core without one: %v, want code %d", err, proto.CodeUnauthorized)
	}
}
