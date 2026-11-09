package loop

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
)

// summaryHarness runs a turn of four reads, then a fifth, with Summarize on and a window every prompt is past. The
// summarizer's call goes to the same scripted server, so its reply sits where the summary runs: before request 5, when
// four responses are open. The prompt the server reports never grows, so no second summary is due (regrown).
func summaryHarness(t *testing.T) *harness {
	t.Helper()
	h := maskHarness(t, false, 120,
		goinfertest.Calls("", "t1", "read", bigRead), goinfertest.Calls("", "t2", "read", bigRead),
		goinfertest.Calls("", "t3", "read", bigRead), goinfertest.Calls("", "t4", "read", bigRead),
		goinfertest.Says(body("read big.txt once")), // the summary before request 5
		goinfertest.Calls("", "t5", "read", bigRead),
		goinfertest.Says("done"))
	h.sess.cfg.Summarize = true
	h.sess = New(h.sess.cfg)
	if _, err := h.sess.Turn(context.Background(), "read big.txt five times"); err != nil {
		t.Fatal(err)
	}
	return h
}

// TestALiveSummaryRebuildsByteForByte: a summary made live is recorded so that the log rebuilds the same request, byte
// for byte, and the summary message keeps the workspace preamble and the task word for word.
func TestALiveSummaryRebuildsByteForByte(t *testing.T) {
	h := summaryHarness(t)
	var comps []proto.Compaction
	for _, p := range h.payloads(proto.EvCompaction) {
		comps = append(comps, p.(proto.Compaction))
	}
	if len(comps) != 1 || comps[0].Trigger != "summary" || comps[0].Request != 5 ||
		comps[0].After >= comps[0].Before {
		t.Fatalf("compactions %+v", comps)
	}
	first := comps[0].Summary
	if !strings.HasPrefix(first, "Repository: ") || !strings.Contains(first, summaryFrame+"## Task\n\nread big.txt five times") ||
		!strings.Contains(first, body("read big.txt once")) || !strings.Contains(first, "## Anchors (exact)\n\nPaths: big.txt") {
		t.Errorf("the summary message:\n%s", first)
	}
	var asked int
	for _, r := range h.script.Requests {
		if strings.Contains(string(r), "Previous summary:") {
			asked++
		}
	}
	if asked != 1 {
		t.Errorf("%d summarizer calls, want 1", asked)
	}
	h.checkRebuild(t)
	evs, _ := h.st.Events("s_test", 0)
	live := h.sess.Conversation()
	re, err := Rebuild(evs, h.ws.Root(), *live)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(live.Request())
	b, _ := json.Marshal(re.Request())
	if string(a) != string(b) {
		t.Errorf("the rebuilt request's bytes differ from the live one's")
	}
}

// TestAResumedSessionSummarizesAsTheLiveOne: a session resumed from a log with summaries holds the same summary state as
// the live one, so its next summary cuts and updates as the live one's would.
func TestAResumedSessionSummarizesAsTheLiveOne(t *testing.T) {
	h := summaryHarness(t)
	evs, _ := h.st.Events("s_test", 0)
	r, err := Resume(h.sess.cfg, evs)
	if err != nil {
		t.Fatal(err)
	}
	l := h.sess
	if r.summarizedTo != l.summarizedTo || r.body != l.body || !reflect.DeepEqual(r.cuts, l.cuts) || len(r.events) != len(l.events) {
		t.Errorf("resumed: to %d, body %q, cuts %+v, %d events\nlive:    to %d, body %q, cuts %+v, %d events",
			r.summarizedTo, r.body, r.cuts, len(r.events), l.summarizedTo, l.body, l.cuts, len(l.events))
	}
	if l.body != body("read big.txt once") {
		t.Errorf("the live body %q", l.body)
	}
}

// TestSummarizeOffChangesNothing: with Summarize off the same turn makes no summary and keeps no events.
func TestSummarizeOffChangesNothing(t *testing.T) {
	h := maskHarness(t, false, 120,
		goinfertest.Calls("", "t1", "read", bigRead), goinfertest.Calls("", "t2", "read", bigRead),
		goinfertest.Calls("", "t3", "read", bigRead), goinfertest.Calls("", "t4", "read", bigRead),
		goinfertest.Calls("", "t5", "read", bigRead), goinfertest.Says("done"))
	if _, err := h.sess.Turn(context.Background(), "read big.txt five times"); err != nil {
		t.Fatal(err)
	}
	if n := len(h.payloads(proto.EvCompaction)); n != 0 || len(h.sess.events) != 0 {
		t.Errorf("%d compactions, %d events kept", n, len(h.sess.events))
	}
}

// maskedSections records what the loop tells it was masked.
type maskedSections struct{ masked [][]string }

func (*maskedSections) ForCall(string, string, json.RawMessage) string { return "" }
func (m *maskedSections) Masked(ids []string)                          { m.masked = append(m.masked, ids) }
func (*maskedSections) Drop(string)                                    {}

// TestASummaryFreesWhatItRemoves: the results a summary removes are told to the section trackers, so their sections may
// load again, and masking never stubs them later.
func TestASummaryFreesWhatItRemoves(t *testing.T) {
	ms := &maskedSections{}
	// Masking on, so the loop tracks every result; reads of distinct ranges, so it masks none and the summary runs.
	h := maskHarness(t, true, 120,
		goinfertest.Calls("", "t1", "read", rangeRead(1)), goinfertest.Calls("", "t2", "read", rangeRead(51)),
		goinfertest.Calls("", "t3", "read", rangeRead(101)), goinfertest.Calls("", "t4", "read", rangeRead(151)),
		goinfertest.Says(body("read")), goinfertest.Says("done"))
	h.sess.cfg.Summarize, h.sess.cfg.Sections = true, ms
	h.sess = New(h.sess.cfg)
	if _, err := h.sess.Turn(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	if len(ms.masked) != 1 || !reflect.DeepEqual(ms.masked[0], []string{"t1"}) {
		t.Errorf("Masked calls %q, want [[t1]]: the summary removes the first read", ms.masked)
	}
	found := false
	for _, o := range h.sess.obs {
		if o.id == "t1" {
			found = true
			if !o.masked {
				t.Error("the removed result t1 can still be masked")
			}
		}
	}
	if !found {
		t.Fatal("the loop does not track t1: the check above checks nothing")
	}
	h.checkRebuild(t)
}

// TestMaskingThatFitsLeavesNoSummary: when masking frees enough to bring the prompt under the bar, no summary is made.
func TestMaskingThatFitsLeavesNoSummary(t *testing.T) {
	// Three distinct reads, then the first again: before request 5, with four responses open, masking stubs t1, and
	// what it frees brings the estimate under the bar.
	h := maskHarness(t, true, 120,
		goinfertest.Calls("", "t1", "read", rangeRead(1)), goinfertest.Calls("", "t2", "read", rangeRead(51)),
		goinfertest.Calls("", "t3", "read", rangeRead(101)), goinfertest.Calls("", "t4", "read", rangeRead(1)),
		goinfertest.Says("done"))
	h.sess.cfg.Summarize = true
	h.sess = New(h.sess.cfg)
	if _, err := h.sess.Turn(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	masked := 0
	for _, p := range h.payloads(proto.EvCompaction) {
		if c := p.(proto.Compaction); c.Summary != "" {
			t.Errorf("a summary at request %d, though masking freed %d bytes", c.Request, c.Before-c.After)
		}
	}
	for _, p := range h.payloads(proto.EvCompaction) {
		masked += len(p.(proto.Compaction).Masked)
	}
	if masked == 0 {
		t.Fatal("masking stubbed nothing: the check above checks nothing")
	}
}

// rangeRead reads 50 lines of big.txt from line from.
func rangeRead(from int) string {
	b, _ := json.Marshal(map[string]any{"path": "big.txt", "offset": from, "limit": 50})
	return string(b)
}
