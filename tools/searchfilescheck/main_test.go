package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCheckPassesOnASmallIndex: under 5,000 chunks SearchFiles is the exact answer.
func TestCheckPassesOnASmallIndex(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("# notes\n\n## Testing\n\nRun the tests.\n\n## Staging\n\nStage and commit.\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n\n// run the tests\nfunc A() {}\n"), 0o644)
	var out bytes.Buffer
	if code := run([]string{"-root", dir, "-files", "./CLAUDE.md"}, &out, &bytes.Buffer{}); code != 0 || !strings.Contains(out.String(), "VERDICT: PASS") ||
		strings.Contains(out.String(), "said inexact") {
		t.Fatalf("exit %d:\n%s", code, out.String())
	}
}

// TestCheckFailsOnAnUnreadableFile: a file that is there but cannot be read fails the check instead of being skipped.
func TestCheckFailsOnAnUnreadableFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("# notes\n\n## Testing\n\nRun the tests.\n"), 0o644)
	os.MkdirAll(filepath.Join(dir, "AGENTS.md"), 0o755)
	var out bytes.Buffer
	if code := run([]string{"-root", dir, "-files", "CLAUDE.md,AGENTS.md"}, &out, &bytes.Buffer{}); code != 1 || !strings.Contains(out.String(), "VERDICT: FAIL") {
		t.Fatalf("exit %d:\n%s", code, out.String())
	}
}

// TestCheckFailsPastTheCap: an index past ken's 5,000-candidate cap whose instruction-file hit ranks below it gives a
// SearchFiles answer that is not exact, and the check says FAIL. Every Go file repeats the query word, so each outranks
// the instruction file's one, in its heading.
func TestCheckFailsPastTheCap(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("# notes\n\n## Zebra\n\nonce, among many other words of prose here.\n"), 0o644)
	for i := range 5100 {
		os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%04d.go", i)), []byte(fmt.Sprintf("package p\n\n// zebra zebra zebra\nfunc F%d() {}\n", i)), 0o644)
	}
	var out bytes.Buffer
	code := run([]string{"-root", dir, "-files", "CLAUDE.md"}, &out, &bytes.Buffer{})
	if code != 1 || !strings.Contains(out.String(), "VERDICT: FAIL") || !strings.Contains(out.String(), `differs: "Zebra"`) ||
		!strings.Contains(out.String(), "SearchFile said inexact") {
		t.Fatalf("exit %d:\n%s", code, out.String())
	}
}

func TestCheckWithNoFileIsInconclusive(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o644)
	var out bytes.Buffer
	if code := run([]string{"-root", dir, "-files", "CLAUDE.md"}, &out, &bytes.Buffer{}); code != 2 || !strings.Contains(out.String(), "VERDICT: INCONCLUSIVE") {
		t.Fatalf("exit %d:\n%s", code, out.String())
	}
}
