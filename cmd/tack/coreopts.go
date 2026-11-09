package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"time"

	"github.com/townsendmerino/tack/internal/config"
	"github.com/townsendmerino/tack/internal/core"
	"github.com/townsendmerino/tack/internal/kensearch"
	"github.com/townsendmerino/tack/internal/loop"
	"github.com/townsendmerino/tack/internal/models"
	"github.com/townsendmerino/tack/internal/server"
	"github.com/townsendmerino/tack/internal/store"
	"github.com/townsendmerino/tack/internal/tools"
	"github.com/townsendmerino/tack/internal/window"
)

// coreOptions is the core's options from the config, as tack run and the daemon both make them: the store, the named
// server's source and sampling, the budgets and every [run] setting, ken, and halt's sweep. hc, when not nil, carries
// the model requests. Each caller adds what is its own (instructions, the edit kit, the token).
func coreOptions(cfg *config.Config, name, stateDir string, st *store.Store, hc *http.Client, stderr io.Writer) core.Options {
	var servers []string
	for n := range cfg.Servers {
		servers = append(servers, "goinfer@"+n)
	}
	sort.Strings(servers)
	opts := core.Options{Store: st, Version: version, Servers: servers, FitWindow: cfg.Run.FitWindow, StopRepeats: cfg.Run.StopRepeats, MaskObservations: cfg.Run.MaskObservations, Summarize: cfg.Run.Summarize, ResultNotes: cfg.Run.ResultNotes, EditMessages: cfg.Run.EditMessages, InstructionsHead: cfg.Run.InstructionsHead, RecentlyChanged: cfg.Run.RecentlyChanged,
		RefuseDuplicateEdits: cfg.Run.RefuseDuplicateEdits,
		VerifyDone:           cfg.Run.VerifyDone,
		BuildAfterEdit:       cfg.Run.BuildAfterEdit,
		RestateTask:          cfg.Run.RestateTask,
		EditLines:            cfg.Run.EditLines,
		CurrentSymbol:        cfg.Run.CurrentSymbol,
		EditTolerances:       cfg.Run.EditTolerances,
		LeanResults:          cfg.Run.LeanResults,
		CompactAtCliff:       cfg.Run.CompactAtCliff,
		WidenRepeats:         cfg.Run.WidenRepeats,
		InjectContext:        cfg.Run.InjectContext,
		RepoMap:              cfg.Run.RepoMap,
		Snapshots:            *cfg.Run.Snapshots,
		SnapshotMaxBytes:     cfg.Run.SnapshotMaxBytes,
		Budget: loop.Budget{MaxRequests: cfg.Run.MaxRequests, MaxToolCalls: cfg.Run.MaxToolCalls,
			MaxWall: cfg.Run.MaxWall.Duration, MaxTokens: cfg.Run.MaxTokens, TurnTokens: cfg.Run.TurnTokens,
			MaxErrors: cfg.Run.MaxErrors, SessionTokens: cfg.Run.SessionTokens, SessionToolCalls: cfg.Run.SessionToolCalls},
		Source: func(model string) (models.Source, error) {
			if model != "main" {
				return nil, fmt.Errorf("model %q: only the main role is configured", model)
			}
			return models.Open(cfg, name, stateDir, hc)
		}}
	if k := cfg.Ken; k != nil {
		// One-shot: build (or load the snapshot of) the index, no watcher. The daemon will watch.
		opts.OpenSearcher = func(ctx context.Context, root string) (tools.Searcher, error) {
			fmt.Fprintf(stderr, "tack: opening ken (%s) over %s\n", k.Mode, root)
			return kensearch.Open(ctx, root, kensearch.Options{Mode: k.Mode, ModelDir: k.ModelDir, Snapshot: true, Log: stderr})
		}
	}
	if cfg.Run.CurrentSymbol && cfg.Ken == nil {
		// The spans alone, for current_symbol: bm25 needs no model, and no snapshot leaves the workspace as it was.
		opts.OpenSpans = func(ctx context.Context, root string) (tools.Symbols, error) {
			start := time.Now()
			s, err := kensearch.Open(ctx, root, kensearch.Options{Mode: "bm25", Log: stderr})
			if err != nil {
				fmt.Fprintf(stderr, "tack: current_symbol: opening ken (bm25) over %s: %v\n", root, err)
				return nil, err
			}
			fmt.Fprintf(stderr, "tack: current_symbol: opened ken (bm25) over %s in %s\n", root, time.Since(start).Round(time.Millisecond))
			return s, nil
		}
	}
	if cfg.Run.InjectContext {
		// The injection's own index: bm25 needs no model, adds no tool, and no snapshot leaves the workspace as it was.
		opts.OpenInject = func(ctx context.Context, root string) (window.InjectIndex, error) {
			start := time.Now()
			s, err := kensearch.Open(ctx, root, kensearch.Options{Mode: "bm25", Log: stderr})
			if err != nil {
				return nil, err
			}
			fmt.Fprintf(stderr, "tack: inject_context: opened ken (bm25) over %s in %s\n", root, time.Since(start).Round(time.Millisecond))
			return s, nil
		}
	}
	if cfg.Run.RepoMap {
		// The map's own index, as inject_context's: bm25, no tool, no snapshot.
		opts.OpenRepoMap = func(ctx context.Context, root string) (window.MapIndex, error) {
			start := time.Now()
			s, err := kensearch.Open(ctx, root, kensearch.Options{Mode: "bm25", Log: stderr})
			if err != nil {
				return nil, err
			}
			fmt.Fprintf(stderr, "tack: repo_map: opened ken (bm25) over %s in %s\n", root, time.Since(start).Round(time.Millisecond))
			return s, nil
		}
	}
	opts.Sweep, opts.HaltDir = sweepTarget, stateDir
	if s, ok := cfg.Servers[name]; ok {
		opts.Sampling = server.Sampling{Temperature: s.Temperature, TopP: s.TopP, TopK: s.TopK}
	}
	if cfg.Run.SummaryRetrieve {
		opts.SummaryRetrieve = cfg.Run.SummaryRetrieveShare
	}
	return opts
}
