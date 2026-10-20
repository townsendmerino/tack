package loop

import (
	"fmt"
	"strings"
)

// Result notes (Config.ResultNotes; e1-calibration-preregistration-2026-10-18.md §4.4) say what to do after an empty
// search or an unchanged repeat. Each is one line, appended to the result after a blank line in the fixed form
// noteOpen…"]", so StripNotes removes exactly them.
const noteOpen = "\n\n[tack: "

// Note texts. GlobEmptyNote is the owner's wording (2026-10-19).
const (
	GlobEmptyNote = "glob matches file names only; to find a name inside files, use grep."
	GrepEmptyNote = "nothing matched. Search for a shorter part of the name, drop regex escapes, or widen the path."
)

// resultNotes is the notes for one call's result: the empty-search note for tool when empty, and the unchanged-repeat
// note when the call has repeated with the same result (repeats, as RepeatGuard counts them). stopRepeats adds when the
// repeat rule will end the turn.
func resultNotes(tool string, empty bool, repeats int, stopRepeats bool) string {
	var b strings.Builder
	if empty {
		switch tool {
		case "glob":
			b.WriteString(noteOpen + GlobEmptyNote + "]")
		case "grep":
			b.WriteString(noteOpen + GrepEmptyNote + "]")
		}
	}
	if repeats > 0 {
		limit := ""
		if stopRepeats {
			limit = fmt.Sprintf(" Repeat %d ends the turn.", RepeatLimit)
		}
		fmt.Fprintf(&b, "%sunchanged: this call repeats an earlier one exactly and got the same result (repeat %d).%s Change the call, or finish.]",
			noteOpen, repeats, limit)
	}
	return b.String()
}

// StripNotes is result without the notes resultNotes appended to it: what the tool returned, as RepeatGuard compares
// it. A result with no notes is returned unchanged.
func StripNotes(result string) string {
	for strings.HasSuffix(result, "]") {
		i := strings.LastIndex(result, noteOpen)
		if i < 0 || strings.Contains(result[i+len(noteOpen):], "\n") {
			break
		}
		result = result[:i]
	}
	return result
}
