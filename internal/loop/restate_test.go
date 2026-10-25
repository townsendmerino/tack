package loop

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
)

// TestRestateTaskQuotesTheTurnsTask: with RestateTask, the first done with no change is answered with the turn's own
// text quoted back, without the workspace facts the first message carries; the second done still ends the turn. A
// later turn quotes its own text.
func TestRestateTaskQuotesTheTurnsTask(t *testing.T) {
	const task = "Rename the window flag to -ctx in cmd/serve.\nKeep the old name as an alias."
	h := editHarness(t, func() (bool, error) { return false, nil },
		goinfertest.Calls("", "d1", "done", `{"reason":"looks fine"}`), goinfertest.Calls("", "d2", "done", `{"reason":"none needed"}`),
		goinfertest.Calls("", "d3", "done", `{"reason":"looks fine"}`), goinfertest.Calls("", "d4", "done", `{"reason":"none needed"}`))
	cfg := h.sess.cfg
	cfg.RestateTask = true
	h.sess = New(cfg)
	if stop, err := h.sess.Turn(context.Background(), task); err != nil || stop != proto.StopDone {
		t.Fatalf("stop %q %v", stop, err)
	}
	r := doneResults(h)
	if len(r) != 2 || r[0] != DoneNoChangeRestated(task) || r[1] != "done" {
		t.Fatalf("results %q", r)
	}
	want := "no files changed; the task asks for a change. The task was:\n\n" + task +
		"\n\nMake the change it asks for, or call done again with the reason none is needed."
	if r[0] != want || strings.Contains(r[0], "Repository:") || !IsDoneNoChange(r[0]) {
		t.Fatalf("refusal %q", r[0])
	}
	if stop, err := h.sess.Turn(context.Background(), "Now the second thing."); err != nil || stop != proto.StopDone {
		t.Fatalf("second turn: stop %q %v", stop, err)
	}
	if r := doneResults(h); len(r) != 4 || r[2] != DoneNoChangeRestated("Now the second thing.") {
		t.Fatalf("second turn's results %q", r)
	}
	h.checkRebuild(t)
}

// TestRestateTaskOffIsUnchanged: without the setting the refusal is DoneNoChange, byte for byte.
func TestRestateTaskOffIsUnchanged(t *testing.T) {
	h := editHarness(t, func() (bool, error) { return false, nil },
		goinfertest.Calls("", "d1", "done", `{"reason":"x"}`), goinfertest.Calls("", "d2", "done", `{"reason":"y"}`))
	h.sess.Turn(context.Background(), "Rename the flag.")
	if r := doneResults(h); r[0] != DoneNoChange {
		t.Fatalf("refusal %q", r[0])
	}
}

func TestDoneNoChangeRestatedCapsTheTask(t *testing.T) {
	line := strings.Repeat("é", 99) + "\n" // 199 bytes a line
	long := strings.Repeat(line, 30)
	r := DoneNoChangeRestated(long)
	task := strings.TrimSuffix(strings.TrimPrefix(r, "no files changed; the task asks for a change. The task was:\n\n"),
		"\n\nMake the change it asks for, or call done again with the reason none is needed.")
	if !strings.HasSuffix(task, "\n…") || len(task) > MaxRestatedTask+len("\n…") || !utf8.ValidString(r) {
		t.Fatalf("capped task: %d bytes, ends %q", len(task), task[len(task)-10:])
	}
	if !strings.HasPrefix(long, strings.TrimSuffix(task, "\n…")) {
		t.Fatal("the cut task is not a prefix of the task")
	}
	if one := DoneNoChangeRestated(strings.Repeat("x", 3000)); !utf8.ValidString(one) || !strings.Contains(one, "\n…") {
		t.Fatal("a task with no line break was not cut")
	}
	if !IsDoneNoChange(DoneNoChange) || IsDoneNoChange("done") || IsDoneNoChange(VerifyRefusal) {
		t.Fatal("IsDoneNoChange")
	}
}
