// Package kensearch is tack's Searcher and Symbols over ken's public engine (ken v1.11.0; DESIGN §12, D6). It uses
// only engine.Open, the Workspace methods and the value types. ken's escape hatches (WatchedIndex, Structural,
// StructuralIfBuilt, SetReranker, SetExtraChunks) and its aliases into ken's internal packages are for ken's own binaries, so ken stays
// free to change what sits behind them.
//
// The tree-sitter chunker is not registered here: as with ken and ken-mcp, registering it is the binary's job
// (cmd/tack does). Open refuses to start if it is missing rather than falling back to another chunker.
package kensearch

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"sync"
	"time"

	"github.com/townsendmerino/aikit/chunk"
	"github.com/townsendmerino/ken/engine"

	"github.com/townsendmerino/tack/internal/tools"
)

// Chunker is the chunker tack asks ken for.
const Chunker = "treesitter"

// Options configures a workspace's engine. ModelDir is passed to ken explicitly: tack never relies on ken's
// ~/.ken/model default, because the app passes the model bundled in Contents/Resources (DESIGN §12).
type Options struct {
	Mode     string // "bm25", "semantic", "hybrid" or "hybrid-rerank"
	ModelDir string // the Model2Vec snapshot directory; required for every mode but bm25
	Watch    bool   // keep the index fresh as files change: the daemon's shape
	Snapshot bool   // persist the index to <root>/.ken and reload it on the next Open
	Log      io.Writer
}

// Searcher is one workspace's engine.
type Searcher struct {
	ws   *engine.Workspace
	root string // absolute
	// opened is when Open began: ken's index holds every file as it was at Open, so a file not modified since is current.
	opened time.Time

	mu sync.Mutex
	// synced is each file's state (Searcher.state) when Sync last refreshed it.
	synced map[string]string
}

// Open builds (or loads) ken's index over root.
func Open(ctx context.Context, root string, o Options) (*Searcher, error) {
	if _, err := chunk.Get(Chunker); err != nil {
		return nil, fmt.Errorf("kensearch: the %s chunker is not registered: the binary must register it (cmd/tack/ken.go)", Chunker)
	}
	switch o.Mode {
	case "bm25":
	case "semantic", "hybrid", "hybrid-rerank":
		if o.ModelDir == "" {
			return nil, fmt.Errorf("kensearch: mode %s needs a model directory", o.Mode)
		}
	default:
		return nil, fmt.Errorf("kensearch: mode %q, want bm25, semantic, hybrid or hybrid-rerank", o.Mode)
	}
	opened := time.Now()
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	log := o.Log
	if log == nil {
		log = io.Discard // never stdout: a stdio mode's stdout is protocol (CLAUDE.md, "Stdio modes")
	}
	ws, err := engine.Open(ctx, abs, engine.Options{Mode: o.Mode, Chunker: Chunker, ModelDir: o.ModelDir,
		Watch: o.Watch, Snapshot: o.Snapshot, MmapModel: true, LogWriter: log})
	if err != nil {
		return nil, err
	}
	return &Searcher{ws: ws, root: abs, opened: opened, synced: map[string]string{}}, nil
}

// Search implements tools.Searcher.
func (s *Searcher) Search(ctx context.Context, query string, k int) ([]tools.Hit, error) {
	hits, err := s.ws.Search(ctx, query, k)
	if err != nil {
		return nil, err
	}
	out := make([]tools.Hit, len(hits))
	for i, h := range hits {
		out[i] = tools.Hit{File: h.File, StartLine: h.StartLine, EndLine: h.EndLine, Text: h.Text, Hash: h.Hash,
			Score: h.Score, Rank: h.Rank, Gen: h.Gen}
	}
	return out, nil
}

// Generation is the index generation ken last published (K4's counter).
func (s *Searcher) Generation() uint64 { return s.ws.Status().Generation }

// Close stops the watcher and releases the index.
func (s *Searcher) Close() error {
	if s == nil || s.ws == nil {
		return nil
	}
	return s.ws.Close()
}

var _ tools.Searcher = (*Searcher)(nil)
