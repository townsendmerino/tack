package core

import (
	"strings"
	"testing"
)

func TestInstructionHeadFirstSectionAndOutline(t *testing.T) {
	titleOnly := "# repo notes\n\n## Rules\n\nStage explicit paths.\n\n## Tests\n\nRun go test.\n\n```\n# not a heading\n```\n\n## Releases\n\nTag it.\n"
	h := instructionHead("CLAUDE.md", titleOnly, 16384)
	if !strings.Contains(h, "## Rules\n\nStage explicit paths.") || strings.Contains(h, "Run go test.") ||
		!strings.Contains(h, "Other sections of CLAUDE.md, to read when you need one:\n## Tests\n## Releases") || strings.Contains(h, "not a heading") {
		t.Errorf("a title-only opening takes in the next section, and the outline lists the rest:\n%s", h)
	}
	withIntro := "# repo\n\nIntro text.\n\n## Rules\n\nStage explicit paths.\n"
	if h := instructionHead("CLAUDE.md", withIntro, 16384); !strings.Contains(h, "Intro text.") || strings.Contains(h, "Stage explicit") ||
		!strings.Contains(h, "## Rules") {
		t.Errorf("a file with an introduction keeps just the introduction:\n%s", h)
	}
}

// TestInstructionHeadStaysUnderItsCap: a head over its cap loses outline headings from the end first, then lines of
// its first section, and says each cut; it never exceeds the cap.
func TestInstructionHeadStaysUnderItsCap(t *testing.T) {
	var b strings.Builder
	b.WriteString("# notes\n\n")
	for range 400 {
		b.WriteString("a long line of the first section, to fill it up\n")
	}
	for range 300 {
		b.WriteString("\n## heading number " + strings.Repeat("x", 20) + "\n\nbody\n")
	}
	window := 4096 // cap: 614 tokens, 1,842 bytes
	h := instructionHead("CLAUDE.md", b.String(), window)
	capBytes := window * headShare / 100 * headBytesPerToken
	if len(h) > capBytes || !strings.Contains(h, "more headings]") || !strings.Contains(h, "cut to fit the window") {
		t.Fatalf("head of %d bytes against a cap of %d:\n%s", len(h), capBytes, h)
	}
}
