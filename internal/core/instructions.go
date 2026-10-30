package core

import (
	"context"
	"fmt"
	"strings"

	"github.com/townsendmerino/tack/internal/loop"
	"github.com/townsendmerino/tack/internal/models"
)

// The budgeted head of an instruction file (Options.InstructionsHead; e1-calibration-preregistration-2026-10-18.md
// §12.12): the file's first section and an outline of its other headings, capped at headShare of the served window. It
// stands in for DESIGN §2.3's section loading until that is built.

// headShare is the share of the window the head may take, in percent.
const headShare = 15

// headBytesPerToken converts the token cap to bytes. The calibration tasks' CLAUDE.md files measure 3.6–3.8 bytes per
// token with the Qwen tokenizer (e1-calibration-preregistration-2026-10-18.md §12.11's table); 3 keeps the head under its
// cap for denser text.
const headBytesPerToken = 3

// headTaskLabel introduces the task statement after the head. The head comes first and the statement last, so the
// message ends with the task: placed after the statement, the head was read as the task
// (docs/measurements/e1-calibration-c6-42-stopped-2026-10-20.md).
const headTaskLabel = "Task: "

// instructionHead is name's content cut to its budgeted head for a window of window tokens, framed as reference notes:
// its first section, which runs to the first heading after some body text (so a file that opens with only its title
// takes in the section after it), then the headings of the rest as an outline. When the two exceed the cap, the outline
// is cut from its end first, then the first section at a line, and each cut is said.
func instructionHead(name, content string, window int) string {
	capBytes := window * headShare / 100 * headBytesPerToken
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	var first, outline []string
	body, inFirst, fence := false, true, false // body: the first section has text beyond headings
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "```") {
			fence = !fence
		}
		isHeading := !fence && strings.HasPrefix(l, "#") && strings.HasPrefix(strings.TrimLeft(l, "#"), " ")
		if isHeading && body {
			inFirst = false
		}
		switch {
		case inFirst:
			first = append(first, l)
			if !isHeading && strings.TrimSpace(l) != "" {
				body = true
			}
		case isHeading:
			outline = append(outline, l)
		}
	}
	intro := fmt.Sprintf("Project notes from %s, for reference:\n\n", name)
	build := func(first, outline []string, cutFirst bool, moreHeadings int) string {
		var b strings.Builder
		b.WriteString(intro)
		b.WriteString(strings.Join(first, "\n"))
		if cutFirst {
			b.WriteString("\n[… the rest of this section is cut to fit the window; read " + name + " for it]")
		}
		if len(outline) > 0 || moreHeadings > 0 {
			b.WriteString("\n\nOther sections of " + name + ", to read when you need one:\n")
			b.WriteString(strings.Join(outline, "\n"))
			if moreHeadings > 0 {
				fmt.Fprintf(&b, "\n[… %d more headings]", moreHeadings)
			}
		}
		return b.String()
	}
	more := 0
	for len(outline) > 0 && len(build(first, outline, false, more)) > capBytes {
		outline, more = outline[:len(outline)-1], more+1
	}
	cut := false
	for len(first) > 1 && len(build(first, outline, cut, more)) > capBytes {
		first, cut = first[:len(first)-1], true
	}
	return build(first, outline, cut, more)
}

// servedWindow is src's served window, asking the server when it is not yet known, or loop.DesignWindow when the server
// does not say.
func servedWindow(ctx context.Context, src models.Source) int {
	if w := src.Window(); w > 0 {
		return w
	}
	if _, err := src.Ensure(ctx); err == nil && src.Window() > 0 {
		return src.Window()
	}
	return loop.DesignWindow
}

// InstructionHead is the head instructions_head gives a session in root for a window of window tokens: the first
// instruction file a session would read there, cut by instructionHead. It is "" when root has none. Measurement tools
// call it to see what a run was shown (e1-calibration-preregistration-2026-10-18.md §13.3).
func InstructionHead(root string, window int) string {
	name, content := readInstructionFile(root)
	if name == "" {
		return ""
	}
	return instructionHead(name, content, window)
}
