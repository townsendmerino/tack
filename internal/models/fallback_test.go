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
// on the fallback at warning or worse, the status line told so; the session's model is the server it is on, and its
// window the smaller of the two (TestAFallbacksWindowFitsBothServers). Back at normal, turns return. The child's idle rules still run (Tick).
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
	if nobara.ensures != 2 || mac.ensures != 1 || f.Model() != "nobara-model" || f.Window() != 3000 {
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

// TestAMovedTurnFindingNobaraBusyOffersNoMacModel: an on-demand server's busy error offers this machine's model (tack
// run --server mac), but a turn the memory pressure moved cannot use it. The fallback asks for no offer, so the error
// names nobara's holder and the pressure, offers nothing, and keeps what wraps it whole.
func TestAMovedTurnFindingNobaraBusyOffersNoMacModel(t *testing.T) {
	f := &fakeServe{state: "idle", startBusy: true, msg: "a timed run holds goinfer's timing lock (pid 7)", lockKey: "k"}
	nobara, _ := onDemand(t, f, nil) // offers "mac"
	if _, err := nobara.Ensure(context.Background()); err == nil || !strings.Contains(err.Error(), "--server mac") {
		t.Fatalf("asked directly: %v; want the offer of this machine's model", err)
	}
	fb := NewFallback(&countingSource{name: "mac"}, nobara, "nobara", func() (Pressure, error) { return PressureWarning, nil })
	_, err := fb.Ensure(context.Background())
	var be *BusyError
	if !errors.As(err, &be) || len(be.Offer) != 0 || strings.Contains(err.Error(), "--server mac") ||
		!strings.Contains(err.Error(), "memory pressure is warning") || !strings.Contains(err.Error(), "pid 7") {
		t.Fatalf("%v; want nobara's busy, with the holder and the pressure, and no offer of the model the pressure keeps off", err)
	}
}

// sizedSource is a Source with a set window, 0 while unknown.
type sizedSource struct {
	countingSource
	window int
}

func (s *sizedSource) Window() int { return s.window }

// TestAFallbacksWindowFitsBothServers: a session's budget is sized once, from Window, at its creation, and its turns
// may run on either server; so Window is the smaller of the windows known, not the one the last turn ran on.
func TestAFallbacksWindowFitsBothServers(t *testing.T) {
	mac := &sizedSource{countingSource{name: "mac"}, 32768}
	nobara := &sizedSource{countingSource{name: "nobara"}, 0} // not asked yet
	p := PressureNormal
	f := NewFallback(mac, nobara, "nobara", func() (Pressure, error) { return p, nil })
	if w := f.Window(); w != 32768 {
		t.Fatalf("with only the Mac's window known: %d; want 32768", w)
	}
	nobara.window = 16384
	if w := f.Window(); w != 16384 {
		t.Fatalf("on the Mac, with nobara's smaller window known: %d; want 16384, which fits a move", w)
	}
	p = PressureWarning
	f.Ensure(context.Background())
	mac.window, nobara.window = 8192, 16384
	if w := f.Window(); w != 8192 {
		t.Fatalf("on nobara, with the Mac's smaller window: %d; want 8192, which fits the return", w)
	}
}
