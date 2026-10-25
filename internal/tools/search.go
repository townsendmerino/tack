package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/townsendmerino/tack/internal/server"
)

// Searcher is the seam for ken's engine (DESIGN §12, D6: in-process, through ken's public engine package);
// internal/kensearch implements it over engine.Workspace. Hit.Text is the file's lines StartLine..EndLine exactly (K10).
type Searcher interface {
	Search(ctx context.Context, query string, k int) ([]Hit, error)
}

// Hit is K2's structured hit as tack needs it: enough for the ledger's identity (file, lines, hash) and the
// inspector's "why" (score, rank, generation).
type Hit struct {
	File      string
	StartLine int
	EndLine   int
	Text      string
	Hash      string
	Score     float64
	Rank      int
	Gen       uint64
}

type searchTool struct{ s Searcher }

func (searchTool) ReadOnly() bool { return true }
func (searchTool) Spec() server.Tool {
	return server.Tool{Name: "search",
		Description: "Find code by meaning or name. Use first for where/how questions.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"},"k":{"type":"integer"}},"required":["query"]}`)}
}

func (t searchTool) Run(ctx context.Context, _ *Workspace, input json.RawMessage) (Result, error) {
	var in struct {
		Query string `json:"query"`
		K     int    `json:"k"`
	}
	if err := decode(input, &in); err != nil || in.Query == "" {
		return errorf("bad input: need a query"), nil
	}
	if in.K <= 0 || in.K > 20 {
		in.K = 8
	}
	hits, err := t.s.Search(ctx, in.Query, in.K)
	if err != nil {
		return errorf("search: %v", err), nil
	}
	var b strings.Builder
	for _, h := range hits {
		fmt.Fprintf(&b, "%s:%d-%d\n%s\n\n", h.File, h.StartLine, h.EndLine, strings.TrimRight(h.Text, "\n"))
	}
	if len(hits) == 0 {
		return Result{Text: "no results"}, nil
	}
	return Result{Text: b.String()}, nil
}
