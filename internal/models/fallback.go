package models

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/townsendmerino/tack/internal/server"
)

// Fallback is a supervised child with another machine's server to move to under memory pressure (DESIGN §8.1). Each
// turn starts on the child while this machine's memory pressure is normal, and on the fallback (nobara's server) at
// warning or worse: no new job starts on the Mac's model at warning, and at critical the child's tick has unloaded it.
// The status line is told when a turn moves. Turns come back once the pressure is normal; each move costs the turn
// the cached prefix of the server it left.
type Fallback struct {
	primary, fallback Source
	name              string // the fallback's, for the status line
	pressure          func() (Pressure, error)

	mu     sync.Mutex
	active Source // the server the last turn started on
}

// NewFallback returns a source that starts each turn on primary, or on fallback, named name, while pressure reads
// warning or worse.
func NewFallback(primary, fallback Source, name string, pressure func() (Pressure, error)) *Fallback {
	return &Fallback{primary: primary, fallback: fallback, name: name, pressure: pressure, active: primary}
}

// Ensure starts the turn on the server the memory pressure allows. A fallback that cannot serve (busy, down) is the
// turn's error, with the pressure that moved it; the turn does not go back to the model the pressure is keeping off.
func (f *Fallback) Ensure(ctx context.Context) (server.Server, error) {
	if p, err := f.pressure(); err == nil && p >= PressureWarning {
		tellStatus(ctx, f.name, "moved", fmt.Sprintf("memory pressure is %s on this machine; using %s", p, f.name))
		f.setActive(f.fallback)
		srv, err := f.fallback.Ensure(ctx)
		if err != nil {
			return nil, fmt.Errorf("models: memory pressure is %s on this machine, so the turn moved to %s: %w", p, f.name, err)
		}
		return srv, nil
	}
	f.setActive(f.primary)
	return f.primary.Ensure(ctx)
}

func (f *Fallback) setActive(s Source) {
	f.mu.Lock()
	f.active = s
	f.mu.Unlock()
}

func (f *Fallback) current() Source {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.active
}

// Model, Window and Touch are the server the last turn started on.
func (f *Fallback) Model() string { return f.current().Model() }
func (f *Fallback) Window() int   { return f.current().Window() }
func (f *Fallback) Touch()        { f.current().Touch() }

// Close closes both.
func (f *Fallback) Close() error { return errors.Join(f.primary.Close(), f.fallback.Close()) }

// Tick applies the child's idle rules and memory guard, whichever server the turns are on.
func (f *Fallback) Tick(ctx context.Context) error {
	if t, ok := f.primary.(Ticker); ok {
		return t.Tick(ctx)
	}
	return nil
}
