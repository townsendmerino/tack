package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// completeTree writes files (a path ending in / is an empty directory) under a fresh root and returns a Completer.
func completeTree(t *testing.T, files ...string) *Completer {
	t.Helper()
	root := t.TempDir()
	for _, f := range files {
		p := filepath.Join(root, f)
		if strings.HasSuffix(f, "/") {
			os.MkdirAll(p, 0o755)
			continue
		}
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("x\n"), 0o644)
	}
	ws, err := NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	return NewCompleter(ws)
}

func paths(items []Completion) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.Path)
	}
	return out
}

// TestCompleteIsAFuzzySubsequenceMatchRankedByBasenameThenLength: a query matches as a subsequence of the path; a path
// whose base name matches ranks before one that matches only across the directories, then shorter before longer; kinds
// are file and dir, and paths are workspace-relative with forward slashes.
func TestCompleteIsAFuzzySubsequenceMatchRankedByBasenameThenLength(t *testing.T) {
	c := completeTree(t, "internal/tools/edit.go", "internal/tools/edit_test.go", "docs/edit/notes.md", "README.md", "cmd/tack/")
	got, err := c.Complete("edit", 20)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"docs/edit", "internal/tools/edit.go", "internal/tools/edit_test.go", "docs/edit/notes.md"}; !slices.Equal(paths(got), want) {
		t.Errorf("edit: %q, want %q", paths(got), want)
	}
	if got, _ := c.Complete("itg", 20); len(got) == 0 || got[0].Path != "internal/tools/edit.go" && !slices.Contains(paths(got), "internal/tools/edit.go") {
		t.Errorf("a subsequence across directories: %q", paths(got))
	}
	got, _ = c.Complete("tack", 20)
	if len(got) == 0 || got[0].Path != "cmd/tack" || got[0].Kind != "dir" {
		t.Errorf("a directory: %+v", got)
	}
	if got, _ := c.Complete("README", 20); len(got) != 1 || got[0].Kind != "file" {
		t.Errorf("a file: %+v", got)
	}
	if got, _ := c.Complete("zzz", 20); len(got) != 0 {
		t.Errorf("no match: %+v", got)
	}
	if got, _ := c.Complete("e", 2); len(got) != 2 {
		t.Errorf("limit 2: %d items", len(got))
	}
}

// TestCompleteLeavesOutWhatACompletionShouldNotOffer: .gitignore'd paths (root and nested, files and whole directories),
// secrets, version-control and dependency trees and symlinks are never listed; the read tools are not touched by it.
func TestCompleteLeavesOutWhatACompletionShouldNotOffer(t *testing.T) {
	c := completeTree(t, ".gitignore", "build/out.bin", "src/main.go", "src/gen/skip.go", "src/gen/.gitignore", "src/gen/keep.go",
		"app.log", ".env", ".git/config", "node_modules/x/index.js", "docs/notes.md")
	root := c.ws.Root()
	os.WriteFile(filepath.Join(root, ".gitignore"), []byte("build/\n*.log\n"), 0o644)
	os.WriteFile(filepath.Join(root, "src/gen/.gitignore"), []byte("skip.go\n"), 0o644)
	os.Symlink(filepath.Join(root, "docs"), filepath.Join(root, "link"))
	c = NewCompleter(c.ws)
	got, err := c.Complete("", 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range paths(got) {
		for _, bad := range []string{"build", "app.log", ".env", ".git/", "node_modules", "skip.go", "link"} {
			if strings.Contains(p, bad) {
				t.Errorf("%q is listed; %q must not be", p, bad)
			}
		}
	}
	for _, want := range []string{"src/main.go", "src/gen/keep.go", "docs/notes.md", ".gitignore"} {
		if !slices.Contains(paths(got), want) {
			t.Errorf("%q is missing from %q", want, paths(got))
		}
	}
	// The read tools still reach an ignored file asked for by name.
	if _, err := c.ws.Resolve("build/out.bin"); err != nil {
		t.Errorf("a read of an ignored path: %v", err)
	}
}

// TestCompleteIsFastOnABigTree: on 10,000 files a completion takes under 20 ms once the list is built, and the list is
// refreshed in the background after it goes stale, so a call after a change still answers at once.
func TestCompleteIsFastOnABigTree(t *testing.T) {
	root := t.TempDir()
	for i := range 10000 {
		p := filepath.Join(root, fmt.Sprintf("d%02d/e%02d/file%05d.go", i%50, i%7, i))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, nil, 0o644)
	}
	ws, _ := NewWorkspace(root)
	c := NewCompleter(ws)
	if _, err := c.Complete("f", 20); err != nil { // builds the list
		t.Fatal(err)
	}
	best := time.Hour
	for range 5 {
		start := time.Now()
		got, _ := c.Complete("d1e3f0", 20)
		if len(got) == 0 {
			t.Fatal("no match on the big tree")
		}
		best = min(best, time.Since(start))
	}
	if best > 20*time.Millisecond {
		t.Errorf("a completion took %v on 10,000 files; want under 20 ms", best)
	}
	os.WriteFile(filepath.Join(root, "brand_new_file.go"), nil, 0o644)
	c.stale() // the list is old now
	start := time.Now()
	c.Complete("brand", 20)
	if took := time.Since(start); took > 20*time.Millisecond {
		t.Errorf("a call on a stale list took %v; it answers from the list and refreshes in the background", took)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if got, _ := c.Complete("brand_new", 20); len(got) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the new file never appeared after the background refresh")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// stale makes the list old, as if completeRefresh had passed.
func (c *Completer) stale() {
	c.mu.Lock()
	c.builtAt = time.Now().Add(-time.Hour)
	c.mu.Unlock()
}
