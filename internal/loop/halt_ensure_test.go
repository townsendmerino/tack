package loop

import (
	"context"
	"slices"
	"testing"

	"github.com/townsendmerino/tack/internal/halt"
	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server"
)

// loadingSource is a model source still starting its server: Ensure returns only when its context ends.
type loadingSource struct{ started chan struct{} }

func (l loadingSource) Ensure(ctx context.Context) (server.Server, error) {
	close(l.started)
	<-ctx.Done()
	return nil, context.Cause(ctx)
}
func (loadingSource) Model() string { return "m" }
func (loadingSource) Window() int   { return 0 }
func (loadingSource) Touch()        {}
func (loadingSource) Close() error  { return nil }

// TestAHaltWhileTheModelLoadsIsAHalt: a halt that lands while the source is still starting its server ends the turn
// with daemon.halted and stop reason halted, not as a server error.
func TestAHaltWhileTheModelLoadsIsAHalt(t *testing.T) {
	h := newHarness(t, Budget{})
	cfg := h.sess.cfg
	src := loadingSource{started: make(chan struct{})}
	cfg.Source = src
	h.sess = New(cfg)
	ctx, cancel := context.WithCancelCause(context.Background())
	go func() {
		<-src.started
		cancel(&halt.Halted{DaemonHalted: proto.DaemonHalted{Trigger: proto.HaltByCommand, By: "test"}})
	}()
	reason, err := h.sess.Turn(ctx, "go")
	var types []proto.EventType
	for _, ev := range h.events {
		types = append(types, ev.Type)
	}
	if err != nil || reason != proto.StopHalted || !slices.Contains(types, proto.EvDaemonHalted) || slices.Contains(types, proto.EvError) {
		t.Errorf("reason %q, %v; events %v", reason, err, types)
	}
}
