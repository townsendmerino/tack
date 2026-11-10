package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
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
	if got, _ := c.Complete("itg", 20); !slices.Contains(paths(got), "internal/tools/edit.go") || slices.Contains(paths(got), "README.md") {
		t.Errorf("a subsequence across directories: %q", paths(got))
	}
	if got, _ := c.Complete("日", 20); len(got) != 0 {
		t.Errorf("a rune with no match: %q", paths(got))
	}
	if got, err := c.Complete("e", -1); err != nil || len(got) != 0 {
		t.Errorf("a negative limit: %q, %v", paths(got), err)
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
	release := make(chan struct{})
	c.buildHook = func() { <-release } // the refresh walk is held until the test lets it go
	c.stale()
	if got, _ := c.Complete("brand", 20); len(got) != 0 {
		t.Errorf("a call on a stale list saw the new file before the refresh finished: %q", paths(got))
	}
	if got, _ := c.Complete("file00001", 20); len(got) == 0 {
		t.Error("a call during the refresh did not answer from the old list")
	}
	close(release)
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

// TestCompleteBuildsOnceEvenWhenNothingIsOffered: an empty workspace, and one whose every path is ignored, keep their
// (empty) list; no later call walks the disk again, and concurrent first calls share one walk.
func TestCompleteBuildsOnceEvenWhenNothingIsOffered(t *testing.T) {
	for name, files := range map[string][]string{"empty": nil, "all ignored": {".gitignore", "a.go", "b/c.go"}} {
		c := completeTree(t, files...)
		if name == "all ignored" {
			os.WriteFile(filepath.Join(c.ws.Root(), ".gitignore"), []byte("*\n"), 0o644)
		}
		walks := 0
		c.buildHook = func() { walks++ }
		var wg sync.WaitGroup
		for range 8 {
			wg.Add(1)
			go func() { defer wg.Done(); c.Complete("a", 20) }()
		}
		wg.Wait()
		for range 3 {
			c.Complete("a", 20)
		}
		if walks != 1 {
			t.Errorf("%s: %d walks; want 1", name, walks)
		}
	}
}

// TestCompleteReadsIgnoreFilesParentFirst: a directory that sorts before ".gitignore" (.github here) is walked before
// the root's .gitignore in lexical order, but the root's patterns still come first, so the nested .gitignore's negation
// wins; and an ignored directory is not entered at all (its .gitignore is never read).
func TestCompleteReadsIgnoreFilesParentFirst(t *testing.T) {
	c := completeTree(t, ".github/.gitignore", ".github/keep.md", ".github/drop.md", "target/deep/x.go", "main.go")
	root := c.ws.Root()
	os.WriteFile(filepath.Join(root, ".gitignore"), []byte(".github/*.md\ntarget/\n"), 0o644)
	os.WriteFile(filepath.Join(root, ".github/.gitignore"), []byte("!keep.md\n"), 0o644)
	got, _ := c.Complete("", 50)
	if !slices.Contains(paths(got), ".github/keep.md") || slices.Contains(paths(got), ".github/drop.md") {
		t.Errorf("nested negation lost to the parent's pattern: %q", paths(got))
	}
	for _, p := range paths(got) {
		if strings.HasPrefix(p, "target") {
			t.Errorf("an ignored directory's path %q is listed", p)
		}
	}
	walked := false
	c2 := completeTree(t, "target/deep/x.go", "main.go")
	os.WriteFile(filepath.Join(c2.ws.Root(), ".gitignore"), []byte("target/\n"), 0o644)
	c2.entryHook = func(rel string) {
		if strings.HasPrefix(rel, "target/") {
			walked = true
		}
	}
	c2.Complete("", 50)
	if walked {
		t.Error("the walk entered an ignored directory")
	}
}

// TestCompleteFixtureMatchesTheCompleter: proto/fixtures/complete.jsonl records answers; its first completion, over the
// tree it describes, is what the real Completer returns, in order.
func TestCompleteFixtureMatchesTheCompleter(t *testing.T) {
	b, err := os.ReadFile("../../proto/fixtures/complete.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	var req struct {
		Msg struct {
			Params struct {
				Query string `json:"query"`
				Limit int    `json:"limit"`
			} `json:"params"`
			Result struct {
				Items []struct{ Path, Kind string } `json:"items"`
			} `json:"result"`
		} `json:"msg"`
	}
	var query string
	var limit int
	var want []string
	for i, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if err := json.Unmarshal([]byte(l), &req); err != nil {
			t.Fatal(err)
		}
		switch i {
		case 2:
			query, limit = req.Msg.Params.Query, req.Msg.Params.Limit
		case 3:
			for _, it := range req.Msg.Result.Items {
				want = append(want, it.Path+" "+it.Kind)
			}
		}
	}
	c := completeTree(t, "internal/tools/edit.go", "internal/tools/edit_test.go", "docs/edit/notes.md", "README.md")
	got, _ := c.Complete(query, limit)
	var have []string
	for _, it := range got {
		have = append(have, it.Path+" "+it.Kind)
	}
	if !slices.Equal(have, want) {
		t.Errorf("fixture %q; the Completer returns %q", want, have)
	}
}
