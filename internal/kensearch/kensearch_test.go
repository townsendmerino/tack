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

// TestBM25DoesNotRelate: a bm25 index has no vectors, so it says it cannot answer find_related, and tack does not
// offer the tool (tools.Options.FindRelated).
func TestBM25DoesNotRelate(t *testing.T) {
	s, err := Open(context.Background(), slice, Options{Mode: "bm25"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.Relates() {
		t.Fatal("a bm25 index says it relates")
	}
}

// TestHybridRelates: in hybrid mode, find_related's hits for a chunk are other chunks, not the one asked about, with
// the file's own lines as their text (K10).
func TestHybridRelates(t *testing.T) {
	if *kenModel == "" {
		t.Skip("needs -ken-model")
	}
	s, err := Open(context.Background(), slice, Options{Mode: "hybrid", ModelDir: *kenModel})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if !s.Relates() {
		t.Fatal("a hybrid index says it does not relate")
	}
	src, err := s.Search(context.Background(), "tokenize", 1)
	if err != nil || len(src) == 0 {
		t.Fatalf("a chunk to start from: %v, %v", src, err)
	}
	from := src[0]
	hits, err := s.Related(context.Background(), from.File, from.StartLine, 5)
	if err != nil || len(hits) == 0 {
		t.Fatalf("Related(%s:%d): %v, %v", from.File, from.StartLine, hits, err)
	}
	for _, h := range hits {
		if h.File == from.File && h.StartLine == from.StartLine && h.EndLine == from.EndLine {
			t.Errorf("the chunk asked about came back: %s:%d-%d", h.File, h.StartLine, h.EndLine)
		}
		if h.Text == "" || h.EndLine < h.StartLine {
			t.Errorf("hit %+v", h)
		}
	}
}
