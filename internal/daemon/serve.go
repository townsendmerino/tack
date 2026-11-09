package daemon

import (
	"context"
	"errors"
	"net"
	"sync"

	"github.com/townsendmerino/tack/internal/core"
)

// Serve speaks the protocol with c on every connection ln accepts, and returns when ctx ends, closing ln and every
// connection it accepted; the core and its sessions are the caller's. A shutdown closes ln first (Listener.Close,
// which marks the daemon stopping): nothing is accepted after that, and the connections already open are served until
// ctx ends, so their clients see the drain. Serve returns early only when accepting fails for another reason.
func Serve(ctx context.Context, ln net.Listener, c *core.Core) error {
	var mu sync.Mutex
	conns := map[net.Conn]bool{}
	var wg sync.WaitGroup
	accepting := make(chan error, 1) // the accept loop's end, with why
	go func() {
		for {
			cn, err := ln.Accept()
			if err != nil {
				accepting <- err
				return
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
	}()
	var err error
	select {
	case <-ctx.Done():
		ln.Close()
		<-accepting
	case err = <-accepting:
		if errors.Is(err, net.ErrClosed) { // a shutdown: serve what is open until ctx ends
			err = nil
			<-ctx.Done()
		}
		ln.Close()
	}
	mu.Lock()
	for cn := range conns {
		cn.Close()
	}
	mu.Unlock()
	wg.Wait() // the accept loop has ended, so nothing is added to wg now
	return err
}
