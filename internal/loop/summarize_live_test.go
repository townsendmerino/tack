package loop

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server"
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
	if r.summarizedTo != l.summarizedTo || r.body != l.body || !reflect.DeepEqual(r.cuts, l.cuts) {
		t.Errorf("resumed: to %d, body %q, cuts %+v\nlive:    to %d, body %q, cuts %+v",
			r.summarizedTo, r.body, r.cuts, l.summarizedTo, l.body, l.cuts)
	}
	if l.body != body("read big.txt once") {
		t.Errorf("the live body %q", l.body)
	}
}

// TestSummarizeOffChangesNothing: with Summarize off the same turn makes no summary.
func TestSummarizeOffChangesNothing(t *testing.T) {
	h := maskHarness(t, false, 120,
		goinfertest.Calls("", "t1", "read", bigRead), goinfertest.Calls("", "t2", "read", bigRead),
		goinfertest.Calls("", "t3", "read", bigRead), goinfertest.Calls("", "t4", "read", bigRead),
		goinfertest.Calls("", "t5", "read", bigRead), goinfertest.Says("done"))
	if _, err := h.sess.Turn(context.Background(), "read big.txt five times"); err != nil {
		t.Fatal(err)
	}
	if n := len(h.payloads(proto.EvCompaction)); n != 0 {
		t.Errorf("%d compactions", n)
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

// TestTheSummarizersRequestsAreLogged: the summarizer's request is logged as request 0, on the alias, with its usage,
// and its tokens count toward the session as a resumed session counts them; the conversation's numbering is untouched.
func TestTheSummarizersRequestsAreLogged(t *testing.T) {
	h := summaryHarness(t)
	var sent []proto.RequestSent
	for _, p := range h.payloads(proto.EvRequestSent) {
		sent = append(sent, p.(proto.RequestSent))
	}
	var zero []proto.RequestSent
	for i, r := range sent {
		if r.Request == 0 {
			zero = append(zero, r)
			continue
		}
		if want := i + 1 - len(zero); r.Request != want {
			t.Errorf("request.sent %d is request %d, want %d: the summarizer's shifted the numbering", i, r.Request, want)
		}
	}
	if len(zero) != 1 || zero[0].Model != "m-fast" {
		t.Fatalf("the summarizer's request.sent %+v, want one on m-fast", zero)
	}
	evs, _ := h.st.Events("s_test", 0)
	var zeroSeq int64
	usageOfZero := 0
	for _, ev := range evs {
		v, _ := ev.Decode()
		if r, ok := v.(proto.RequestSent); ok && r.Request == 0 {
			zeroSeq = ev.Seq
		}
		if u, ok := v.(proto.Usage); ok && u.Request == 0 {
			usageOfZero++
			if ev.Cause != zeroSeq {
				t.Errorf("the summarizer's usage is caused by seq %d, want its request.sent at %d", ev.Cause, zeroSeq)
			}
		}
	}
	if usageOfZero != 1 {
		t.Errorf("%d usage events for the summarizer, want 1", usageOfZero)
	}
	r, err := Resume(h.sess.cfg, evs)
	if err != nil {
		t.Fatal(err)
	}
	if r.tokens != h.sess.tokens || r.requests != h.sess.requests || h.sess.requests != len(sent)-1 {
		t.Errorf("resumed: %d tokens, %d requests; live: %d tokens, %d requests; %d request.sent", r.tokens, r.requests,
			h.sess.tokens, h.sess.requests, len(sent))
	}
}

// TestTheSummarizersTokensCountTowardTheSession: the session's token count, which its budget reads, includes the
// summarizer's requests on top of the conversation's.
func TestTheSummarizersTokensCountTowardTheSession(t *testing.T) {
	h := summaryHarness(t)
	total := h.sess.tokens // every request's, the summarizer's included
	var conv int
	for _, p := range h.payloads(proto.EvUsage) {
		if u := p.(proto.Usage); u.Request != 0 {
			conv += computedTokens(u)
		}
	}
	if total <= conv {
		t.Fatalf("session tokens %d, the conversation's alone %d: the summarizer's were not counted", total, conv)
	}
}

// grows is a reply calling read with a reported prompt of prompt tokens, so the prompt can regrow past a floor.
func grows(id string, prompt int) goinfertest.Reply {
	return goinfertest.Reply{Body: goinfertest.SSEWithReuse("tool_use", prompt, 0, 20,
		proto.ModelBlock{Type: "tool_use", ID: id, Name: "read", Input: json.RawMessage(bigRead)})}
}

// TestASecondSummaryUpdatesTheFirst: once the prompt regrows past the first summary, a second one cuts at a message the
// first kept, updates its body, and the log still rebuilds the live request byte for byte.
func TestASecondSummaryUpdatesTheFirst(t *testing.T) {
	h := maskHarness(t, false, 120,
		goinfertest.Calls("", "t1", "read", bigRead), goinfertest.Calls("", "t2", "read", bigRead),
		goinfertest.Calls("", "t3", "read", bigRead), goinfertest.Calls("", "t4", "read", bigRead),
		goinfertest.Says(body("read big.txt once")), // before request 5
		grows("t5", 100), grows("t6", 130), // the prompt regrows 30 tokens past the first after the summary
		goinfertest.Says(body("read big.txt twice")), // before request 7
		goinfertest.Says("done"))
	h.sess.cfg.Summarize = true
	h.sess = New(h.sess.cfg)
	if _, err := h.sess.Turn(context.Background(), "read big.txt"); err != nil {
		t.Fatal(err)
	}
	var comps []proto.Compaction
	for _, p := range h.payloads(proto.EvCompaction) {
		comps = append(comps, p.(proto.Compaction))
	}
	if len(comps) != 2 || comps[0].Request != 5 || comps[1].Request != 7 {
		t.Fatalf("compactions %+v", comps)
	}
	if !strings.Contains(comps[1].Summary, body("read big.txt twice")) || strings.Contains(comps[1].Summary, body("read big.txt once")) {
		t.Errorf("the second summary does not replace the first body:\n%s", comps[1].Summary)
	}
	if slices.Max(comps[1].Summarized) <= slices.Max(comps[0].Summarized) {
		t.Errorf("the second summary covers up to seq %d, the first %d", slices.Max(comps[1].Summarized), slices.Max(comps[0].Summarized))
	}
	var asks []string
	for _, r := range h.script.Requests {
		if strings.Contains(string(r), "Previous summary:") {
			asks = append(asks, string(r))
		}
	}
	if len(asks) != 2 || !strings.Contains(asks[1], "read big.txt once") {
		t.Errorf("%d summarizer calls; the second was not given the first body", len(asks))
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
		t.Error("the rebuilt request's bytes differ from the live one's after two summaries")
	}
}

// TestASummaryThatGainsNothingBacksOff: a summary no smaller than what it would replace changes nothing, and the next
// requests do not pay for the summarizer again until the prompt regrows.
func TestASummaryThatGainsNothingBacksOff(t *testing.T) {
	small := `{"path":"mcp/token_budget.go"}` // a few bytes a result: the summary message is bigger
	h := maskHarness(t, false, 120,
		goinfertest.Calls("", "t1", "read", small), goinfertest.Calls("", "t2", "read", small),
		goinfertest.Calls("", "t3", "read", small), goinfertest.Calls("", "t4", "read", small),
		goinfertest.Says(body("read it")), // the one summarizer call, before request 5
		goinfertest.Calls("", "t5", "read", small), goinfertest.Calls("", "t6", "read", small), goinfertest.Says("done"))
	h.sess.cfg.Summarize = true
	h.sess = New(h.sess.cfg)
	if _, err := h.sess.Turn(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	asked := 0
	for _, r := range h.script.Requests {
		if strings.Contains(string(r), "Previous summary:") {
			asked++
		}
	}
	if asked != 1 || len(h.payloads(proto.EvCompaction)) != 0 {
		t.Errorf("%d summarizer calls, %d compactions; want 1 and none", asked, len(h.payloads(proto.EvCompaction)))
	}
}

// TestCancellingDuringTheSummarizerSendsNothingMore: a turn cancelled while the summarizer's request runs records no
// compaction and sends no further request, and ends cancelled.
func TestCancellingDuringTheSummarizerSendsNothingMore(t *testing.T) {
	h := maskHarness(t, false, 120,
		goinfertest.Calls("", "t1", "read", bigRead), goinfertest.Calls("", "t2", "read", bigRead),
		goinfertest.Calls("", "t3", "read", bigRead), goinfertest.Calls("", "t4", "read", bigRead),
		goinfertest.Says(body("never used")), goinfertest.Says("never sent"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := h.sess.cfg
	cfg.Summarize = true
	emit := cfg.Emit
	cfg.Emit = func(ev proto.Event) {
		emit(ev)
		if v, _ := ev.Decode(); ev.Type == proto.EvRequestSent && v.(proto.RequestSent).Request == 0 {
			cancel() // as the summarizer's request goes out
		}
	}
	h.sess = New(cfg)
	reason, err := h.sess.Turn(ctx, "go")
	if err != nil || reason != proto.StopCancelled {
		t.Fatalf("reason %q, %v; want cancelled", reason, err)
	}
	var after []int
	seenZero := false
	for _, p := range h.payloads(proto.EvRequestSent) {
		r := p.(proto.RequestSent)
		if r.Request == 0 {
			seenZero = true
		} else if seenZero {
			after = append(after, r.Request)
		}
	}
	if !seenZero || len(after) != 0 || len(h.payloads(proto.EvCompaction)) != 0 {
		t.Errorf("summarizer request seen %v; requests after it %v; %d compactions", seenZero, after, len(h.payloads(proto.EvCompaction)))
	}
	h.checkRebuild(t)
}

// TestATimedOutSummarizerCallFallsBack: a summarizer request past summaryCallTimeout is a failure: the summary is made
// without a body, and the turn goes on.
func TestATimedOutSummarizerCallFallsBack(t *testing.T) {
	to := summaryCallTimeout
	summaryCallTimeout = time.Nanosecond
	t.Cleanup(func() { summaryCallTimeout = to })
	h := maskHarness(t, false, 120,
		goinfertest.Calls("", "t1", "read", bigRead), goinfertest.Calls("", "t2", "read", bigRead),
		goinfertest.Calls("", "t3", "read", bigRead), goinfertest.Calls("", "t4", "read", bigRead),
		goinfertest.Says("done"))
	h.sess.cfg.Summarize = true
	h.sess = New(h.sess.cfg)
	reason, err := h.sess.Turn(context.Background(), "go")
	var comps []proto.Compaction
	for _, p := range h.payloads(proto.EvCompaction) {
		comps = append(comps, p.(proto.Compaction))
	}
	if err != nil || reason != proto.StopEndTurn || len(comps) != 1 || strings.Contains(comps[0].Summary, "## Goal") ||
		!strings.Contains(comps[0].Summary, "## Task\n\ngo") {
		t.Errorf("reason %q, %v; compactions %+v", reason, err, comps)
	}
	h.checkRebuild(t)
}

// TestTheSummaryFloorCanBePassed: the regrowth a floor asks for is capped by the room left, so a floor set near a full
// window does not switch summarizing off for good.
func TestTheSummaryFloorCanBePassed(t *testing.T) {
	for _, c := range []struct{ w, floor, want int }{
		{120, 60, 12}, // maskRegrowShare of the window
		{120, 108, 6}, // at 90%: half the 12 tokens left
		{120, 120, 0}, // at the window: any growth, or none
		{16384, 8000, 1638},
	} {
		if got := summaryRegrow(c.w, c.floor); got != c.want || c.floor+got > c.w {
			t.Errorf("summaryRegrow(%d, %d) = %d, want %d", c.w, c.floor, got, c.want)
		}
	}
}

// TestANewTurnClearsTheSummaryFloor: an attempt that gains nothing backs off for the rest of its turn, and the next
// turn tries again.
func TestANewTurnClearsTheSummaryFloor(t *testing.T) {
	small := `{"path":"mcp/token_budget.go"}`
	h := maskHarness(t, false, 120,
		goinfertest.Calls("", "t1", "read", small), goinfertest.Calls("", "t2", "read", small),
		goinfertest.Calls("", "t3", "read", small), goinfertest.Calls("", "t4", "read", small),
		goinfertest.Says(body("first try")), // turn 1's one attempt, before request 5
		goinfertest.Calls("", "t5", "read", small), goinfertest.Says("done"),
		goinfertest.Says(body("second try")), // turn 2's attempt, before its first request
		goinfertest.Says("done again"))
	h.sess.cfg.Summarize = true
	h.sess = New(h.sess.cfg)
	for _, q := range []string{"go", "again"} {
		if _, err := h.sess.Turn(context.Background(), q); err != nil {
			t.Fatal(err)
		}
	}
	asked := 0
	for _, r := range h.script.Requests {
		if strings.Contains(string(r), "Previous summary:") {
			asked++
		}
	}
	if asked != 2 {
		t.Errorf("%d summarizer calls over two turns, want 2: one each", asked)
	}
}

// TestAMaskingCompactionClearsTheSummaryFloor: masking that changes the history clears the floor, so the next request may
// try a summary again.
func TestAMaskingCompactionClearsTheSummaryFloor(t *testing.T) {
	h := maskHarness(t, true, 120)
	h.sess.sumFloor = 100
	h.sess.obs = []observation{{id: "c1", tool: "bash", key: "x", req: 1, bytes: 1000, ok: true}}
	h.sess.requests = 10
	// compactNow masks the old command output (older than maskKeepRequests requests) when it can rewrite it.
	h.sess.conv.BeginTurn("go", false)
	h.sess.conv.AddResponse([]proto.ModelBlock{{Type: "tool_use", ID: "c1", Name: "bash", Input: json.RawMessage(`{"command":"x"}`)}})
	h.sess.conv.AnswerTools([]server.ToolResult{{ID: "c1", Result: strings.Repeat("o", 1000)}})
	if n, err := h.sess.compactNow(11, 0, "window"); err != nil || n != 1 {
		t.Fatalf("compactNow masked %d, %v", n, err)
	}
	if h.sess.sumFloor != 0 {
		t.Errorf("the summary floor is %d after a masking compaction, want 0", h.sess.sumFloor)
	}
}

// TestATaskWithItsOwnHeadingsKeepsTheBody: a task written in markdown, with its own "## Goal", does not shift the body:
// the summary's body, live and on a resume, is the summarizer's, not a span starting inside the user's text.
func TestATaskWithItsOwnHeadingsKeepsTheBody(t *testing.T) {
	h := maskHarness(t, false, 120,
		goinfertest.Calls("", "t1", "read", bigRead), goinfertest.Calls("", "t2", "read", bigRead),
		goinfertest.Calls("", "t3", "read", bigRead), goinfertest.Calls("", "t4", "read", bigRead),
		goinfertest.Says(body("read big.txt")), goinfertest.Calls("", "t5", "read", bigRead), goinfertest.Says("done"))
	h.sess.cfg.Summarize = true
	h.sess = New(h.sess.cfg)
	task := "Make the reader faster.\n\n## Goal\n\nHalve the time of a whole read.\n\n## Constraints\n\nNo new dependency."
	if _, err := h.sess.Turn(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if len(h.payloads(proto.EvCompaction)) != 1 {
		t.Fatalf("%d compactions", len(h.payloads(proto.EvCompaction)))
	}
	want := body("read big.txt")
	if h.sess.body != want {
		t.Errorf("live body:\n%s\nwant the summarizer's:\n%s", h.sess.body, want)
	}
	evs, _ := h.st.Events("s_test", 0)
	r, err := Resume(h.sess.cfg, evs)
	if err != nil || r.body != want {
		t.Errorf("resumed body:\n%s\n%v", r.body, err)
	}
}

// TestFirstSummaryCutsAsTheLiveLoopDoes: on the log as it stood when the live summary ran, FirstSummary hands the
// summarizer the stretch the live loop covered, seq for seq.
func TestFirstSummaryCutsAsTheLiveLoopDoes(t *testing.T) {
	h := summaryHarness(t)
	evs, _ := h.st.Events("s_test", 0)
	var before []proto.Event // up to the summarizer's own first request
	for _, ev := range evs {
		if v, _ := ev.Decode(); ev.Type == proto.EvRequestSent && v.(proto.RequestSent).Request == 0 {
			break
		}
		before = append(before, ev)
	}
	stretch, ok := FirstSummary(before)
	comps := h.payloads(proto.EvCompaction)
	if !ok || len(comps) != 1 {
		t.Fatalf("ok %v, %d compactions", ok, len(comps))
	}
	var got []int64
	for _, ev := range stretch {
		got = append(got, ev.Seq)
	}
	if want := comps[0].(proto.Compaction).Summarized; !slices.Equal(got, want) {
		t.Errorf("FirstSummary covers %v, the live summary %v", got, want)
	}
	if _, ok := FirstSummary(before[:4]); ok {
		t.Error("a log with too few requests should have no summary")
	}
}
