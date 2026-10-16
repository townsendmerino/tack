package models

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"sync"

	"github.com/townsendmerino/tack/internal/config"
	"github.com/townsendmerino/tack/internal/server"
	"github.com/townsendmerino/tack/internal/server/goinfer"
)

// Source gives the loop a ready model server: a supervised child on this machine, or a remote goinfer.
type Source interface {
	// Ensure returns a server with the model ready to answer, starting or loading it if needed.
	Ensure(ctx context.Context) (server.Server, error)
	// Model is the model id requests name.
	Model() string
	// Window is the served context window, known once Ensure has succeeded (0 before, or if the server does not say).
	Window() int
	// Touch records use, for idle unloading.
	Touch()
	// Close releases the source: a child is stopped; a remote is left running.
	Close() error
}

// Open returns the source for the named server in cfg. stateDir is tack's state directory; a child keeps its files
// under stateDir/goinfer/<name>.
func Open(cfg *config.Config, name, stateDir string) (Source, error) {
	s, ok := cfg.Servers[name]
	if !ok {
		return nil, fmt.Errorf("models: no server %q", name)
	}
	switch s.Kind {
	case config.KindChild:
		sup, err := NewSupervisor(ChildConfig{Binary: s.Binary, ModelPath: s.ModelPath, Model: s.Model, Ctx: s.Ctx,
			Args: s.Args, Dir: filepath.Join(stateDir, "goinfer", name), IdleUnload: s.IdleUnload.Duration,
			IdleExit: s.IdleExit.Duration})
		if err != nil {
			return nil, err
		}
		return &childSource{sup: sup}, nil
	case config.KindRemote:
		tok := ""
		if s.TokenFile != "" {
			var err error
			if tok, err = config.ReadToken(s.TokenFile); err != nil {
				return nil, err
			}
		}
		return NewRemote(name, s.URL, tok, s.Model, nil), nil
	}
	return nil, fmt.Errorf("models: server %q has kind %q", name, s.Kind)
}

type childSource struct {
	sup *Supervisor
}

func (c *childSource) Ensure(ctx context.Context) (server.Server, error) { return c.sup.Ensure(ctx) }
func (c *childSource) Model() string                                     { return c.sup.cfg.Model }
func (c *childSource) Touch()                                            { c.sup.Touch() }
func (c *childSource) Close() error                                      { return c.sup.Stop() }
func (c *childSource) Window() int {
	p, _ := c.sup.Plan()
	return p.ContextWindow
}

// Remote is a goinfer-serve tack does not manage (DESIGN §8.2): it reads the served window and checks the model is
// there; goinfer's own guards on that box decide memory.
type Remote struct {
	client *goinfer.Client
	model  string

	mu     sync.Mutex
	window int
}

// NewRemote returns a source for the goinfer at url. name labels it in events ("goinfer@<name>"); model may be empty
// when the server serves exactly one model. hc nil means a default client.
func NewRemote(name, url, token, model string, hc *http.Client) *Remote {
	return &Remote{client: goinfer.New("goinfer@"+name, url, token, hc), model: model}
}

// Ensure checks the server answers and serves the model, and records its window.
func (r *Remote) Ensure(ctx context.Context) (server.Server, error) {
	ms, err := r.client.Models(ctx)
	if err != nil {
		return nil, fmt.Errorf("models: %s: %w", r.client.Name(), err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.model == "" {
		if len(ms) != 1 {
			return nil, fmt.Errorf("models: %s serves %d models; name one in the config", r.client.Name(), len(ms))
		}
		r.model = ms[0].ID
	}
	for _, m := range ms {
		if m.ID == r.model {
			r.window = m.ContextWindow
			return r.client, nil
		}
	}
	return nil, fmt.Errorf("models: %s does not serve %q", r.client.Name(), r.model)
}

func (r *Remote) Model() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.model
}

func (r *Remote) Window() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.window
}

func (r *Remote) Touch()       {}
func (r *Remote) Close() error { return nil }
