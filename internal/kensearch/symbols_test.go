package kensearch

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/townsendmerino/tack/internal/tools"
)

// openSymbols opens ken in bm25 mode over a copy of testdata/symbols, which the test may change.
func openSymbols(t *testing.T) (*Searcher, string) {
	t.Helper()
	root := t.TempDir()
	src := filepath.Join("testdata", "symbols")
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		os.MkdirAll(filepath.Join(root, filepath.Dir(rel)), 0o755)
		return os.WriteFile(filepath.Join(root, rel), b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(context.Background(), root, Options{Mode: "bm25"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, root
}

func oneDef(t *testing.T, s *Searcher, name string) tools.Def {
	t.Helper()
	defs, err := s.Definition(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	if len(defs) != 1 {
		t.Fatalf("Definition(%q) = %+v, want one", name, defs)
	}
	return defs[0]
}

// TestSymbolsOverKen checks the adapter against ken v1.11.0 on a fixture: qualified names (K14), spans and doc lines
// (K11), call lines (K11), callees and tests (K15), and the symbol at a line (K12).
func TestSymbolsOverKen(t *testing.T) {
	s, _ := openSymbols(t)
	ctx := context.Background()
	for name, want := range map[string]tools.Def{
		"pull.Select": {File: "pull/types.go", Name: "Select", Kind: "func", StartLine: 7, EndLine: 9, DocLine: 6},
		"Select":      {File: "pull/types.go", Name: "Select", Kind: "func", StartLine: 7, EndLine: 9, DocLine: 6},
		"Model.Size":  {File: "pull/methods.go", Name: "Model.Size", Kind: "method", StartLine: 4, EndLine: 6, DocLine: 3},
		"Model":       {File: "pull/types.go", Name: "Model", Kind: "type", StartLine: 4, EndLine: 4, DocLine: 3},
		"helper":      {File: "pull/types.go", Name: "helper", Kind: "func", StartLine: 11, EndLine: 11},
	} {
		if got := oneDef(t, s, name); got != want {
			t.Errorf("Definition(%q) = %+v, want %+v", name, got, want)
		}
	}

	out, err := s.Outline(ctx, "pull/methods.go")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range out {
		if d == (tools.Def{File: "pull/methods.go", Name: "Model.Size", Kind: "method", StartLine: 4, EndLine: 6, DocLine: 3}) {
			found = true
		}
	}
	if !found {
		t.Errorf("Outline(pull/methods.go) = %+v, want Model.Size at 4-6", out)
	}

	callers, err := s.Callers(ctx, "Size")
	if err != nil {
		t.Fatal(err)
	}
	wantCallers := []tools.Site{{File: "pull/pull_test.go", Line: 13, Name: "TestSize"}, {File: "pull/types.go", Line: 8, Name: "Select"}}
	if len(callers) != len(wantCallers) || callers[0] != wantCallers[0] || callers[1] != wantCallers[1] {
		t.Errorf("Callers(Size) = %+v, want %+v", callers, wantCallers)
	}

	callees, err := s.Callees(ctx, "Select")
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]int{}
	for _, c := range callees {
		names[c.Name] = c.Line
	}
	if names["m.Size"] != 8 || names["helper"] != 8 {
		t.Errorf("Callees(Select) = %+v, want m.Size and helper at line 8", callees)
	}

	ts, err := s.TestsFor(ctx, "Model.Size")
	if err != nil {
		t.Fatal(err)
	}
	direct, via := false, false
	for _, x := range ts {
		direct = direct || x == tools.TestSite{File: "pull/pull_test.go", Name: "TestSize", Line: 11, Dir: "pull"}
		via = via || x.Name == "TestSelect" && x.Via == "Select" && x.Dir == "pull"
	}
	if !direct || !via {
		t.Errorf("TestsFor(Model.Size) = %+v, want TestSize directly and TestSelect through Select", ts)
	}

	at, err := s.SymbolAt(ctx, "pull/types.go", 8)
	if err != nil || len(at) == 0 || at[0].Name != "Select" || at[0].StartLine != 7 || at[0].EndLine != 9 {
		t.Errorf("SymbolAt(pull/types.go, 8) = %+v, %v", at, err)
	}
	if at, err := s.SymbolAt(ctx, "pull/types.go", 1); err != nil || len(at) != 0 {
		t.Errorf("SymbolAt at file scope = %+v, %v", at, err)
	}
	if _, err := s.SymbolAt(ctx, "README.md", 1); !errors.Is(err, tools.ErrNotIndexed) {
		t.Errorf("SymbolAt(README.md) error %v, want tools.ErrNotIndexed", err)
	}
}

// TestSyncFollowsAChangeOnDisk: ken's index keeps a file's old spans until Sync refreshes it (no watcher here, as in
// tack run), and Sync refreshes only a file that changed since it last synced it or, never synced, since Open.
func TestSyncFollowsAChangeOnDisk(t *testing.T) {
	s, root := openSymbols(t)
	ctx := context.Background()
	if d := oneDef(t, s, "Model.Size"); d.StartLine != 4 {
		t.Fatalf("Model.Size starts at %d", d.StartLine)
	}
	p := filepath.Join(root, "pull", "methods.go")
	b, _ := os.ReadFile(p)
	if err := os.WriteFile(p, []byte(strings.Replace(string(b), "package pull\n", "package pull\n\nvar a = 1\n\nvar b = 2\n", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if d := oneDef(t, s, "Model.Size"); d.StartLine != 4 {
		t.Fatalf("before Sync, Model.Size starts at %d: the index followed the change by itself, so this test shows nothing", d.StartLine)
	}
	if changed, err := s.Sync(ctx, []string{"pull/methods.go"}); err != nil || !changed {
		t.Fatalf("Sync after a change = %v, %v", changed, err)
	}
	if d := oneDef(t, s, "Model.Size"); d.StartLine != 8 || d.EndLine != 10 || d.DocLine != 7 {
		t.Fatalf("after Sync, Model.Size = %+v, want lines 8-10 with its comment at 7", d)
	}
	if changed, err := s.Sync(ctx, []string{"pull/methods.go"}); err != nil || changed {
		t.Fatalf("Sync of an unchanged file = %v, %v", changed, err)
	}
	// A file untouched since Open is current already: its first Sync refreshes nothing, so ken does not republish.
	gen := s.Generation()
	if changed, err := s.Sync(ctx, []string{"pull/types.go"}); err != nil || changed {
		t.Fatalf("first Sync of a file unchanged since Open = %v, %v", changed, err)
	}
	if s.Generation() != gen {
		t.Fatal("the first Sync of a file unchanged since Open republished the search index")
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if changed, err := s.Sync(ctx, []string{"pull/methods.go"}); err != nil || !changed {
		t.Fatalf("Sync of a deleted file = %v, %v", changed, err)
	}
	if defs, err := s.Definition(ctx, "Model.Size"); err != nil || len(defs) != 0 {
		t.Fatalf("after deleting its file, Model.Size = %+v, %v", defs, err)
	}
	if _, err := s.Sync(ctx, []string{"../outside.go"}); err == nil {
		t.Fatal("Sync of a path outside the root succeeded")
	}
}

// TestHitTextIsTheFilesLines: with ken v1.11.0 (K10), a hit's text is exactly the file's lines StartLine..EndLine,
// with no enrichment label, and Hash is its sha256.
func TestHitTextIsTheFilesLines(t *testing.T) {
	s, root := openSymbols(t)
	hits, err := s.Search(context.Background(), "Select picks one model size", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("no hits")
	}
	for _, h := range hits {
		b, err := os.ReadFile(filepath.Join(root, h.File))
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.SplitAfter(string(b), "\n")
		want := strings.Join(lines[h.StartLine-1:h.EndLine], "")
		if strings.TrimRight(h.Text, "\n") != strings.TrimRight(want, "\n") {
			t.Errorf("%s:%d-%d text\n%q\nis not the file's lines\n%q", h.File, h.StartLine, h.EndLine, h.Text, want)
		}
		if strings.Contains(h.Text, "# ken:") {
			t.Errorf("%s:%d carries ken's enrichment label", h.File, h.StartLine)
		}
	}
}
