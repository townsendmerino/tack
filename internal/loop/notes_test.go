package loop

import (
	"context"
	"strings"
	"testing"

	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
)

func TestStripNotesRecoversTheToolsOutput(t *testing.T) {
	for _, raw := range []string{"no files match x", "a.go\nb.go\n", "ends in a bracket]", "", "[tack: inside text]\nmore", "out\n\n[tack: not a note\nstill output]"} {
		for _, notes := range []string{"", resultNotes("glob", true, 0, true), resultNotes("grep", true, 3, true), resultNotes("read", false, 2, false)} {
			if got := StripNotes(raw + notes); got != raw {
				t.Errorf("StripNotes(%q + notes %q) = %q", raw, notes, got)
			}
		}
	}
}

func TestResultNotesSayWhatToDo(t *testing.T) {
	if n := resultNotes("glob", true, 0, false); !strings.Contains(n, GlobEmptyNote) || strings.Contains(n, "unchanged") {
		t.Errorf("empty glob: %q", n)
	}
	if n := resultNotes("grep", true, 0, false); !strings.Contains(n, GrepEmptyNote) {
		t.Errorf("empty grep: %q", n)
	}
	if n := resultNotes("read", false, 0, true); n != "" {
		t.Errorf("a first, non-empty call got %q", n)
	}
	if n := resultNotes("grep", false, 3, true); !strings.Contains(n, "(repeat 3). Repeat 8 ends the turn.") {
		t.Errorf("a repeat with the stop rule on: %q", n)
	}
	if n := resultNotes("grep", false, 3, false); strings.Contains(n, "ends the turn") {
		t.Errorf("a repeat with the stop rule off promises a stop: %q", n)
	}
}

// TestResultNotesKeepTheStopRule: through the loop with both settings on, an empty grep carries its note, each repeat
// its count, and the repeat rule still ends the turn at the same call as with notes off: the notes' changing counts do
// not make a repeat look changed. Off, results carry no notes.
func TestResultNotesKeepTheStopRule(t *testing.T) {
	script := func() []goinfertest.Reply {
		var rs []goinfertest.Reply
		for i := 0; i <= RepeatLimit; i++ {
			rs = append(rs, goinfertest.Calls("", "g", "grep", `{"pattern":"nothing matches this"}`))
		}
		return append(rs, goinfertest.Says("gave up"))
	}
	b := testBudget
	b.MaxRequests, b.MaxToolCalls = 20, 20
	on := newHarness(t, b, script()...)
	on.sess.cfg.StopRepeats, on.sess.cfg.ResultNotes = true, true
	if stop, err := on.sess.Turn(context.Background(), "find it"); err != nil || stop != proto.StopRepeatLoop {
		t.Fatalf("notes on: stop %q, %v", stop, err)
	}
	fins := on.payloads(proto.EvToolFinished)
	if len(fins) != RepeatLimit+1 {
		t.Fatalf("notes on: %d calls ran, want %d", len(fins), RepeatLimit+1)
	}
	first, last := fins[0].(proto.ToolFinished).Result, fins[RepeatLimit].(proto.ToolFinished).Result
	if !strings.Contains(first, GrepEmptyNote) || strings.Contains(first, "unchanged") ||
		!strings.Contains(last, "(repeat 8)") || StripNotes(last) != "no matches for nothing matches this" {
		t.Fatalf("first result %q\nlast %q", first, last)
	}
	on.checkRebuild(t)
	off := newHarness(t, b, script()...)
	off.sess.cfg.StopRepeats = true
	off.sess.Turn(context.Background(), "find it")
	if r := off.payloads(proto.EvToolFinished)[0].(proto.ToolFinished).Result; r != "no matches for nothing matches this" {
		t.Fatalf("notes off: %q", r)
	}
}
