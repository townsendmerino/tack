package loop

import (
	"context"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/perms"
	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
	"github.com/townsendmerino/tack/internal/shell"
	"github.com/townsendmerino/tack/internal/tools"
)

// gateShell runs nothing: each command reports it started, then waits for release (or for its context, as a killed
// command would end).
type gateShell struct {
	started chan string
	release chan struct{}
	mu      sync.Mutex
	ran     []string
}

func (g *gateShell) Run(ctx context.Context, command string, _ time.Duration) (shell.Result, error) {
	g.mu.Lock()
	g.ran = append(g.ran, command)
	g.mu.Unlock()
	g.started <- command
	select {
	case <-g.release:
		return shell.Result{Output: "ran " + command}, nil
	case <-ctx.Done():
		return shell.Result{Exit: -1, Output: "killed"}, nil
	}
}

// drainHarness is a session in auto mode whose model calls bash twice in one response, through a gateShell.
func drainHarness(t *testing.T) (*harness, *gateShell) {
	t.Helper()
	h := newHarness(t, Budget{}, goinfertest.Calls("", "t1", "bash", `{"command":"echo one"}`, "t2", "bash", `{"command":"echo two"}`),
		goinfertest.Says("not reached"))
	sh := &gateShell{started: make(chan string, 4), release: make(chan struct{})}
	cfg := h.sess.cfg
	cfg.Tools, cfg.Shell = tools.New(tools.Options{Write: true, Shell: sh}), sh
	cfg.Mode = proto.ModeAuto
	cfg.Perms = &perms.Engine{Root: h.ws.Root(), Path: os.Getenv("PATH"), Sandboxed: true}
	h.sess = New(cfg)
	return h, sh
}

// TestDrainStopsBetweenSteps: a drain lets the call that is running finish, runs no other, and ends the turn before
// its next request, with stop reason shutdown (DESIGN §6.1).
func TestDrainStopsBetweenSteps(t *testing.T) {
	h, sh := drainHarness(t)
	stop := make(chan string, 1)
	go func() { s, _ := h.sess.Turn(context.Background(), "run both"); stop <- s }()
	<-sh.started // echo one is running
	h.sess.Drain()
	close(sh.release)
	var got string
	select {
	case got = <-stop:
	case <-time.After(10 * time.Second):
		t.Fatal("the drained turn did not end")
	}
	if got != proto.StopShutdown {
		t.Fatalf("stop reason %q, want %q", got, proto.StopShutdown)
	}
	if f := h.finished("t1"); f.Outcome != proto.OutcomeOK || f.Result != "ran echo one" {
		t.Errorf("the running call: %+v; want it finished", f)
	}
	if f := h.finished("t2"); f.Outcome != proto.OutcomeCancelled || f.Result != "error: not run: "+ErrShutdown.Error() {
		t.Errorf("the next call: %+v; want it not run, for the shutdown", f)
	}
	if !slices.Equal(sh.ran, []string{"echo one"}) || len(h.script.Requests) != 1 {
		t.Errorf("ran %q in %d requests; want only echo one, and no request after it", sh.ran, len(h.script.Requests))
	}
}

// TestShutdownCancelsATurnThatDidNotDrain: a turn still running when the grace window ends is cancelled with
// ErrShutdown as its cause: its command is killed, and the turn ends with stop reason shutdown, not as a user's cancel.
func TestShutdownCancelsATurnThatDidNotDrain(t *testing.T) {
	h, sh := drainHarness(t)
	ctx, cancel := context.WithCancelCause(context.Background())
	stop := make(chan string, 1)
	go func() { s, _ := h.sess.Turn(ctx, "run both"); stop <- s }()
	<-sh.started
	h.sess.Drain()
	cancel(ErrShutdown) // the grace window ended with echo one still running
	var got string
	select {
	case got = <-stop:
	case <-time.After(10 * time.Second):
		t.Fatal("the cancelled turn did not end")
	}
	if got != proto.StopShutdown {
		t.Fatalf("stop reason %q, want %q", got, proto.StopShutdown)
	}
	if f := h.finished("t2"); f.Result != "error: not run: "+ErrShutdown.Error() {
		t.Errorf("the next call: %+v; want it not run, for the shutdown", f)
	}
}
