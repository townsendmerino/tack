package kensearch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"path/filepath"
	"strings"
	"testing"

	"github.com/townsendmerino/aikit/chunk"
	"github.com/townsendmerino/aikit/chunk/treesitter"
)

// The test binary registers the chunker as cmd/tack does.
func init() {
	chunk.Register(Chunker, treesitter.New(treesitter.WithParseTimeoutMicros(1_000_000)))
}

var kenModel = flag.String("ken-model", "", "a Model2Vec directory for the hybrid test (e.g. ~/.ken/model)")

// slice is the slice of ken that cmd/tack's tests answer questions about.
var slice = filepath.Join("..", "..", "cmd", "tack", "testdata", "ken")

func check(t *testing.T, s *Searcher) {
	t.Helper()
	hits, err := s.Search(context.Background(), "applyTokenBudget", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].File != "mcp/token_budget.go" || !strings.Contains(hits[0].Text, "applyTokenBudget") {
		t.Fatalf("hits: %+v", hits)
	}
	for _, h := range hits {
		sum := sha256.Sum256([]byte(h.Text))
		if h.Hash != hex.EncodeToString(sum[:]) {
			t.Errorf("%s:%d hash %s is not sha256(Text)", h.File, h.StartLine, h.Hash)
		}
		if h.Gen == 0 || h.Gen != s.Generation() {
			t.Errorf("hit generation %d, workspace %d", h.Gen, s.Generation())
		}
		if h.StartLine < 1 || h.EndLine < h.StartLine || h.Rank < 1 {
			t.Errorf("hit %+v", h)
		}
	}
}

func TestBM25OverASliceOfKen(t *testing.T) {
	s, err := Open(context.Background(), slice, Options{Mode: "bm25"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	check(t, s)
}

func TestHybrid(t *testing.T) {
	if *kenModel == "" {
		t.Skip("needs -ken-model")
	}
	s, err := Open(context.Background(), slice, Options{Mode: "hybrid", ModelDir: *kenModel})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	check(t, s)
}

func TestOpenRefusesWhatItCannotServe(t *testing.T) {
	for _, o := range []Options{{Mode: "hybrid"}, {Mode: "fuzzy"}, {Mode: ""}} {
		if _, err := Open(context.Background(), slice, o); err == nil {
			t.Errorf("Open(%+v) succeeded", o)
		}
	}
}
