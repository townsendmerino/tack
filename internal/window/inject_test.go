package window

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/townsendmerino/tack/internal/tools"
)

// The statement's named code: backquoted qualified identifiers in order, (*T).m as T.m, without Go words, single
// letters or single-word all-lower-case names.
func TestInjectIdentifiers(t *testing.T) {
	got := InjectIdentifiers("In `serve`, `(*loadedModel).prepare(sm sampling, top_p int)` calls `pull.Select(files []File)`; " +
		"`kv`, `x`, `nil` and `pull.Select` again are not new.")
	if want := "loadedModel.prepare pull.Select File"; strings.Join(got, " ") != want {
		t.Fatalf("identifiers %v, want %s", got, want)
	}
}

type fakeIndex struct {
	defs map[string][]tools.Def
	hits []tools.Hit
}

func (f fakeIndex) Definition(_ context.Context, name string) ([]tools.Def, error) {
	return f.defs[name], nil
}
func (f fakeIndex) Search(context.Context, string, int) ([]tools.Hit, error) { return f.hits, nil }

// Candidates: each name's definitions (at most 3, from the doc line), the shorter name when the full one finds
// nothing, then search hits without _test.go or non-Go files.
func TestInjectCandidates(t *testing.T) {
	ix := fakeIndex{
		defs: map[string][]tools.Def{
			"pull.Select": {{File: "pull/pull.go", StartLine: 10, EndLine: 20, DocLine: 8}},
			"Flags": {{File: "a.go", StartLine: 1, EndLine: 2}, {File: "b.go", StartLine: 1, EndLine: 2},
				{File: "c.go", StartLine: 1, EndLine: 2}, {File: "d.go", StartLine: 1, EndLine: 2}},
		},
		hits: []tools.Hit{{File: "x_test.go", StartLine: 1, EndLine: 3}, {File: "README.md", StartLine: 1, EndLine: 3},
			{File: "pull/registry.go", StartLine: 5, EndLine: 9}},
	}
	got, err := InjectCandidates(context.Background(), ix, "Make `pull.Select` and `Flags.Options` agree.")
	if err != nil {
		t.Fatal(err)
	}
	var spans []string
	for _, it := range got {
		spans = append(spans, it.File+":"+strconv.Itoa(it.Start)+"-"+strconv.Itoa(it.End))
	}
	if want := "pull/pull.go:8-20 a.go:1-2 b.go:1-2 c.go:1-2 pull/registry.go:5-9"; strings.Join(spans, " ") != want {
		t.Fatalf("candidates %v, want %s", spans, want)
	}
}

// Packing: whole items while they fit at 3 bytes a token, skipping one too big (and going on) and one already packed;
// each packed item carries its lines.
func TestInjectPack(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.go"), []byte(strings.Repeat("abcdefghijklmnopqrstuvwxyz\n", 40)), 0o644)
	got := InjectPack(root, []InjectItem{
		{File: "a.go", Start: 1, End: 2},   // 18 tokens
		{File: "a.go", Start: 3, End: 30},  // 252 tokens: too big for what is left of 100
		{File: "a.go", Start: 1, End: 1},   // already packed
		{File: "a.go", Start: 31, End: 33}, // 27 tokens
	}, 100)
	if len(got) != 2 || got[0].Start != 1 || got[1].Start != 31 || got[0].Text != strings.Repeat("abcdefghijklmnopqrstuvwxyz\n", 2)[:53] {
		t.Fatalf("packed %+v", got)
	}
	if txt := InjectText(got); !strings.Contains(txt, "a.go, lines 31-33:\n```go\n") || InjectText(nil) != "" {
		t.Fatalf("text:\n%s", txt)
	}
}

type fakeOutline map[string][]tools.Def

func (f fakeOutline) Outline(_ context.Context, path string) ([]tools.Def, error) {
	return f[path], nil
}

// A tree with no exported definition has no map at all, not a bare title.
func TestRepoMapEmpty(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n\nfunc hidden() {}\n"), 0o644)
	m, err := RepoMap(context.Background(), fakeOutline{"a.go": {{File: "a.go", Name: "hidden", StartLine: 3, EndLine: 3}}}, root)
	if err != nil || m != "" {
		t.Fatalf("map %q, %v; want none", m, err)
	}
}
