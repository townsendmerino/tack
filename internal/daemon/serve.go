package daemon

import (
	"context"
	"errors"
	"net"
	"sync"

	"github.com/townsendmerino/tack/internal/core"
)

// Serve speaks the protocol with c on every connection ln accepts, until ctx ends or ln is closed. When it returns,
// ln is closed and so is every connection it accepted; the core and its sessions are the caller's.
func Serve(ctx context.Context, ln net.Listener, c *core.Core) error {
	var mu sync.Mutex
	conns := map[net.Conn]bool{}
	var wg sync.WaitGroup
	stop := context.AfterFunc(ctx, func() { ln.Close() })
	defer stop()
	defer func() {
		ln.Close()
		mu.Lock()
		for cn := range conns {
			cn.Close()
		}
		mu.Unlock()
		wg.Wait()
	}()
	for {
		cn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		mu.Lock()
		conns[cn] = true
		mu.Unlock()
		wg.Go(func() {
			_ = c.Serve(ctx, cn)
			cn.Close()
			mu.Lock()
			delete(conns, cn)
			mu.Unlock()
		})
	}
}
