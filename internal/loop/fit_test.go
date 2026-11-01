package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/townsendmerino/tack/internal/models"
	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
	"github.com/townsendmerino/tack/internal/tools"
)

// The scripted server reports a 100-token prompt and a 20-token reply, and testBudget's max_tokens is 256, so a window
// of fitUsed+n leaves n tokens. The request is far more than 6 bytes per token, so the rate is held at fitMaxRate:
// n tokens of room are 6n bytes.
const fitUsed = 100 + 20 + 256 + fitMarginTokens

// bigRead is a read of all 300 lines of big.txt, about 14 KB: under the 16 KiB result cap.
const bigRead = `{"path":"big.txt","offset":1,"limit":300}`

// fitHarness is newHarness with big.txt in the workspace, FitWindow on and the served window set.
func fitHarness(t *testing.T, window int, replies ...goinfertest.Reply) *harness {
	t.Helper()
	h := newHarness(t, Budget{}, replies...)
	var b strings.Builder
	for i := 1; i <= 300; i++ {
		fmt.Fprintf(&b, "line %03d of the big file, long enough to count\n", i)
	}
	if err := os.WriteFile(filepath.Join(h.ws.Root(), "big.txt"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	h.sess.cfg.FitWindow = true
	h.script.Window = window
	return h
}

// cutMarker is the in-band marker of a cut, or "" when the result was not cut.
func cutMarker(result string) string {
	i := strings.Index(result, "\n[… cut ")
	if i < 0 {
		return ""
	}
	j := strings.Index(result[i:], "…]\n")
	return result[i : i+j+len("…]\n")]
}

func TestFitWindowCutsAResultToTheRoomLeft(t *testing.T) {
	h := fitHarness(t, fitUsed+400, goinfertest.Calls("", "t1", "read", bigRead), goinfertest.Says("ok"))
	h.sess.Turn(context.Background(), "read it")
	fin := h.finished("t1")
	m := cutMarker(fin.Result)
	if !fin.Truncated || m == "" || !strings.Contains(m, "to fit the window") {
		t.Fatalf("not cut to the window: truncated %v, %q", fin.Truncated, fin.Result)
	}
	if kept := len(fin.Result) - len(m); kept != 400*fitMaxRate {
		t.Fatalf("kept %d bytes of output, want the room left, %d", kept, 400*int(fitMaxRate))
	}
	full, err := h.st.Blobs.Get(fin.Blob)
	if err != nil || !strings.Contains(string(full), "line 150 of") {
		t.Fatalf("the full output is not in the blob store: %v", err)
	}
	h.checkRebuild(t)
}

func TestFitWindowPassesAResultThatFits(t *testing.T) {
	h := fitHarness(t, 16384, goinfertest.Calls("", "t1", "read", bigRead), goinfertest.Says("ok"))
	h.sess.Turn(context.Background(), "read it")
	fin := h.finished("t1")
	if fin.Truncated || fin.Blob != "" || !strings.Contains(fin.Result, "line 001 of") || !strings.Contains(fin.Result, "line 300 of") {
		t.Fatalf("a result that fits was changed: truncated %v, blob %q", fin.Truncated, fin.Blob)
	}
}

func TestFitWindowNeverExceedsTheResultCap(t *testing.T) {
	h := fitHarness(t, 16384, goinfertest.Calls("", "t1", "read", bigRead), goinfertest.Says("ok"))
	h.sess.cfg.MaxResultBytes = 2000
	h.sess.Turn(context.Background(), "read it")
	fin := h.finished("t1")
	m := cutMarker(fin.Result)
	if kept := len(fin.Result) - len(m); m == "" || kept != 2000 || !strings.Contains(m, "over the 2000-byte result cap") {
		t.Fatalf("kept %d bytes with marker %q; want 2000 and the cap named", kept, m)
	}
}

func TestFitWindowNearlyFullIsANoteNotASliver(t *testing.T) {
	h := fitHarness(t, fitUsed+100, goinfertest.Calls("", "t1", "read", bigRead), goinfertest.Says("ok"))
	h.sess.Turn(context.Background(), "read it")
	fin := h.finished("t1")
	if !strings.HasPrefix(fin.Result, "[window nearly full: this output (") || strings.Contains(fin.Result, "line 001") ||
		!strings.Contains(fin.Result, "300 lines) is not shown. Finish up: give your answer now.]") {
		t.Fatalf("with %d bytes of room the result is %q", 100*int(fitMaxRate), fin.Result)
	}
	if full, err := h.st.Blobs.Get(fin.Blob); err != nil || !strings.Contains(string(full), "line 300 of") {
		t.Fatalf("the full output is not in the blob store: %v", err)
	}
}

// TestFitWindowKeepsAShortResultWhole: with no room left, a result under fitMinBytes still passes whole; only a longer
// one becomes the note.
func TestFitWindowKeepsAShortResultWhole(t *testing.T) {
	h := fitHarness(t, fitUsed, goinfertest.Calls("", "t1", "grep", `{"pattern":"func applyTokenBudget"}`), goinfertest.Says("ok"))
	h.sess.Turn(context.Background(), "find it")
	fin := h.finished("t1")
	if fin.Truncated || !strings.Contains(fin.Result, "mcp/token_budget.go:3") {
		t.Fatalf("a short result was not passed whole: %q", fin.Result)
	}
}

// TestFitWindowMarksEveryCut: a cut says how much it cut, of how much, in bytes and lines, how to narrow it, and the
// handle that pages the rest when read_output is there.
func TestFitWindowMarksEveryCut(t *testing.T) {
	h := fitHarness(t, fitUsed+400, goinfertest.Calls("", "t1", "read", bigRead), goinfertest.Says("ok"))
	h.sess.cfg.Tools = tools.New(tools.Options{Blobs: h.st.Blobs})
	h.sess.Turn(context.Background(), "read it")
	fin := h.finished("t1")
	full, _ := h.st.Blobs.Get(fin.Blob)
	m := cutMarker(fin.Result)
	kept := len(fin.Result) - len(m)
	for _, want := range []string{
		fmt.Sprintf("cut %d of %d bytes (", len(full)-kept, len(full)),
		fmt.Sprintf(" of %d lines) to fit the window", lineCount(string(full))),
		"narrow it with grep, or read a line range with offset and limit",
		fmt.Sprintf("read_output with handle %q pages the rest", fin.Blob),
	} {
		if !strings.Contains(m, want) {
			t.Errorf("marker %q lacks %q", m, want)
		}
	}
}

// TestFitWindowResultsShareTheRoom: two results in one response take the room in turn, so the second meets what the
// first left. Here the first is cut to the room and the second becomes the note.
func TestFitWindowResultsShareTheRoom(t *testing.T) {
	h := fitHarness(t, fitUsed+400, goinfertest.Calls("", "t1", "read", bigRead, "t2", "read", bigRead), goinfertest.Says("ok"))
	h.sess.Turn(context.Background(), "read it twice")
	fins := []proto.ToolFinished{h.finished("t1"), h.finished("t2")}
	if cutMarker(fins[0].Result) == "" || !strings.HasPrefix(fins[1].Result, "[window nearly full") {
		t.Fatalf("first %q…, second %q", fins[0].Result[:40], fins[1].Result)
	}
}

// TestFitWindowOffIsUnchanged: with FitWindow off, a long result is cut exactly as before it existed.
func TestFitWindowOffIsUnchanged(t *testing.T) {
	h := fitHarness(t, fitUsed+100, goinfertest.Calls("", "t1", "read", bigRead), goinfertest.Says("ok"))
	h.sess.cfg.FitWindow = false
	h.sess.cfg.MaxResultBytes = 40
	h.sess.Turn(context.Background(), "read it")
	fin := h.finished("t1")
	b, _ := h.st.Blobs.Get(fin.Blob)
	full := string(b)
	want := full[:30] + fmt.Sprintf("\n[… %d bytes omitted …]\n", len(full)-40) + full[len(full)-10:]
	if fin.Result != want {
		t.Fatalf("off, the result is\n%q\nwant\n%q", fin.Result, want)
	}
}

// windowSource is a models.Source that only says its window, for fitRoom.
type windowSource struct{ models.Source }

func (windowSource) Window() int { return 10000 }

// TestFitRoomScalesTheMeasuredRate: the request's bytes per token is scaled by fitRateScale, then held to
// [fitMinRate, fitMaxRate].
func TestFitRoomScalesTheMeasuredRate(t *testing.T) {
	s := &Session{cfg: Config{Source: windowSource{}}, conv: &server.Conversation{MaxTokens: 1000}}
	left := 10000 - 2000 - 500 - 1000 - fitMarginTokens // window, prompt, output, max_tokens, margin
	for _, c := range []struct {
		reqBytes int
		rate     float64
	}{
		{8000, 0.85 * 4}, // 4 bytes per token measured, 3.4 used
		{2000, fitMinRate},
		{40000, fitMaxRate},
	} {
		room, ok := s.fitRoom(proto.Usage{Prompt: 2000, Output: 500}, c.reqBytes)
		if want := int(float64(left) * c.rate); !ok || room != want {
			t.Errorf("request of %d bytes: room %d, %v; want %d", c.reqBytes, room, ok, want)
		}
	}
}

// TestFitWindowNoteIsNotAFailure: the nearly-full note keeps the tool's outcome, so notes in a row do not trip the
// consecutive-failure budget.
func TestFitWindowNoteIsNotAFailure(t *testing.T) {
	b := testBudget
	b.MaxErrors = 2
	h := fitHarness(t, fitUsed+100,
		goinfertest.Calls("", "t1", "read", bigRead), goinfertest.Calls("", "t2", "read", bigRead),
		goinfertest.Calls("", "t3", "read", bigRead), goinfertest.Says("ok"))
	h.sess.cfg.Budget = b
	stop, err := h.sess.Turn(context.Background(), "read it three times")
	if err != nil || stop != proto.StopEndTurn {
		t.Fatalf("stop %q, %v: three notes ended the turn", stop, err)
	}
	for _, id := range []string{"t1", "t2", "t3"} {
		if f := h.finished(id); f.Outcome != proto.OutcomeOK || !strings.HasPrefix(f.Result, "[window nearly full") {
			t.Fatalf("%s: outcome %q, %q", id, f.Outcome, f.Result)
		}
	}
}

// cliffHarness is fitHarness with 600 bytes of room per response, so the 14 KB bigRead gets the nearly-full note, and
// small.txt (15 lines, under 1 KB, so it passes whole) for a whole read before it.
func cliffHarness(t *testing.T, on bool, replies ...goinfertest.Reply) *harness {
	t.Helper()
	h := fitHarness(t, fitUsed+100, replies...)
	var b strings.Builder
	for i := 1; i <= 15; i++ {
		fmt.Fprintf(&b, "small %02d of a short file, long enough to count\n", i)
	}
	os.WriteFile(filepath.Join(h.ws.Root(), "small.txt"), []byte(b.String()), 0o644)
	h.sess.cfg.CompactAtCliff = on
	return h
}

// At the cliff: request 6's read gets the nearly-full note, so before request 7 a compaction stubs what is older than
// the last 3 requests (requests 1-3) and in scope: the grep (request 1) and the whole read of small.txt (request 3).
// It keeps the range read of big.txt (request 2), which is old and large, so only the range rule keeps it. The event's
// trigger is "cliff", and the log rebuilds the request. Off, or with room enough that no note is given, nothing is
// compacted.
func TestCompactAtCliff(t *testing.T) {
	replies := func() []goinfertest.Reply {
		return []goinfertest.Reply{
			goinfertest.Calls("", "g1", "grep", `{"pattern":"line 00"}`),
			goinfertest.Calls("", "r2", "read", `{"path":"big.txt","offset":1,"limit":15}`),
			goinfertest.Calls("", "r3", "read", `{"path":"small.txt"}`),
			goinfertest.Calls("", "l4", "glob", `{"pattern":"*.txt"}`),
			goinfertest.Calls("", "r5", "read", `{"path":"small.txt","offset":1,"limit":3}`),
			goinfertest.Calls("", "b6", "read", bigRead),
			goinfertest.Says("ok"),
		}
	}
	h := cliffHarness(t, true, replies()...)
	if _, err := h.sess.Turn(context.Background(), "explore"); err != nil {
		t.Fatal(err)
	}
	if f := h.finished("b6"); !strings.HasPrefix(f.Result, "[window nearly full") {
		t.Fatalf("request 6's read did not get the note: %.80q", f.Result)
	}
	if f := h.finished("r2"); len(f.Result) <= maskMinBytes {
		t.Fatalf("the range read is %d bytes; the test needs it over %d", len(f.Result), maskMinBytes)
	}
	var comps []proto.Compaction
	for _, p := range h.payloads(proto.EvCompaction) {
		comps = append(comps, p.(proto.Compaction))
	}
	ids := func(c proto.Compaction) string {
		var s []string
		for _, m := range c.Masked {
			s = append(s, m.ID)
		}
		return strings.Join(s, ",")
	}
	if len(comps) != 1 || comps[0].Trigger != "cliff" || comps[0].Request != 7 || ids(comps[0]) != "g1,r3" {
		t.Fatalf("compactions %+v", comps)
	}
	if last := string(h.script.Requests[6]); strings.Contains(last, "big.txt:1:line 001") || !strings.Contains(last, "this whole read of small.txt (request 3") {
		t.Fatalf("request 7 still carries the grep output or lacks the stub:\n%.400s", last)
	}
	h.checkRebuild(t)

	off := cliffHarness(t, false, replies()...)
	if _, err := off.sess.Turn(context.Background(), "explore"); err != nil {
		t.Fatal(err)
	}
	roomy := cliffHarness(t, true, replies()...)
	roomy.script.Window = 100_000
	if _, err := roomy.sess.Turn(context.Background(), "explore"); err != nil {
		t.Fatal(err)
	}
	if len(off.script.Requests) != 7 || len(roomy.script.Requests) != 7 {
		t.Fatalf("the negative turns made %d and %d requests, want 7 each", len(off.script.Requests), len(roomy.script.Requests))
	}
	if n, m := len(off.payloads(proto.EvCompaction)), len(roomy.payloads(proto.EvCompaction)); n != 0 || m != 0 {
		t.Fatalf("off: %d compactions; with room and no note: %d; want 0 and 0", n, m)
	}
}

// React when stuck, and the arming. Six greps (requests 1-6), then the same read at the cliff 9 times (requests
// 7-15, the 9th an 8th unchanged repeat). The first note compacts once as "cliff" (requests 1-4). The 8th repeat would
// end the turn; instead a "stuck" compaction stubs requests 5-6 and the turn goes on. After a new grep and 9 more of the
// read, a second "stuck" compaction stubs it. After a third grep and 9 more, the cap is reached, and the turn ends with
// repeat_loop at request 35. No second "cliff" compaction runs, though every later read also gets the note.
func TestCompactAtCliffReactsWhenStuck(t *testing.T) {
	grep := func(id, pat string) goinfertest.Reply {
		return goinfertest.Calls("", id, "grep", `{"pattern":"`+pat+`"}`)
	}
	var replies []goinfertest.Reply
	for i, p := range []string{"line 00", "line 01", "line 02", "line 03", "line 04", "line 05"} {
		replies = append(replies, grep(fmt.Sprintf("g%d", i+1), p))
	}
	reads := func(tag string) {
		for i := 0; i < 9; i++ {
			replies = append(replies, goinfertest.Calls("", fmt.Sprintf("%s%d", tag, i), "read", bigRead))
		}
	}
	reads("a")
	replies = append(replies, grep("g7", "line 06"))
	reads("b")
	replies = append(replies, grep("g8", "line 07"))
	reads("c")
	replies = append(replies, goinfertest.Says("never reached"))
	h := cliffHarness(t, true, replies...)
	h.sess.cfg.StopRepeats = true
	h.sess.cfg.Budget.MaxRequests, h.sess.cfg.Budget.MaxToolCalls = 60, 60
	stop, err := h.sess.Turn(context.Background(), "explore")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range h.payloads(proto.EvCompaction) {
		c := p.(proto.Compaction)
		var ids []string
		for _, m := range c.Masked {
			ids = append(ids, m.ID)
		}
		got = append(got, fmt.Sprintf("%s@%d:%s", c.Trigger, c.Request, strings.Join(ids, ",")))
	}
	want := "cliff@8:g1,g2,g3,g4 stuck@16:g5,g6 stuck@26:g7"
	if strings.Join(got, " ") != want || stop != proto.StopRepeatLoop || len(h.script.Requests) != 35 {
		t.Fatalf("compactions %q, want %q; stop %s after %d requests, want repeat_loop after 35", got, want, stop, len(h.script.Requests))
	}
	h.checkRebuild(t)
}

// A repeat away from the cliff (the call gets its whole result each time) still ends the turn with repeat_loop, as
// without the setting, and compacts nothing, though an old, large grep is there to stub.
func TestCompactAtCliffKeepsTheRepeatRuleAwayFromTheCliff(t *testing.T) {
	var replies []goinfertest.Reply
	replies = append(replies, goinfertest.Calls("", "g0", "grep", `{"pattern":"line 00"}`)) // old and large: stubbable if it compacted
	for i := 0; i < 9; i++ {
		replies = append(replies, goinfertest.Calls("", fmt.Sprintf("r%d", i), "read", `{"path":"small.txt","offset":1,"limit":3}`))
	}
	h := cliffHarness(t, true, replies...)
	h.script.Window = 100_000
	h.sess.cfg.StopRepeats = true
	h.sess.cfg.Budget.MaxRequests, h.sess.cfg.Budget.MaxToolCalls = 20, 20
	stop, err := h.sess.Turn(context.Background(), "x")
	if err != nil || stop != proto.StopRepeatLoop || len(h.script.Requests) != 10 || len(h.payloads(proto.EvCompaction)) != 0 {
		t.Fatalf("stop %s, err %v, %d requests, %d compactions; want repeat_loop at 10 and none", stop, err, len(h.script.Requests), len(h.payloads(proto.EvCompaction)))
	}
}

// Progressive widening: the same whole read of a 700-line file three times shows 200, 600, then all 700 lines, and
// the same grep over 400 matches shows 100, 300, then all 400. Each widening step shows something new, so it starts the
// repeat count again. With the setting off, each call shows the base cap, and the repeats are counted.
func TestCompactAtCliffWidensARepeatedCall(t *testing.T) {
	for _, on := range []bool{true, false} {
		replies := []goinfertest.Reply{}
		for i := 0; i < 3; i++ {
			replies = append(replies, goinfertest.Calls("", fmt.Sprintf("r%d", i), "read", `{"path":"wide.txt"}`))
		}
		for i := 0; i < 3; i++ {
			replies = append(replies, goinfertest.Calls("", fmt.Sprintf("g%d", i), "grep", `{"pattern":"^hit ","path":"hits.txt"}`))
		}
		replies = append(replies, goinfertest.Says("ok"))
		h := cliffHarness(t, on, replies...)
		h.script.Window = 1_000_000
		h.sess.cfg.ResultNotes, h.sess.cfg.MaxResultBytes = true, 1<<20
		var wide, hits strings.Builder
		for i := 1; i <= 700; i++ {
			fmt.Fprintf(&wide, "w %d\n", i)
		}
		for i := 1; i <= 400; i++ {
			fmt.Fprintf(&hits, "hit %d\n", i)
		}
		os.WriteFile(filepath.Join(h.ws.Root(), "wide.txt"), []byte(wide.String()), 0o644)
		os.WriteFile(filepath.Join(h.ws.Root(), "hits.txt"), []byte(hits.String()), 0o644)
		if _, err := h.sess.Turn(context.Background(), "x"); err != nil {
			t.Fatal(err)
		}
		lines := func(id string) int { return strings.Count(h.finished(id).Result, "\tw ") }
		matches := func(id string) int { return strings.Count(h.finished(id).Result, "hits.txt:") }
		want := map[bool][6]int{true: {200, 600, 700, 100, 300, 400}, false: {200, 200, 200, 100, 100, 100}}[on]
		got := [6]int{lines("r0"), lines("r1"), lines("r2"), matches("g0"), matches("g1"), matches("g2")}
		if got != want {
			t.Errorf("on %v: shown %v, want %v", on, got, want)
		}
		// Repeats count on what was shown: on, each widening step shows something new, so no repeat is noted; off,
		// the same output is shown, and the notes count repeat 1 and 2.
		for id, rep := range map[string]string{"r1": "repeat 1", "r2": "repeat 2", "g2": "repeat 2"} {
			if strings.Contains(h.finished(id).Result, rep) == on {
				t.Errorf("on %v: %s's note counting %s is %v", on, id, rep, !on)
			}
		}
	}
}

// Widening needs only compact_at_cliff: with stop_repeats and result_notes off, the same whole read three times shows
// 200, 600, then all 700 lines.
func TestCompactAtCliffWidensAlone(t *testing.T) {
	var replies []goinfertest.Reply
	for i := 0; i < 3; i++ {
		replies = append(replies, goinfertest.Calls("", fmt.Sprintf("r%d", i), "read", `{"path":"wide.txt"}`))
	}
	replies = append(replies, goinfertest.Says("ok"))
	h := cliffHarness(t, true, replies...)
	h.script.Window = 1_000_000
	h.sess.cfg.StopRepeats, h.sess.cfg.ResultNotes, h.sess.cfg.MaxResultBytes = false, false, 1<<20
	var wide strings.Builder
	for i := 1; i <= 700; i++ {
		fmt.Fprintf(&wide, "w %d\n", i)
	}
	os.WriteFile(filepath.Join(h.ws.Root(), "wide.txt"), []byte(wide.String()), 0o644)
	if _, err := h.sess.Turn(context.Background(), "x"); err != nil {
		t.Fatal(err)
	}
	lines := func(id string) int { return strings.Count(h.finished(id).Result, "\tw ") }
	if got := [3]int{lines("r0"), lines("r1"), lines("r2")}; got != [3]int{200, 600, 700} {
		t.Fatalf("shown %v, want [200 600 700]", got)
	}
}

// callsAt is a scripted reply with the given calls (id, name, input triples) that reports a prompt of the given tokens.
func callsAt(prompt int, calls ...string) goinfertest.Reply {
	var bs []proto.ModelBlock
	for i := 0; i+2 < len(calls); i += 3 {
		bs = append(bs, proto.ModelBlock{Type: "tool_use", ID: calls[i], Name: calls[i+1], Input: json.RawMessage(calls[i+2])})
	}
	return goinfertest.Reply{Body: goinfertest.SSEWithReuse("tool_use", prompt, 0, 1, bs...)}
}

// Several calls in one response: when two calls reach the repeat limit together, one at the cliff and one away from it,
// the turn ends with repeat_loop, and no stuck compaction runs, though old greps (requests 5 and 6) are there to stub.
func TestCompactAtCliffEndsOnAnyLoopAwayFromTheCliff(t *testing.T) {
	var replies []goinfertest.Reply
	for i := 0; i < 6; i++ { // requests 1-6: the cliff stubs 1-4; 5 and 6 are left for a stuck compaction
		replies = append(replies, goinfertest.Calls("", fmt.Sprintf("g%d", i), "grep", fmt.Sprintf(`{"pattern":"line 0%d"}`, i)))
	}
	for i := 0; i < 9; i++ {
		replies = append(replies, goinfertest.Calls("", fmt.Sprintf("b%d", i), "read", bigRead,
			fmt.Sprintf("s%d", i), "read", `{"path":"small.txt","offset":1,"limit":3}`))
	}
	h := cliffHarness(t, true, replies...)
	h.sess.cfg.StopRepeats = true
	h.sess.cfg.Budget.MaxRequests, h.sess.cfg.Budget.MaxToolCalls = 30, 30
	stop, err := h.sess.Turn(context.Background(), "x")
	stuck := 0
	for _, p := range h.payloads(proto.EvCompaction) {
		if p.(proto.Compaction).Trigger == "stuck" {
			stuck++
		}
	}
	if err != nil || stop != proto.StopRepeatLoop || len(h.script.Requests) != 15 || stuck != 0 {
		t.Fatalf("stop %s, err %v, %d requests, %d stuck compactions; want repeat_loop at 15 and none", stop, err, len(h.script.Requests), stuck)
	}
}

// Order at the cliff: when a repeat loop reaches the limit on note-answered calls and the turn's cliff compaction has not
// yet found anything to stub, the cliff compaction runs there, not a stuck one, and the turn goes on. The same read gets
// the note 9 times (the 9th its 8th unchanged repeat). A grep in request 6 is the only thing to stub, and it is old
// enough (older than the last 3 requests) only before request 10. So the compactions tried before requests 7-9 find
// nothing, and the one at the limit is a cliff compaction of the grep.
func TestCompactAtCliffRunsTheCliffFirst(t *testing.T) {
	var replies []goinfertest.Reply
	for i := 0; i < 9; i++ {
		calls := []string{fmt.Sprintf("b%d", i), "read", bigRead}
		if i == 5 {
			calls = append(calls, "g5", "grep", `{"pattern":"line 00"}`)
		}
		replies = append(replies, goinfertest.Calls("", calls...))
	}
	replies = append(replies, goinfertest.Says("ok"))
	h := cliffHarness(t, true, replies...)
	h.sess.cfg.StopRepeats = true
	h.sess.cfg.Budget.MaxRequests, h.sess.cfg.Budget.MaxToolCalls = 30, 30
	stop, err := h.sess.Turn(context.Background(), "x")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range h.payloads(proto.EvCompaction) {
		c := p.(proto.Compaction)
		got = append(got, fmt.Sprintf("%s@%d", c.Trigger, c.Request))
	}
	if strings.Join(got, ",") != "cliff@10" || stop != proto.StopEndTurn || h.sess.stuckDone != 0 || len(h.script.Requests) != 10 {
		t.Fatalf("compactions %v, stop %s, stuck %d, %d requests; want cliff@10, the turn going on to end_turn at 10, no stuck", got, stop, h.sess.stuckDone, len(h.script.Requests))
	}
}

// With mask_observations on too, a cliff compaction moves its regrowth floor: right after it, a window compaction is
// not due though the prompt is past 75%; once the prompt has grown 10% of the window past the first prompt after it,
// it is.
func TestCliffCompactionMovesTheMaskFloor(t *testing.T) {
	h := cliffHarness(t, true, goinfertest.Says("ok"))
	if _, err := h.sess.Turn(context.Background(), "x"); err != nil { // the source learns the window from a request
		t.Fatal(err)
	}
	h.sess.cfg.MaskObservations = true
	w := h.sess.cfg.Source.Window()
	if w <= 0 {
		t.Fatalf("window %d", w)
	}
	h.sess.track(observe("g", "grep", []byte(`{"pattern":"x"}`), 1, strings.Repeat("y", 600), true), false, 0)
	h.sess.noteUsage(w * 80 / 100)
	if !h.sess.due(w) {
		t.Fatal("before any compaction a window compaction is not due at 80%")
	}
	if m, err := h.sess.compactNow(9, 0, "cliff"); err != nil || m != 1 {
		t.Fatalf("the cliff compaction masked %d, %v", m, err)
	}
	h.sess.noteUsage(w * 80 / 100) // the first prompt after it: the floor
	if h.sess.due(w) {
		t.Fatal("a window compaction is due right after a cliff compaction, without regrowth")
	}
	h.sess.noteUsage(w * 91 / 100)
	if !h.sess.due(w) {
		t.Fatal("a window compaction is not due after 11% regrowth")
	}
}

// After the cliff, the first shown result starts the repeat count again. The same read gets the nearly-full note 4
// times, then, with room, is shown 5 times: 9 calls, but the 5 shown ones are only 4 unchanged repeats of each other,
// so stop_repeats does not end the turn (counting the notes and the shown results as one output would, at the 9th).
func TestCompactAtCliffShownResultStartsTheCountAgain(t *testing.T) {
	const window = 100_000
	read := `{"path":"small.txt","offset":1,"limit":40}`
	var replies []goinfertest.Reply
	for i := 0; i < 4; i++ {
		replies = append(replies, callsAt(window-300, fmt.Sprintf("n%d", i), "read", read))
	}
	for i := 0; i < 5; i++ {
		replies = append(replies, callsAt(100, fmt.Sprintf("s%d", i), "read", read))
	}
	replies = append(replies, goinfertest.Says("ok"))
	h := cliffHarness(t, true, replies...)
	h.script.Window = window
	h.sess.cfg.StopRepeats = true
	h.sess.cfg.Budget.MaxRequests, h.sess.cfg.Budget.MaxToolCalls = 30, 30
	var b strings.Builder
	for i := 1; i <= 40; i++ {
		fmt.Fprintf(&b, "small %02d of a file now long enough to need room\n", i)
	}
	os.WriteFile(filepath.Join(h.ws.Root(), "small.txt"), []byte(b.String()), 0o644)
	stop, err := h.sess.Turn(context.Background(), "x")
	if err != nil || stop != proto.StopEndTurn || len(h.script.Requests) != 10 {
		t.Fatalf("stop %s, err %v, %d requests; want end_turn after 10", stop, err, len(h.script.Requests))
	}
	if !strings.HasPrefix(h.finished("n3").Result, "[window nearly full") || strings.HasPrefix(h.finished("s0").Result, "[window nearly full") {
		t.Fatal("the script did not give 4 notes and then room")
	}
}

// A note neither advances widening nor keeps it, and widening works through redaction. The same whole read of a
// 700-line file (one line holding an AWS key id, which redaction replaces) is first answered with the note, then shown
// at 200 lines (not 600), then 600, then all 700.
func TestCompactAtCliffWideningStartsAfterTheNote(t *testing.T) {
	const window = 100_000
	read := `{"path":"wide.txt"}`
	replies := []goinfertest.Reply{callsAt(window-300, "n0", "read", read)}
	for i := 0; i < 3; i++ {
		replies = append(replies, callsAt(100, fmt.Sprintf("s%d", i), "read", read))
	}
	replies = append(replies, goinfertest.Says("ok"))
	h := cliffHarness(t, true, replies...)
	h.script.Window = window
	h.sess.cfg.MaxResultBytes = 1 << 20
	var wide strings.Builder
	for i := 1; i <= 700; i++ {
		if i == 5 {
			wide.WriteString("w key=AKIAIOSFODNN7EXAMPLE\n")
			continue
		}
		fmt.Fprintf(&wide, "w %d\n", i)
	}
	os.WriteFile(filepath.Join(h.ws.Root(), "wide.txt"), []byte(wide.String()), 0o644)
	if _, err := h.sess.Turn(context.Background(), "x"); err != nil {
		t.Fatal(err)
	}
	lines := func(id string) int { return strings.Count(h.finished(id).Result, "\tw ") }
	if !strings.HasPrefix(h.finished("n0").Result, "[window nearly full") {
		t.Fatalf("the first read did not get the note: %.80q", h.finished("n0").Result)
	}
	if got := [3]int{lines("s0"), lines("s1"), lines("s2")}; got != [3]int{200, 600, 700} {
		t.Fatalf("after the note, shown %v, want [200 600 700]", got)
	}
	if strings.Contains(h.finished("s2").Result, "AKIAIOSFODNN7EXAMPLE") {
		t.Fatal("the widened result was not redacted")
	}
}

// The cliff note is per turn: a turn that ends on its budget right after a note leaves no cliff compaction for the next
// turn's first request.
func TestCompactAtCliffNoteDoesNotCrossTurns(t *testing.T) {
	var replies []goinfertest.Reply
	for i := 0; i < 4; i++ {
		replies = append(replies, goinfertest.Calls("", fmt.Sprintf("g%d", i), "grep", fmt.Sprintf(`{"pattern":"line 0%d"}`, i)))
	}
	replies = append(replies, goinfertest.Calls("", "b0", "read", bigRead), goinfertest.Says("ok"))
	h := cliffHarness(t, true, replies...)
	h.sess.cfg.Budget.MaxRequests = 5 // the turn trips its request budget right after the note
	if stop, err := h.sess.Turn(context.Background(), "first"); err != nil || stop == proto.StopEndTurn {
		t.Fatalf("the first turn: stop %s, err %v; want a budget stop", stop, err)
	}
	if !strings.HasPrefix(h.finished("b0").Result, "[window nearly full") {
		t.Fatal("the first turn's last read did not get the note")
	}
	if _, err := h.sess.Turn(context.Background(), "second"); err != nil {
		t.Fatal(err)
	}
	if n := len(h.payloads(proto.EvCompaction)); n != 0 {
		t.Fatalf("%d compactions: the first turn's note compacted in the second", n)
	}
}

// Widening restarts the repeat count at most twice: the same whole read of a 700-line file, with stop_repeats, is
// shown at 200, 600, then all 700 lines, and its 8th unchanged repeat of the whole file (the 11th call) ends the turn
// with repeat_loop. Off, the 9th call would.
func TestCompactAtCliffRepeatRuleStillEndsAWidenedLoop(t *testing.T) {
	for on, want := range map[bool]int{true: 11, false: 9} {
		var replies []goinfertest.Reply
		for i := 0; i < 12; i++ {
			replies = append(replies, goinfertest.Calls("", fmt.Sprintf("r%d", i), "read", `{"path":"wide.txt"}`))
		}
		h := cliffHarness(t, on, replies...)
		h.script.Window = 1_000_000
		h.sess.cfg.StopRepeats, h.sess.cfg.MaxResultBytes = true, 1<<20
		h.sess.cfg.Budget.MaxRequests, h.sess.cfg.Budget.MaxToolCalls = 20, 20
		var wide strings.Builder
		for i := 1; i <= 700; i++ {
			fmt.Fprintf(&wide, "w %d\n", i)
		}
		os.WriteFile(filepath.Join(h.ws.Root(), "wide.txt"), []byte(wide.String()), 0o644)
		stop, err := h.sess.Turn(context.Background(), "x")
		if err != nil || stop != proto.StopRepeatLoop || len(h.script.Requests) != want {
			t.Errorf("on %v: stop %s, err %v, %d requests; want repeat_loop at %d", on, stop, err, len(h.script.Requests), want)
		}
	}
}

// A result cut to fit is not shown whole, so it ends the widening chain: with a 1 KB result cap, every read of the
// 700-line file is cut, and the stored whole output of each (its blob) stays 200 lines rather than widening.
func TestCompactAtCliffCutResultEndsTheChain(t *testing.T) {
	var replies []goinfertest.Reply
	for i := 0; i < 3; i++ {
		replies = append(replies, goinfertest.Calls("", fmt.Sprintf("r%d", i), "read", `{"path":"wide.txt"}`))
	}
	replies = append(replies, goinfertest.Says("ok"))
	h := cliffHarness(t, true, replies...)
	h.script.Window = 1_000_000
	h.sess.cfg.MaxResultBytes = 1 << 10
	var wide strings.Builder
	for i := 1; i <= 700; i++ {
		fmt.Fprintf(&wide, "w %d\n", i)
	}
	os.WriteFile(filepath.Join(h.ws.Root(), "wide.txt"), []byte(wide.String()), 0o644)
	if _, err := h.sess.Turn(context.Background(), "x"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"r0", "r1", "r2"} {
		f := h.finished(id)
		full, err := h.st.Blobs.Get(f.Blob)
		if !f.Truncated || err != nil || strings.Count(string(full), "\tw ") != 200 {
			t.Errorf("%s: cut %v, blob err %v, %d lines in its whole output; want cut, 200", id, f.Truncated, err, strings.Count(string(full), "\tw "))
		}
	}
}
