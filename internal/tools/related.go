package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/townsendmerino/tack/internal/server"
)

// Relater is a Searcher that finds code similar in meaning to a place in the repository: ken's FindRelated, a
// dense-only query on the chunk at path:line (internal/kensearch). Relates says whether this index can answer it:
// only ken's semantic and hybrid modes hold the vectors it needs.
type Relater interface {
	Searcher
	Related(ctx context.Context, path string, line, k int) ([]Hit, error)
	Relates() bool
	// Sync brings the index up to date for paths (Symbols.Sync): without a watcher (tack run), an edit is not in it
	// until synced, and path:line would name the chunk the file had before.
	Sync(ctx context.Context, paths []string) (bool, error)
}

// relatedDefaultK and relatedMaxK bound find_related's k: few hits by default, as each is a chunk of code.
const (
	relatedDefaultK = 5
	relatedMaxK     = 10
)

type relatedTool struct{ r Relater }

func (relatedTool) ReadOnly() bool { return true }

func (relatedTool) Spec() server.Tool {
	return server.Tool{Name: "find_related",
		Description: "Find code similar in meaning to the code at path:line.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"line":{"type":"integer"},"k":{"type":"integer"}},"required":["path","line"]}`)}
}

// Run resolves path inside the workspace, as read does, syncs it into the index, and asks for the chunks nearest the
// one holding line, the source chunk left out, printed as search prints its hits.
func (t relatedTool) Run(ctx context.Context, ws *Workspace, input json.RawMessage) (Result, error) {
	var in struct {
		Path string `json:"path"`
		Line int    `json:"line"`
		K    int    `json:"k"`
	}
	if err := decode(input, &in); err != nil || in.Path == "" {
		return errorf("bad input: find_related takes a path and a line, and optionally k"), nil
	}
	if in.Line < 1 {
		return errorf("bad input: find_related needs a line (from 1) in %s", in.Path), nil
	}
	abs, err := ws.Resolve(in.Path)
	if err != nil {
		return ws.pathError(in.Path, err), nil
	}
	if in.K <= 0 {
		in.K = relatedDefaultK
	}
	in.K = min(in.K, relatedMaxK)
	rel := ws.Rel(abs)
	if _, err := t.r.Sync(ctx, []string{rel}); err != nil {
		return errorf("find_related: bringing ken's index up to date for %s: %v", in.Path, err), nil
	}
	hits, err := t.r.Related(ctx, rel, in.Line, in.K)
	if err != nil {
		return errorf("find_related: %v", err), nil
	}
	if len(hits) == 0 {
		return Result{Text: fmt.Sprintf("no results: nothing indexed at %s:%d, or nothing like it", in.Path, in.Line)}, nil
	}
	var b strings.Builder
	for _, h := range hits {
		fmt.Fprintf(&b, "%s:%d-%d\n%s\n\n", h.File, h.StartLine, h.EndLine, strings.TrimRight(h.Text, "\n"))
	}
	return Result{Text: b.String()}, nil
}
