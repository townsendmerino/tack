package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/townsendmerino/tack/internal/eval/pilot"
)

// §13.1's named code: qualified identifiers from backquoted spans only, (*T).m read as T.m, keywords, predeclared
// names and single letters dropped, each once in order.
func TestIdentifiers(t *testing.T) {
	got := identifiers("Fix `(*loadedModel).prepare(sm sampling, promptIDs []int)` and `pull.Select(files []File, n int)`; " +
		"not prose like Select, and `pull.Select` again, `x`, `nil`.")
	want := "loadedModel.prepare sm sampling promptIDs pull.Select files File"
	if strings.Join(got, " ") != want {
		t.Fatalf("identifiers %v, want %s", got, want)
	}
}

// §13.1's packing: whole items in order while they fit the budget at 3 bytes a token; one that does not fit is skipped
// and packing goes on; one whose lines are all packed already is skipped.
func TestPack(t *testing.T) {
	root := t.TempDir()
	var b strings.Builder
	for range 40 {
		b.WriteString("abcdefghijklmnopqrstuvwxyz\n") // 27 bytes a line, 9 tokens
	}
	os.WriteFile(filepath.Join(root, "a.go"), []byte(b.String()), 0o644)
	items := []pilot.LineSpan{
		{File: "a.go", Start: 1, End: 2},   // 18 tokens: packed
		{File: "a.go", Start: 3, End: 30},  // 252 tokens: over what is left of 100, skipped
		{File: "a.go", Start: 1, End: 1},   // already packed, skipped
		{File: "a.go", Start: 31, End: 33}, // 27 tokens: packed
	}
	got := pack(root, items, 100)
	if len(got) != 2 || got[0].Start != 1 || got[1].Start != 31 {
		t.Fatalf("packed %+v, want lines 1-2 and 31-33", got)
	}
}
