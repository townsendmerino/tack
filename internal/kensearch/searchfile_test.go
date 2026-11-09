package kensearch

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSearchFileKeepsToTheFile: SearchFile answers from the one file, each hit with its section's heading path, and
// refuses a file the index does not hold instead of answering it with nothing.
func TestSearchFileKeepsToTheFile(t *testing.T) {
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("# Rules\n\n## Staging\n\nNever stage with git add -A; stage explicit paths.\n\n"+
		"## Testing\n\nA skip is not a pass.\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(dir, "stage.go"), []byte("package x\n\n// stage explicit paths with git add\nfunc stage() {}\n"), 0o644))
	s, err := Open(context.Background(), dir, Options{Mode: "bm25"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	hits, err := s.SearchFile(context.Background(), "stage explicit paths git add", 5, "CLAUDE.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || strings.Join(hits[0].HeadingPath, "/") != "Rules/Staging" {
		t.Fatalf("hits %+v", hits)
	}
	all, _ := s.Search(context.Background(), "stage explicit paths git add", 5)
	if len(all) < 2 || len(hits) >= len(all) {
		t.Fatalf("the whole search should also find stage.go: %+v", all)
	}
	if _, err := s.SearchFile(context.Background(), "stage", 5, "AGENTS.md"); err == nil {
		t.Fatal("a file outside the index answered without an error")
	}
	if s.Chunks() < 2 {
		t.Errorf("chunks %d", s.Chunks())
	}
	paths, err := s.SectionPaths(context.Background(), "stage explicit paths git add", 5, "CLAUDE.md")
	if err != nil || len(paths) == 0 || strings.Join(paths[0], "/") != "Rules/Staging" {
		t.Errorf("section paths %q %v", paths, err)
	}
}

// must fails the test on a setup error, so a test never passes over a fixture it could not write.
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
