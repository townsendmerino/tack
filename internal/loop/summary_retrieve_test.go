package loop

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
	"github.com/townsendmerino/tack/internal/tools"
)

// fakeSearcher answers each query with its hits, and records the queries.
type fakeSearcher struct {
	hits  map[string][]tools.Hit
	err   error
	asked []string
}

func (f *fakeSearcher) Search(_ context.Context, q string, k int) ([]tools.Hit, error) {
	f.asked = append(f.asked, q)
	h := f.hits[q]
	return h[:min(k, len(h))], f.err
}

func TestOpenThreadsAreTakenFromTheBody(t *testing.T) {
	b := strings.Join([]string{"## Goal", "fix it", "## In progress", "- the rename in edit.go", "(none)",
		"## Decisions", "keep the mode", "## Next steps", "1. run the tests", "- the rename in edit.go"}, "\n")
	got := openThreads(b, Anchors{Symbols: []string{"renameFile", "TestRenameKeepsMode"}})
	want := []string{"the rename in edit.go", "run the tests", "renameFile", "TestRenameKeepsMode"}
	if !slices.Equal(got, want) {
		t.Errorf("open threads %q, want %q", got, want)
	}
	var many []string
	for i := range 20 {
		many = append(many, "Sym"+string(rune('A'+i))+"x")
	}
	if n := len(openThreads("", Anchors{Symbols: many})); n != retrieveQueries {
		t.Errorf("%d queries, want %d", n, retrieveQueries)
	}
}

func hit(file string, from, to int, text string) tools.Hit {
	return tools.Hit{File: file, StartLine: from, EndLine: to, Text: text}
}

// TestTheRetrieveSectionIsDedupedAndBudgeted: each span once, in the queries' order, stopping at the first hit past the
// budget; nothing found is no section.
func TestTheRetrieveSectionIsDedupedAndBudgeted(t *testing.T) {
	s := &fakeSearcher{hits: map[string][]tools.Hit{
		"a": {hit("x.go", 1, 3, "func A() {}"), hit("y.go", 5, 9, "func B() {}")},
		"b": {hit("x.go", 1, 3, "func A() {}"), hit("z.go", 2, 4, strings.Repeat("z", 400))},
	}}
	sec := retrieveSection(context.Background(), s, []string{"a", "b"}, 4096)
	if !strings.HasPrefix(sec, retrieveHeading+"\n\nx.go:1-3\n```\nfunc A() {}\n```\n\ny.go:5-9") || strings.Count(sec, "x.go:1-3") != 1 ||
		!strings.Contains(sec, "z.go:2-4") {
		t.Errorf("section:\n%s", sec)
	}
	small := retrieveSection(context.Background(), s, []string{"a", "b"}, 160)
	if strings.Contains(small, "z.go") || !strings.Contains(small, "x.go:1-3") || len(small) > 160 {
		t.Errorf("a 160-byte budget gave %d bytes:\n%s", len(small), small)
	}
	if retrieveSection(context.Background(), s, []string{"nothing"}, 4096) != "" ||
		retrieveSection(context.Background(), &fakeSearcher{err: errors.New("down")}, []string{"a"}, 4096) != "" {
		t.Error("no hits, or a failed search, should give no section")
	}
}

// retrieveHarness is summaryHarness with re-retrieval at share percent of the window, through s.
func retrieveHarness(t *testing.T, share int, s tools.Searcher) *harness {
	t.Helper()
	h := maskHarness(t, false, 4000,
		grows("t1", 3500), grows("t2", 3500), grows("t3", 3500), grows("t4", 3500),
		goinfertest.Says(strings.Replace(body("read big.txt"), "## Next steps\n\n(none)", "## Next steps\n\nfind applyTokenBudget", 1)),
		grows("t5", 3500), goinfertest.Says("done"))
	h.sess.cfg.Summarize, h.sess.cfg.SummaryRetrieve, h.sess.cfg.Searcher = true, share, s
	h.sess = New(h.sess.cfg)
	if _, err := h.sess.Turn(context.Background(), "read big.txt"); err != nil {
		t.Fatal(err)
	}
	return h
}

// TestALiveSummaryReRetrieves: the open thread the summary names is searched, its current code ends the summary
// message, the log rebuilds the request byte for byte, and the next summary's previous body leaves the section out.
func TestALiveSummaryReRetrieves(t *testing.T) {
	s := &fakeSearcher{hits: map[string][]tools.Hit{"find applyTokenBudget": {hit("mcp/token_budget.go", 3, 3, "func applyTokenBudget() {}")}}}
	h := retrieveHarness(t, 10, s)
	var comps []proto.Compaction
	for _, p := range h.payloads(proto.EvCompaction) {
		comps = append(comps, p.(proto.Compaction))
	}
	if len(comps) != 1 {
		t.Fatalf("compactions %+v", comps)
	}
	sum := comps[0].Summary
	if !slices.Contains(s.asked, "find applyTokenBudget") ||
		!strings.HasSuffix(sum, retrieveHeading+"\n\nmcp/token_budget.go:3-3\n```\nfunc applyTokenBudget() {}\n```") {
		t.Errorf("asked %q; summary ends:\n%s", s.asked, sum[max(0, len(sum)-300):])
	}
	if strings.Contains(h.sess.body, retrieveHeading) || strings.Contains(bodyOf(sum), "applyTokenBudget() {}") {
		t.Error("the section reached the next summary's previous body")
	}
	if bodyOf(sum) != h.sess.body {
		t.Error("the body a resume reads from the log differs from the live session's")
	}
	h.checkRebuild(t)
	evs, _ := h.st.Events("s_test", 0)
	re, err := Rebuild(evs, h.ws.Root(), *h.sess.Conversation())
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(h.sess.Conversation().Request())
	b, _ := json.Marshal(re.Request())
	if string(a) != string(b) {
		t.Error("the rebuilt request's bytes differ from the live one's")
	}
}

// TestNoReRetrievalWhenOffOrWithoutKen: with the share at 0, or no searcher, the summary has no section and ken is not
// asked.
func TestNoReRetrievalWhenOffOrWithoutKen(t *testing.T) {
	s := &fakeSearcher{hits: map[string][]tools.Hit{"find applyTokenBudget": {hit("a.go", 1, 1, "x")}}}
	for name, h := range map[string]*harness{"off": retrieveHarness(t, 0, s), "no ken": retrieveHarness(t, 10, nil)} {
		for _, p := range h.payloads(proto.EvCompaction) {
			if strings.Contains(p.(proto.Compaction).Summary, retrieveHeading) {
				t.Errorf("%s: a re-retrieval section", name)
			}
		}
	}
	if len(s.asked) != 0 {
		t.Errorf("ken was asked %q", s.asked)
	}
}

// TestTheShareSetsTheBudget: the share is the budget: a 1 KB hit fits 10% of a 4000-token window (1,600 bytes), not 1%
// (160 bytes).
func TestTheShareSetsTheBudget(t *testing.T) {
	for share, want := range map[int]bool{1: false, 10: true} {
		s := &fakeSearcher{hits: map[string][]tools.Hit{"find applyTokenBudget": {hit("a.go", 1, 200, strings.Repeat("line\n", 200))}}}
		h := retrieveHarness(t, share, s)
		got := false
		for _, p := range h.payloads(proto.EvCompaction) {
			got = got || strings.Contains(p.(proto.Compaction).Summary, retrieveHeading)
		}
		if got != want || len(s.asked) == 0 {
			t.Errorf("share %d%%: section %v, want %v; asked %q", share, got, want, s.asked)
		}
	}
}

// TestBodyOfStopsAtTheSectionWithNoAnchors: with no anchors the re-retrieval section follows the body directly, and the
// body read back from the log leaves it out, as the live session's does.
func TestBodyOfStopsAtTheSectionWithNoAnchors(t *testing.T) {
	b := body("did it")
	msg := UserText(true, "/w", summaryFrame+"## Task\n\ngo\n\n"+b+"\n\n"+retrieveHeading+"\n\na.go:1-2\n```\nfunc A() {}\n```")
	if got := bodyOf(msg); got != b {
		t.Errorf("bodyOf:\n%s\nwant:\n%s", got, b)
	}
}

// TestASectionTooBigForWhatTheSummaryFreesIsLeftOut: a hit within the share but past half of what the summary frees
// is left out, and the summary still goes in.
func TestASectionTooBigForWhatTheSummaryFreesIsLeftOut(t *testing.T) {
	s := &fakeSearcher{hits: map[string][]tools.Hit{"find applyTokenBudget": {hit("a.go", 1, 300, strings.Repeat("x", 15<<10))}}}
	h := maskHarness(t, false, 40000, // 10% is 16,000 bytes: the 15 KB hit is within the share
		grows("t1", 35000), grows("t2", 35000), grows("t3", 35000), grows("t4", 35000),
		goinfertest.Says(strings.Replace(body("read big.txt"), "## Next steps\n\n(none)", "## Next steps\n\nfind applyTokenBudget", 1)),
		grows("t5", 35000), goinfertest.Says("done"))
	h.sess.cfg.Summarize, h.sess.cfg.SummaryRetrieve, h.sess.cfg.Searcher = true, 10, s
	h.sess = New(h.sess.cfg)
	if _, err := h.sess.Turn(context.Background(), "read big.txt"); err != nil {
		t.Fatal(err)
	}
	comps := h.payloads(proto.EvCompaction)
	if len(comps) != 1 {
		t.Fatalf("%d compactions, want the summary without its section", len(comps))
	}
	c := comps[0].(proto.Compaction)
	if strings.Contains(c.Summary, retrieveHeading) || c.After >= c.Before || len(s.asked) == 0 {
		t.Errorf("section in the summary %v, after %d, before %d, asked %q", strings.Contains(c.Summary, retrieveHeading), c.After, c.Before, s.asked)
	}
	h.checkRebuild(t)
}

// hangingSearcher answers nothing until its context ends; onSearch, when set, runs first.
type hangingSearcher struct{ onSearch func() }

func (h hangingSearcher) Search(ctx context.Context, _ string, _ int) ([]tools.Hit, error) {
	if h.onSearch != nil {
		h.onSearch()
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

// TestASearchThatHangsTimesOut: each re-retrieval search has its own timeout, so a hung ken costs the summary its
// section, not the turn.
func TestASearchThatHangsTimesOut(t *testing.T) {
	to := retrieveSearchTimeout
	retrieveSearchTimeout = 20 * time.Millisecond
	t.Cleanup(func() { retrieveSearchTimeout = to })
	start := time.Now()
	h := retrieveHarness(t, 10, hangingSearcher{})
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("the turn took %v with ken hung", took)
	}
	comps := h.payloads(proto.EvCompaction)
	if len(comps) != 1 || strings.Contains(comps[0].(proto.Compaction).Summary, retrieveHeading) {
		t.Errorf("%d compactions; want the summary without a section", len(comps))
	}
}

// TestCancellingDuringRetrievalRecordsNoSummary: a turn cancelled while its summary re-retrieves records no compaction
// and ends cancelled, as one cancelled during the summarizer's call does.
func TestCancellingDuringRetrievalRecordsNoSummary(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := maskHarness(t, false, 4000,
		grows("t1", 3500), grows("t2", 3500), grows("t3", 3500), grows("t4", 3500),
		goinfertest.Says(strings.Replace(body("read"), "## Next steps\n\n(none)", "## Next steps\n\nfind it", 1)),
		goinfertest.Says("never sent"))
	h.sess.cfg.Summarize, h.sess.cfg.SummaryRetrieve, h.sess.cfg.Searcher = true, 10, hangingSearcher{onSearch: cancel}
	h.sess = New(h.sess.cfg)
	reason, err := h.sess.Turn(ctx, "go")
	if err != nil || reason != proto.StopCancelled || len(h.payloads(proto.EvCompaction)) != 0 {
		t.Errorf("reason %q, %v, %d compactions; want cancelled and none", reason, err, len(h.payloads(proto.EvCompaction)))
	}
	h.checkRebuild(t)
}
