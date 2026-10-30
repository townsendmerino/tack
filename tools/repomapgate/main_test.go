package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/townsendmerino/aikit/chunk"
	"github.com/townsendmerino/aikit/chunk/treesitter"

	"github.com/townsendmerino/tack/internal/kensearch"
)

// §13.3's map: exported symbols only, each file's symbols by how many other .go files name them (test files count as
// references but get no row), files by their summed references and then by path.
func TestBuildMap(t *testing.T) {
	if _, err := chunk.Get(kensearch.Chunker); err != nil {
		chunk.Register(kensearch.Chunker, treesitter.New())
	}
	root := t.TempDir()
	for p, src := range map[string]string{
		"go.mod":      "module m\n",
		"a/a.go":      "package a\n\nfunc Popular() {}\n\ntype Lonely struct{}\n\nfunc hidden() {}\n",
		"b/b.go":      "package b\n\nfunc B() { Popular() }\n",
		"c/c.go":      "package c\n\nfunc C() { Popular() }\n",
		"a/x_test.go": "package a\n\nvar _ Lonely\n",
	} {
		os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0o755)
		os.WriteFile(filepath.Join(root, p), []byte(src), 0o644)
	}
	s, err := kensearch.Open(context.Background(), root, kensearch.Options{Mode: "bm25"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	m, _, err := buildMap(context.Background(), s, root)
	if err != nil {
		t.Fatal(err)
	}
	want := mapTitle + "\na/a.go: Popular, Lonely\nb/b.go: B\nc/c.go: C\n"
	if m != want {
		t.Fatalf("map:\n%s\nwant:\n%s", m, want)
	}
	if strings.Contains(m, "hidden") || strings.Contains(m, "x_test.go") {
		t.Fatalf("an unexported symbol or a test file is mapped:\n%s", m)
	}
}

// The gold symbol counts only on the gold file's own row: a name listed on another file's row does not.
func TestRowLists(t *testing.T) {
	m := mapTitle + "\na/a.go: Options, Run\nb/b.go: Flags.Options\n"
	if !rowLists(m, "a/a.go", "Run") || rowLists(m, "b/b.go", "Options") || rowLists(m, "c/c.go", "Run") {
		t.Fatal("rowLists counted a symbol off its file's row")
	}
}
