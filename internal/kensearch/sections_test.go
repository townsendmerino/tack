package kensearch

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/townsendmerino/tack/internal/window"
)

// edgeMarkdown has the shapes a heading parser gets wrong: a fenced block holding a "#" line, a tilde fence, closing
// hashes, a heading of hashes alone, an empty preamble line, a deeper level after a shallower one, and a heading
// straight after a fence.
const edgeMarkdown = "\n# Title #\n\ntext\n\n```sh\n# a comment, not a heading\n```\n## Rules\n\n~~~\n## still code\n~~~\n" +
	"#### Deep\n\nx\n\n### ###\n\n## Back up ##\n<!-- tack:on bash:git -->\nend\n"

// TestParseSectionsAgreesWithKen: window.ParseSections splits tack's own instruction files and edgeMarkdown where ken's
// Sections does, heading paths included, so a hit ken finds in a section names the section tack loads (K9).
func TestParseSectionsAgreesWithKen(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{"edge.md": edgeMarkdown}
	for _, p := range []string{"CLAUDE.md", "docs/design/p1-notes.md"} {
		b, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(p)))
		if err != nil {
			t.Fatal(err)
		}
		files[filepath.Base(p)] = string(b)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s, err := Open(context.Background(), dir, Options{Mode: "bm25"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for name, content := range files {
		kens, err := s.ws.Sections(context.Background(), name)
		if err != nil {
			t.Fatal(err)
		}
		ours := window.ParseSections(content)
		if len(ours) != len(kens) {
			t.Errorf("%s: %d sections, ken has %d", name, len(ours), len(kens))
		}
		for i := range min(len(ours), len(kens)) {
			o, k := ours[i], kens[i]
			if !slices.Equal(o.HeadingPath, k.HeadingPath) || o.Level != k.Level || o.StartLine != k.StartLine || o.EndLine != k.EndLine {
				t.Errorf("%s section %d: ours %q level %d lines %d–%d; ken's %q level %d lines %d–%d", name, i, o.HeadingPath,
					o.Level, o.StartLine, o.EndLine, k.HeadingPath, k.Level, k.StartLine, k.EndLine)
				break
			}
		}
	}
}
