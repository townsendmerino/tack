package loop

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
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
	sec := retrieveSection(context.Background(), s, []string{"a", "b"}, 4096, nil)
	if !strings.HasPrefix(sec, retrieveHeading+"\n\nx.go:1-3\n```\nfunc A() {}\n```\n\ny.go:5-9") || strings.Count(sec, "x.go:1-3") != 1 ||
		!strings.Contains(sec, "z.go:2-4") {
		t.Errorf("section:\n%s", sec)
	}
	small := retrieveSection(context.Background(), s, []string{"a", "b"}, 160, nil)
	if strings.Contains(small, "z.go") || !strings.Contains(small, "x.go:1-3") || len(small) > 160 {
		t.Errorf("a 160-byte budget gave %d bytes:\n%s", len(small), small)
	}
	if retrieveSection(context.Background(), s, []string{"nothing"}, 4096, nil) != "" ||
		retrieveSection(context.Background(), &fakeSearcher{err: errors.New("down")}, []string{"a"}, 4096, nil) != "" {
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
	if strings.Contains(h.sess.body, retrieveHeading) || strings.Contains(comps[0].Body, "applyTokenBudget() {}") {
		t.Error("the section reached the next summary's previous body")
	}
	if comps[0].Body != h.sess.body {
		t.Error("the body recorded in the log differs from the live session's")
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

// deadlineSearcher hangs until its context ends, as hangingSearcher does, and records each search's deadline as the time
// it had left when called (-1 for none, which it answers at once rather than hang) and how its context ended.
type deadlineSearcher struct {
	mu   *sync.Mutex
	left *[]time.Duration
	ends *[]error
}

func (d deadlineSearcher) Search(ctx context.Context, _ string, _ int) ([]tools.Hit, error) {
	dl, ok := ctx.Deadline()
	d.mu.Lock()
	if !ok {
		*d.left = append(*d.left, -1)
		d.mu.Unlock()
		return nil, errors.New("no deadline")
	}
	*d.left = append(*d.left, time.Until(dl))
	d.mu.Unlock()
	<-ctx.Done()
	d.mu.Lock()
	*d.ends = append(*d.ends, ctx.Err())
	d.mu.Unlock()
	return nil, ctx.Err()
}

// TestASearchThatHangsTimesOut: each re-retrieval search has its own timeout, so a hung ken costs the summary its
// section, not the turn: every search is called with a deadline no further off than retrieveSearchTimeout, and ends
// at it. It reads the deadline each search was given, not the turn's wall time, so a loaded machine cannot fail it.
func TestASearchThatHangsTimesOut(t *testing.T) {
	to := retrieveSearchTimeout
	retrieveSearchTimeout = 20 * time.Millisecond
	t.Cleanup(func() { retrieveSearchTimeout = to })
	var left []time.Duration
	var ends []error
	h := retrieveHarness(t, 10, deadlineSearcher{mu: &sync.Mutex{}, left: &left, ends: &ends})
	if len(left) == 0 {
		t.Fatal("no search ran: the test checks nothing")
	}
	for i, l := range left {
		if l < 0 || l > retrieveSearchTimeout {
			t.Errorf("search %d was called with %v left (-1: no deadline); want its own timeout, at most %v", i, l, retrieveSearchTimeout)
		}
	}
	for i, err := range ends {
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("search %d ended %v, want its deadline", i, err)
		}
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

// TestALiveBodyIsTheLoggedOne: a summarizer whose body echoes the section's heading leaves the same previous body live
// and on a resume: the body is recorded with the summary, not parsed back out of it.
func TestALiveBodyIsTheLoggedOne(t *testing.T) {
	echo := strings.Replace(body("read"), "## Next steps\n\n(none)", "## Next steps\n\nfind it\n\n"+retrieveHeading+"\n\nan echo", 1)
	h := maskHarness(t, false, 4000,
		grows("t1", 3500), grows("t2", 3500), grows("t3", 3500), grows("t4", 3500),
		goinfertest.Says(echo), grows("t5", 3500), goinfertest.Says("done"))
	h.sess.cfg.Summarize = true
	h.sess = New(h.sess.cfg)
	if _, err := h.sess.Turn(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	comps := h.payloads(proto.EvCompaction)
	if len(comps) != 1 {
		t.Fatalf("%d compactions", len(comps))
	}
	if logged := comps[0].(proto.Compaction).Body; h.sess.body != logged || h.sess.body != echo {
		t.Errorf("live body %q, recorded %q, want the summarizer's, echo and all", h.sess.body, logged)
	}
	evs, _ := h.st.Events("s_test", 0)
	r, err := Resume(h.sess.cfg, evs)
	if err != nil || r.body != h.sess.body {
		t.Errorf("resumed body %q, live %q, %v", r.body, h.sess.body, err)
	}
}

// TestASummaryWithNoAnchorsRebuilds: a history with nothing to anchor (no path, symbol, hash or error in it) still
// summarizes, with its section after the body, and the log rebuilds the live request.
func TestASummaryWithNoAnchorsRebuilds(t *testing.T) {
	grep := func(id string) goinfertest.Reply { // a grep with no path, past 75% of the window
		return goinfertest.Reply{Body: goinfertest.SSEWithReuse("tool_use", 3500, 0, 20,
			proto.ModelBlock{Type: "tool_use", ID: id, Name: "grep", Input: json.RawMessage(`{"pattern":"plain"}`)})}
	}
	h := maskHarness(t, false, 4000, grep("t1"), grep("t2"), grep("t3"), grep("t4"),
		goinfertest.Says(strings.Replace(body("searched"), "## Next steps\n\n(none)", "## Next steps\n\nfind it", 1)),
		grep("t5"), goinfertest.Says("done"))
	root := h.ws.Root()
	os.RemoveAll(filepath.Join(root, "mcp"))
	os.Remove(filepath.Join(root, "big.txt"))
	os.WriteFile(filepath.Join(root, "notes"), []byte(strings.Repeat("plain words with nothing to anchor here\n", 300)), 0o644)
	s := &fakeSearcher{hits: map[string][]tools.Hit{"find it": {hit("notes", 1, 1, "plain words")}}}
	h.sess.cfg.Summarize, h.sess.cfg.SummaryRetrieve, h.sess.cfg.Searcher = true, 10, s
	h.sess = New(h.sess.cfg)
	if _, err := h.sess.Turn(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	comps := h.payloads(proto.EvCompaction)
	if len(comps) != 1 {
		t.Fatalf("%d compactions", len(comps))
	}
	sum := comps[0].(proto.Compaction).Summary
	if strings.Contains(sum, "## Anchors (exact)") || !strings.Contains(sum, retrieveHeading) {
		t.Fatalf("want no anchors and a section:\n%s", sum)
	}
	if strings.Contains(h.sess.body, retrieveHeading) {
		t.Error("the section reached the body")
	}
	h.checkRebuild(t)
}

// indexSearcher is an index that goes stale: indexed is what it holds, disk what the files are now, and Sync(paths)
// reloads those paths. Search returns the files whose indexed text contains the query, in name order, and sleeps
// delay (cut short by its context) so a timeout can be shared or not.
type indexSearcher struct {
	indexed, disk map[string]string
	delay         time.Duration
	synced        [][]string
}

func (x *indexSearcher) wait(ctx context.Context) error {
	select {
	case <-time.After(x.delay):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (x *indexSearcher) Search(ctx context.Context, q string, k int) ([]tools.Hit, error) {
	if err := x.wait(ctx); err != nil {
		return nil, err
	}
	var hits []tools.Hit
	for _, f := range slices.Sorted(maps.Keys(x.indexed)) {
		if strings.Contains(x.indexed[f], q) {
			hits = append(hits, hit(f, 1, 1, x.indexed[f]))
		}
	}
	return hits[:min(k, len(hits))], nil
}

func (x *indexSearcher) Sync(ctx context.Context, paths []string) (bool, error) {
	if err := x.wait(ctx); err != nil {
		return false, err
	}
	x.synced = append(x.synced, paths)
	moved := false
	for _, p := range paths {
		if d, ok := x.disk[p]; ok && x.indexed[p] != d {
			x.indexed[p], moved = d, true
		}
	}
	return moved, nil
}

// TestRetrieveSyncsTheFilesTheSessionChanged: a file the session created, and one whose edit brought in the queried
// text, are found though neither was a hit before the sync; without the changed list they are not.
func TestRetrieveSyncsTheFilesTheSessionChanged(t *testing.T) {
	mk := func() *indexSearcher {
		return &indexSearcher{
			indexed: map[string]string{"old.go": "func Old() {} // needle", "edited.go": "func Edited() {}"},
			disk:    map[string]string{"old.go": "func Old() {} // needle", "edited.go": "func Edited() {} // needle", "new.go": "func New() {} // needle"},
		}
	}
	got := retrieveSection(context.Background(), mk(), []string{"needle"}, 4096, []string{"new.go", "edited.go"})
	for _, f := range []string{"old.go", "edited.go", "new.go"} {
		if !strings.Contains(got, f+":1-1") {
			t.Errorf("with the changed files, %s is missing:\n%s", f, got)
		}
	}
	got = retrieveSection(context.Background(), mk(), []string{"needle"}, 4096, nil)
	if strings.Contains(got, "new.go") || strings.Contains(got, "edited.go") {
		t.Errorf("without them the unsynced files appeared:\n%s", got)
	}
}

// TestRetrieveGivesSyncAndEachSearchItsOwnTimeout: three steps of 30 ms each fit separate 50 ms timeouts but not one
// shared one, and the section still has the synced file's hit.
func TestRetrieveGivesSyncAndEachSearchItsOwnTimeout(t *testing.T) {
	to := retrieveSearchTimeout
	retrieveSearchTimeout = 50 * time.Millisecond
	t.Cleanup(func() { retrieveSearchTimeout = to })
	x := &indexSearcher{delay: 30 * time.Millisecond,
		indexed: map[string]string{"a.go": "x // needle"}, disk: map[string]string{"a.go": "y // needle"}}
	if got := retrieveSection(context.Background(), x, []string{"needle"}, 4096, nil); !strings.Contains(got, "y // needle") {
		t.Errorf("the second search was lost to a shared timeout:\n%s", got)
	}
}

// TestChangedFilesAreTheSuccessfulWritesAndEdits: a failed call, a read, and a path outside the root are left out;
// an absolute path inside the root is made relative; a name that merely starts with ".." is inside; each file once.
func TestChangedFilesAreTheSuccessfulWritesAndEdits(t *testing.T) {
	call := func(id, name, path string) []any {
		return []any{proto.EvToolCall, proto.ToolCall{ID: id, Name: name, Input: json.RawMessage(`{"path":"` + path + `"}`)}}
	}
	done := func(id, outcome string) []any {
		return []any{proto.EvToolFinished, proto.ToolFinished{ID: id, Outcome: outcome}}
	}
	var pairs []any
	for _, p := range [][]any{
		call("1", "write", "./a/new.go"), done("1", proto.OutcomeOK),
		call("2", "edit", "/repo/b.go"), done("2", proto.OutcomeOK),
		call("3", "edit", "c.go"), done("3", proto.OutcomeError),
		call("4", "read", "d.go"), done("4", proto.OutcomeOK),
		call("5", "edit", "a/new.go"), done("5", proto.OutcomeOK),
		call("6", "write", "/elsewhere/e.go"), done("6", proto.OutcomeOK),
		call("7", "edit", "../f.go"), done("7", proto.OutcomeOK),
		call("8", "write", "..g/h.go"), done("8", proto.OutcomeOK),
	} {
		pairs = append(pairs, p...)
	}
	evs := summaryLog(t, pairs...)
	got := changedFiles(evs, "/repo")
	if want := []string{"a/new.go", "b.go", "..g/h.go"}; !slices.Equal(got, want) {
		t.Errorf("changed %q, want %q", got, want)
	}
}

// TestABashCallMayHaveChangedAnyFile: after a successful bash call (sed -i, a formatter, a generator) the files
// modified since the session began are changed files, dot directories and files older than the session are not, and
// without a successful bash call the walk does not happen.
func TestABashCallMayHaveChangedAnyFile(t *testing.T) {
	root := t.TempDir()
	start := time.Now().Add(-time.Hour)
	put := func(rel string, mod time.Time) {
		p := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("x"), 0o644)
		os.Chtimes(p, mod, mod)
	}
	put("old.go", start.Add(-time.Hour))
	put("gen/new.go", start.Add(time.Minute))
	put(".git/hook", start.Add(time.Minute))
	log := func(name, outcome string) []proto.Event {
		var evs []proto.Event
		for i, p := range []struct {
			typ proto.EventType
			v   any
		}{
			{proto.EvToolCall, proto.ToolCall{ID: "b", Name: name, Input: json.RawMessage(`{"command":"gofmt -w ."}`)}},
			{proto.EvToolFinished, proto.ToolFinished{ID: "b", Outcome: outcome}},
		} {
			e, err := proto.NewEvent(int64(i+1), start, p.typ, 0, p.v)
			if err != nil {
				t.Fatal(err)
			}
			evs = append(evs, e)
		}
		return evs
	}
	if got := changedFiles(log("bash", proto.OutcomeOK), root); !slices.Equal(got, []string{"gen/new.go"}) {
		t.Errorf("after bash: %q, want [gen/new.go]", got)
	}
	if got := changedFiles(log("bash", proto.OutcomeError), root); len(got) != 0 {
		t.Errorf("after a failed bash: %q", got)
	}
	if got := changedFiles(log("read", proto.OutcomeOK), root); len(got) != 0 {
		t.Errorf("after a read: %q", got)
	}
}

// TestTheBashWalkSkipsDependencyDirsKeepsTheNewestAndAllowsSlack: vendor/ and node_modules/ are not entered; past the cap
// the newest files by modification time are kept, not the lexically first; a file stamped a second before the session
// began (a same-tick edit) still counts, one a minute before does not.
func TestTheBashWalkSkipsDependencyDirsKeepsTheNewestAndAllowsSlack(t *testing.T) {
	cap0 := retrieveWalkCap
	retrieveWalkCap = 2
	t.Cleanup(func() { retrieveWalkCap = cap0 })
	root := t.TempDir()
	start := time.Now().Add(-time.Hour)
	put := func(rel string, mod time.Time) {
		p := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("x"), 0o644)
		os.Chtimes(p, mod, mod)
	}
	put("a.go", start.Add(time.Minute))
	put("b.go", start.Add(2*time.Minute))
	put("z_newest.go", start.Add(10*time.Minute))
	put("y_newer.go", start.Add(5*time.Minute))
	put("vendor/dep.go", start.Add(20*time.Minute))
	put("node_modules/m/i.js", start.Add(20*time.Minute))
	var evs []proto.Event
	for i, p := range []struct {
		typ proto.EventType
		v   any
	}{
		{proto.EvToolCall, proto.ToolCall{ID: "b", Name: "bash", Input: json.RawMessage(`{"command":"make gen"}`)}},
		{proto.EvToolFinished, proto.ToolFinished{ID: "b", Outcome: proto.OutcomeOK}},
	} {
		e, err := proto.NewEvent(int64(i+1), start, p.typ, 0, p.v)
		if err != nil {
			t.Fatal(err)
		}
		evs = append(evs, e)
	}
	if got := changedFiles(evs, root); !slices.Equal(got, []string{"z_newest.go", "y_newer.go"}) {
		t.Errorf("past the cap, kept %q; want the two newest, neither dependency dir", got)
	}
	retrieveWalkCap = 10
	put("tick.go", start.Add(-time.Second))
	put("stale.go", start.Add(-time.Minute))
	got := changedFiles(evs, root)
	if !slices.Contains(got, "tick.go") || slices.Contains(got, "stale.go") || slices.ContainsFunc(got, func(p string) bool { return strings.HasPrefix(p, "vendor") || strings.HasPrefix(p, "node_modules") }) {
		t.Errorf("changed %q: want tick.go (within the slack), not stale.go, nothing under vendor or node_modules", got)
	}
}
