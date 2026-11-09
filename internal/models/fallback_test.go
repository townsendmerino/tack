package models

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/townsendmerino/tack/internal/config"
	"github.com/townsendmerino/tack/internal/server"
)

// countingSource is a Source that counts its Ensures and names itself.
type countingSource struct {
	name    string
	ensures int
	ticks   int
	err     error
}

func (c *countingSource) Ensure(context.Context) (server.Server, error) {
	c.ensures++
	return nil, c.err
}
func (c *countingSource) Model() string              { return c.name + "-model" }
func (c *countingSource) Window() int                { return len(c.name) * 1000 }
func (c *countingSource) Touch()                     {}
func (c *countingSource) Close() error               { return nil }
func (c *countingSource) Tick(context.Context) error { c.ticks++; return nil }

// TestPressureMovesTurnsToTheFallback: a turn starts on this machine's model while its memory pressure is normal, and
// on the fallback at warning or worse, the status line told so; the session's model and window are the server it is
// on. Back at normal, turns return. The child's idle rules still run (Tick).
func TestPressureMovesTurnsToTheFallback(t *testing.T) {
	mac, nobara := &countingSource{name: "mac"}, &countingSource{name: "nobara"}
	p := PressureNormal
	f := NewFallback(mac, nobara, "nobara", func() (Pressure, error) { return p, nil })
	var mu sync.Mutex
	var said []string
	ctx := WithStatus(context.Background(), func(srv, state, msg string) {
		mu.Lock()
		said = append(said, srv+" "+state+": "+msg)
		mu.Unlock()
	})
	if _, err := f.Ensure(ctx); err != nil || mac.ensures != 1 || f.Model() != "mac-model" || len(said) != 0 {
		t.Fatalf("at normal: %v, mac %d, model %s, said %q; want this machine's model, quietly", err, mac.ensures, f.Model(), said)
	}
	for _, level := range []Pressure{PressureWarning, PressureCritical} {
		p = level
		if _, err := f.Ensure(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if nobara.ensures != 2 || mac.ensures != 1 || f.Model() != "nobara-model" || f.Window() != 6000 {
		t.Fatalf("under pressure: mac %d, nobara %d, model %s, window %d; want both turns on nobara", mac.ensures, nobara.ensures, f.Model(), f.Window())
	}
	if len(said) != 2 || !strings.Contains(said[0], "nobara moved: memory pressure is warning on this machine; using nobara") {
		t.Fatalf("status %q; want each moved turn announced", said)
	}
	p = PressureNormal
	if _, err := f.Ensure(ctx); err != nil || mac.ensures != 2 || f.Model() != "mac-model" {
		t.Fatalf("back at normal: %v, mac %d, model %s", err, mac.ensures, f.Model())
	}
	if err := f.Tick(context.Background()); err != nil || mac.ticks != 1 {
		t.Fatalf("Tick: %v, mac ticked %d; want the child's idle rules applied", err, mac.ticks)
	}
}

// TestAFallbackThatFailsSaysSo: a moved turn whose fallback cannot serve (busy, down) fails with its reason; it does
// not fall back to the model the pressure is keeping off.
func TestAFallbackThatFailsSaysSo(t *testing.T) {
	mac := &countingSource{name: "mac"}
	nobara := &countingSource{name: "nobara", err: &BusyError{Server: "nobara", Message: "a timed run holds the lock"}}
	f := NewFallback(mac, nobara, "nobara", func() (Pressure, error) { return PressureCritical, nil })
	_, err := f.Ensure(context.Background())
	if !errors.As(err, new(*BusyError)) || mac.ensures != 0 || !strings.Contains(err.Error(), "memory pressure is critical") {
		t.Fatalf("%v, mac %d; want nobara's busy, with the pressure that moved it, and the Mac left alone", err, mac.ensures)
	}
}

// TestOpenMakesAFallback: a child with a fallback opens as a Fallback over the child and the named server.
func TestOpenMakesAFallback(t *testing.T) {
	cfg := &config.Config{Servers: map[string]config.Server{
		"mac":    {Kind: config.KindChild, Binary: "/bin/goinfer-serve", ModelPath: "/m/q.gguf", Model: "q", Ctx: 16384, Fallback: "nobara"},
		"nobara": {Kind: config.KindRemote, URL: "http://nobara:8091", OnDemand: true},
	}}
	src, err := Open(cfg, "mac", "/tmp/tk", nil)
	if err != nil {
		t.Fatal(err)
	}
	f, ok := src.(*Fallback)
	if !ok {
		t.Fatalf("Open made %T", src)
	}
	if _, ok := f.primary.(*childSource); !ok {
		t.Errorf("the primary is %T, want the child", f.primary)
	}
	if _, ok := f.fallback.(*OnDemand); !ok {
		t.Errorf("the fallback is %T, want nobara's on-demand source", f.fallback)
	}
}
