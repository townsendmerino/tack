package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/townsendmerino/tack/internal/core"
)

// RunOptions configures Run.
type RunOptions struct {
	StateDir string
	Version  string
	// Grace is how long a shutdown lets running tool calls finish before it cancels their turns (core.Drain).
	Grace time.Duration
	// NewCore makes the daemon's core under ctx. It must set core.Options.ClientToken to token and
	// core.Options.Shutdown to shutdown. Run ends ctx once the drain is over, and before it closes the core.
	NewCore func(ctx context.Context, token string, shutdown func()) (*core.Core, error)
	// Watch, when set, runs beside the core from before it serves (halt.Watch) until ctx ends; Run waits for it to
	// return before it closes the core and releases the lock.
	Watch func(ctx context.Context, c *core.Core)
	// Log is told what the daemon does: what it recovered, refused connections, the drain. nil discards it.
	Log io.Writer
}

// Run is the daemon (DESIGN §6.1). It takes the lock, or returns a *HeldError naming the daemon that has it; makes a
// fresh client token; makes the core and recovers the turns a crash left open; binds the socket; and serves until ctx
// ends or a privileged client asks daemon/shutdown. Then it stops accepting, drains the core within
// Grace, closes the connections, ends the core's context and its watcher, closes the core, and releases the lock.
func Run(ctx context.Context, o RunOptions) error {
	log := o.Log
	if log == nil {
		log = io.Discard
	}
	l, err := Acquire(o.StateDir, o.Version)
	if err != nil {
		return err
	}
	defer l.Release()
	l.SetShutdown(DrainWait(o.Grace)) // the stopping record says so, and a start waits that long for the lock
	tok, err := l.NewToken()
	if err != nil {
		return err
	}
	stop := make(chan struct{})
	var once sync.Once
	cctx, ccancel := context.WithCancel(context.Background())
	defer ccancel()
	c, err := o.NewCore(cctx, tok, func() { once.Do(func() { close(stop) }) })
	if err != nil {
		return err
	}
	defer c.Close()
	// Deferred after Close, so it runs first: the core's context and its watcher end before the core closes.
	var watching sync.WaitGroup
	defer func() {
		ccancel()
		watching.Wait()
	}()
	if o.Watch != nil {
		watching.Go(func() { o.Watch(cctx, c) })
	}
	ids, err := c.Recover()
	for _, id := range ids {
		fmt.Fprintf(log, "tack daemon: %s was inside a turn when tack stopped; it is marked interrupted\n", id)
	}
	if err != nil {
		fmt.Fprintf(log, "tack daemon: recovery: %v\n", err)
	}
	ln, err := l.Listen()
	if err != nil {
		return err
	}
	ln.Refused = func(err error) { fmt.Fprintln(log, "tack daemon:", err) }
	// Connections are served under their own context, ended only after the drain, so clients see the turns end.
	sctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	served := make(chan error, 1)
	go func() { served <- Serve(sctx, ln, c) }()
	fmt.Fprintf(log, "tack daemon %s: serving on %s\n", o.Version, SocketPath(o.StateDir))

	var serveErr error
	select {
	case <-ctx.Done():
	case <-stop:
	case serveErr = <-served:
		served = nil
	}
	// Stop accepting first: closing the listener marks the record stopping, then unlinks the socket, so a new client
	// starts the next daemon, which waits for this one's lock (takeLock), instead of reaching one that refuses it. The
	// connections already open stay, and see their turns drain.
	ln.Close()
	fmt.Fprintf(log, "tack daemon: shutting down; no new connections; running tool calls have %s to finish\n", o.Grace)
	if cancelled := c.Drain(o.Grace); len(cancelled) > 0 {
		fmt.Fprintf(log, "tack daemon: cancelled the turns of %v after the grace window\n", cancelled)
	}
	cancel()
	if served != nil {
		serveErr = <-served
	}
	if serveErr != nil && !errors.Is(serveErr, context.Canceled) {
		return fmt.Errorf("daemon: serving: %w", serveErr)
	}
	return nil
}
