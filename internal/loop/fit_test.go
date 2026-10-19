package loop

import (
	"context"
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
