package main

import (
	"bytes"
	"context"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/townsendmerino/tack/internal/kensearch"
)

// TestCheckPassesOnASmallIndex: under 5,000 chunks SearchFiles is the exact answer.
func TestCheckPassesOnASmallIndex(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("# notes\n\n## Testing\n\nRun the tests.\n\n## Staging\n\nStage and commit.\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n\n// run the tests\nfunc A() {}\n"), 0o644)
	var out bytes.Buffer
	if code := run([]string{"-root", dir, "-files", "./CLAUDE.md"}, &out, &bytes.Buffer{}); code != 0 || !strings.Contains(out.String(), "VERDICT: PASS") {
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

// dropping drops every hit SearchFile returns past the first, as ken's capped SearchFiles did past 5,000 candidates.
type dropping struct{ fileSearcher }

func (d dropping) SearchFile(ctx context.Context, q string, k int, f string) ([]kensearch.SectionHit, error) {
	hits, err := d.fileSearcher.SearchFile(ctx, q, k, f)
	return hits[:min(len(hits), 1)], err
}

// TestCheckFailsWhenHitsAreMissing: a SearchFile that loses hits fails the check, so the gate can go red.
func TestCheckFailsWhenHitsAreMissing(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("# notes\n\n## Testing\n\nRun the tests.\n\n## More testing\n\nRun the tests again.\n"), 0o644)
	wrap = func(s fileSearcher) fileSearcher { return dropping{s} }
	t.Cleanup(func() { wrap = func(s fileSearcher) fileSearcher { return s } })
	var out bytes.Buffer
	if code := run([]string{"-root", dir, "-files", "CLAUDE.md"}, &out, &bytes.Buffer{}); code != 1 || !strings.Contains(out.String(), "VERDICT: FAIL") ||
		!strings.Contains(out.String(), "differs:") {
		t.Fatalf("exit %d:\n%s", code, out.String())
	}
}

// timeoutArgs is the arguments of every treesitter.WithParseTimeoutMicros call in a Go file, as written.
func timeoutArgs(t *testing.T, path string) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) != 1 {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "WithParseTimeoutMicros" {
			var b strings.Builder
			printer.Fprint(&b, fset, call.Args[0])
			out = append(out, b.String())
		}
		return true
	})
	return out
}

// TestMeasurementChunkerHasNoTimeout: the measurement tool registers the chunker with no parse timeout, so its index
// does not depend on the box's load, and the live path keeps its 1 s guard. Both are read from the code that
// registers them: the call's argument, not a nearby string.
func TestMeasurementChunkerHasNoTimeout(t *testing.T) {
	if got := timeoutArgs(t, "main.go"); len(got) != 1 || got[0] != "parseTimeoutMicros" || parseTimeoutMicros != 0 {
		t.Errorf("searchfilescheck registers the chunker with %q (parseTimeoutMicros %d); want parseTimeoutMicros, 0", got, parseTimeoutMicros)
	}
	if got := timeoutArgs(t, filepath.Join("..", "..", "cmd", "tack", "ken.go")); len(got) != 1 || got[0] != "1_000_000" {
		t.Errorf("cmd/tack/ken.go registers the chunker with %q; want the 1 s guard, 1_000_000", got)
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

// TestCheckSkipsEmptyEntriesAndRepeats: an empty -files entry names no file, and a heading the file repeats is queried
// once.
func TestCheckSkipsEmptyEntriesAndRepeats(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("# notes\n\n## Testing\n\nRun the tests.\n\n## Testing\n\nAgain.\n"), 0o644)
	var out bytes.Buffer
	if code := run([]string{"-root", dir, "-files", "CLAUDE.md,,"}, &out, &bytes.Buffer{}); code != 0 || !strings.Contains(out.String(), "VERDICT: PASS") ||
		!strings.Contains(out.String(), "CLAUDE.md k=5: 12 queries") {
		t.Fatalf("exit %d:\n%s", code, out.String())
	}
}
