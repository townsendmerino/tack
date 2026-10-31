package loop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
)

// maskHarness is newHarness with big.txt (about 14 KB) in the workspace, MaskObservations as given and the served
// window set. The scripted server reports a 100-token prompt, so a window of 120 is past maskShare from the first reply.
func maskHarness(t *testing.T, on bool, window int, replies ...goinfertest.Reply) *harness {
	t.Helper()
	h := newHarness(t, Budget{}, replies...)
	var b strings.Builder
	for i := 1; i <= 300; i++ {
		fmt.Fprintf(&b, "line %03d of the big file, long enough to count\n", i)
	}
	if err := os.WriteFile(filepath.Join(h.ws.Root(), "big.txt"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	h.sess.cfg.MaskObservations = on
	h.script.Window = window
	return h
}

// Past the threshold, a read superseded by a later read of the same file is sent as a stub from the next request on;
// the later read stays whole; the compaction is an event naming what it replaced; and the log rebuilds the request.
func TestMaskObservationsStubsASupersededRead(t *testing.T) {
	h := maskHarness(t, true, 120,
		goinfertest.Calls("", "t1", "read", bigRead), goinfertest.Calls("", "t2", "read", bigRead), goinfertest.Says("ok"))
	if _, err := h.sess.Turn(context.Background(), "read it twice"); err != nil {
		t.Fatal(err)
	}
	if n := len(h.script.Requests); n != 3 {
		t.Fatalf("%d requests, want 3", n)
	}
	second, third := string(h.script.Requests[1]), string(h.script.Requests[2])
	if strings.Count(second, "line 150 of the big file") != 1 || strings.Contains(second, "masked to fit the window") {
		t.Fatalf("request 2 should carry the first read whole, nothing masked")
	}
	if strings.Count(third, "line 150 of the big file") != 1 || !strings.Contains(third, "this read of big.txt from line 1, 300 lines (request 1") {
		t.Fatalf("request 3 should carry the first read as a stub and the second whole:\n%.600s", third)
	}
	var comps []proto.Compaction
	for _, p := range h.payloads(proto.EvCompaction) {
		comps = append(comps, p.(proto.Compaction))
	}
	if len(comps) != 1 || comps[0].Request != 3 || comps[0].Trigger != "window" || len(comps[0].Masked) != 1 ||
		comps[0].Masked[0].ID != "t1" || comps[0].After >= comps[0].Before {
		t.Fatalf("compactions %+v", comps)
	}
	h.checkRebuild(t)
}

// Off, or under the threshold, history is only appended to: both reads go whole and no compaction is recorded.
func TestMaskObservationsOffOrUnderTheThreshold(t *testing.T) {
	for name, c := range map[string]struct {
		on     bool
		window int
	}{"off": {false, 120}, "under the threshold": {true, 100_000}} {
		h := maskHarness(t, c.on, c.window,
			goinfertest.Calls("", "t1", "read", bigRead), goinfertest.Calls("", "t2", "read", bigRead), goinfertest.Says("ok"))
		h.sess.Turn(context.Background(), "read it twice")
		if third := string(h.script.Requests[2]); strings.Count(third, "line 150 of the big file") != 2 || len(h.payloads(proto.EvCompaction)) != 0 {
			t.Errorf("%s: something was masked", name)
		}
	}
}

// The rule on command output: a bash result older than the last maskKeepRequests requests and over maskMinBytes is
// masked, once; a recent one and a short one are kept.
func TestMaskStubsCommandOutput(t *testing.T) {
	long := strings.Repeat("output line\n", 40)
	s := &Session{obs: []observation{
		observe("old", "bash", []byte(`{"command":"go test ./..."}`), 1, long),
		observe("short", "bash", []byte(`{"command":"ls"}`), 1, "a\nb\n"),
		observe("recent", "bash", []byte(`{"command":"go build ./..."}`), 4, long),
	}}
	got := s.maskStubs(5)
	if len(got) != 1 || got[0].ID != "old" || !strings.Contains(got[0].Stub, "the output of `go test ./...` (request 1") {
		t.Fatalf("masked %+v", got)
	}
	if again := s.maskStubs(9); len(again) != 1 || again[0].ID != "recent" {
		t.Fatalf("a second compaction masked %+v, want only the newly old output", again)
	}
}
