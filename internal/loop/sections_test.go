package loop

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
)

// fakeSections brings in a note for the first call it is asked about, and records what the loop asks and tells it.
type fakeSections struct {
	asked  []string
	masked []string
}

func (f *fakeSections) ForCall(id, tool string, _ json.RawMessage) string {
	f.asked = append(f.asked, id+":"+tool)
	if len(f.asked) == 1 {
		return "\n\n[Project rules from CLAUDE.md for this call:]\n\n## Rules\n\nStage explicit paths."
	}
	return ""
}

func (f *fakeSections) Masked(ids []string) { f.masked = append(f.masked, ids...) }

// TestSectionsFollowTheResultAndLeaveWithIt: a call that ran is asked about once, its sections follow its result in
// the next request and the log, and a compaction that stubs that result says so; the rebuilt conversation matches.
func TestSectionsFollowTheResultAndLeaveWithIt(t *testing.T) {
	h := maskHarness(t, true, 120,
		goinfertest.Calls("", "t1", "read", bigRead), goinfertest.Calls("", "t2", "read", bigRead), goinfertest.Says("ok"))
	f := &fakeSections{}
	cfg := h.sess.cfg
	cfg.Sections = f
	h.sess = New(cfg)
	if _, err := h.sess.Turn(context.Background(), "read it twice"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.asked, []string{"t1:read", "t2:read"}) {
		t.Errorf("asked %q", f.asked)
	}
	if second := string(h.script.Requests[1]); !strings.Contains(second, "Stage explicit paths.") {
		t.Errorf("request 2 lacks t1's sections")
	}
	if r := doneResults(h); len(r) != 2 || !strings.HasSuffix(r[0], "Stage explicit paths.") || strings.Contains(r[1], "Stage") {
		t.Errorf("results %q", r)
	}
	if !slices.Equal(f.masked, []string{"t1"}) {
		t.Errorf("masked %q, want t1's result", f.masked)
	}
	h.checkRebuild(t)
}

// TestSectionsOnlyForCallsThatRan: a call answered without running (a repeat of a failed call) brings nothing in.
func TestSectionsOnlyForCallsThatRan(t *testing.T) {
	missing := `{"path":"no/such/file.go"}`
	h := newHarness(t, Budget{}, goinfertest.Calls("", "t1", "read", missing), goinfertest.Calls("", "t2", "read", missing),
		goinfertest.Says("ok"))
	f := &fakeSections{}
	cfg := h.sess.cfg
	cfg.Sections = f
	h.sess = New(cfg)
	if _, err := h.sess.Turn(context.Background(), "read it"); err != nil {
		t.Fatal(err)
	}
	if r := doneResults(h); len(r) != 2 || r[1] != RepeatedCall {
		t.Fatalf("results %q", r)
	}
	if !slices.Equal(f.asked, []string{"t1:read"}) {
		t.Errorf("asked %q, want t1 alone", f.asked)
	}
}

// bigSections brings in a note far bigger than any window's room.
type bigSections struct{ fakeSections }

func (f *bigSections) ForCall(id, tool string, _ json.RawMessage) string {
	f.asked = append(f.asked, id+":"+tool)
	return "\n\n[Project rules from CLAUDE.md for this call:]\n\n" + strings.Repeat("rule ", 200_000)
}

// TestSectionsThatDoNotFitTheRoomAreRolledBack: under fit_window, a note that does not fit the room left is not added,
// and the loop tells the sections with Masked that the call's result does not carry it.
func TestSectionsThatDoNotFitTheRoomAreRolledBack(t *testing.T) {
	h := newHarness(t, Budget{}, goinfertest.Calls("", "t1", "read", `{"path":"mcp/token_budget.go","offset":3,"limit":1}`),
		goinfertest.Says("ok"))
	f := &bigSections{}
	cfg := h.sess.cfg
	cfg.Sections, cfg.FitWindow = f, true
	h.sess = New(cfg)
	if _, err := h.sess.Turn(context.Background(), "read it"); err != nil {
		t.Fatal(err)
	}
	if r := doneResults(h); len(r) != 1 || strings.Contains(r[0], "rule rule") {
		t.Fatalf("the oversized note reached the result: %.200q", r)
	}
	if !slices.Equal(f.masked, []string{"t1"}) {
		t.Errorf("masked %q, want the rolled-back t1", f.masked)
	}
	h.checkRebuild(t)
}
