package loop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

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
		observe("old", "bash", []byte(`{"command":"go test ./..."}`), 1, long, true),
		observe("short", "bash", []byte(`{"command":"ls"}`), 1, "a\nb\n", true),
		observe("recent", "bash", []byte(`{"command":"go build ./..."}`), 4, long, true),
	}}
	got := s.maskStubs(5)
	if len(got) != 1 || got[0].ID != "old" || !strings.Contains(got[0].Stub, "the output of `go test ./...` (request 1") {
		t.Fatalf("masked %+v", got)
	}
	if again := s.maskStubs(9); len(again) != 1 || again[0].ID != "recent" {
		t.Fatalf("a second compaction masked %+v, want only the newly old output", again)
	}
}

// One-shot: with the prompt staying past maskRearmShare, a compaction runs once, though a later read would supersede
// another; the rule re-arms only when the prompt falls below maskRearmShare.
func TestMaskObservationsIsOneShot(t *testing.T) {
	h := maskHarness(t, true, 120,
		goinfertest.Calls("", "t1", "read", bigRead), goinfertest.Calls("", "t2", "read", bigRead),
		goinfertest.Calls("", "t3", "read", bigRead), goinfertest.Says("ok"))
	h.sess.Turn(context.Background(), "read it three times")
	if n := len(h.payloads(proto.EvCompaction)); n != 1 {
		t.Fatalf("%d compactions, want 1 while the prompt stays past the re-arm share", n)
	}
	h.sess.noteUsage(10) // the prompt falls below maskRearmShare of 120
	if h.sess.maskSpent {
		t.Fatal("a prompt under the re-arm share did not re-arm the rule")
	}
	h.checkRebuild(t)
}

// The threshold, at its edge: the scripted prompt is 100 tokens, 75% of a 133-token window is 99.75, so it
// compacts; of a 134-token window it is 100.5, so it does not.
func TestMaskObservationsThresholdEdge(t *testing.T) {
	for window, want := range map[int]int{133: 1, 134: 0} {
		h := maskHarness(t, true, window,
			goinfertest.Calls("", "t1", "read", bigRead), goinfertest.Calls("", "t2", "read", bigRead), goinfertest.Says("ok"))
		h.sess.Turn(context.Background(), "read it twice")
		if n := len(h.payloads(proto.EvCompaction)); n != want {
			t.Errorf("window %d: %d compactions, want %d", window, n, want)
		}
	}
}

// Which reads are superseded: only a later successful read of the same file that returned every line the earlier did.
func TestMaskReadSuperseding(t *testing.T) {
	long := strings.Repeat("x", 400)
	read := func(id, input string, ok bool) observation {
		return observe(id, "read", []byte(input), 1, long, ok)
	}
	for name, c := range map[string]struct {
		earlier, later observation
		masked         bool
	}{
		"a whole read, read whole again":            {read("a", `{"path":"f.go"}`, true), read("b", `{"path":"f.go"}`, true), true},
		"a range inside a later wider range":        {read("a", `{"path":"f.go","offset":10,"limit":5}`, true), read("b", `{"path":"f.go","offset":1,"limit":50}`, true), true},
		"a whole read, then a partial one":          {read("a", `{"path":"f.go"}`, true), read("b", `{"path":"f.go","offset":1,"limit":20}`, true), false},
		"a later read that failed":                  {read("a", `{"path":"f.go"}`, true), read("b", `{"path":"f.go"}`, false), false},
		"another file":                              {read("a", `{"path":"f.go"}`, true), read("b", `{"path":"g.go"}`, true), false},
		"a range past line 200, then a whole read":  {read("a", `{"path":"f.go","offset":300,"limit":20}`, true), read("b", `{"path":"f.go"}`, true), false},
		"an offset with no limit is never compared": {read("a", `{"path":"f.go","offset":5}`, true), read("b", `{"path":"f.go","offset":1,"limit":500}`, true), false},
	} {
		s := &Session{obs: []observation{c.earlier, c.later}}
		got := s.maskStubs(2)
		if (len(got) == 1 && got[0].ID == "a") != c.masked {
			t.Errorf("%s: masked %+v, want the earlier read masked: %v", name, got, c.masked)
		}
	}
}

// The edges of the command-output rule and of a stub's worth: output of maskMinBytes is kept and one byte more is
// masked; output from exactly maskKeepRequests requests before the next is masked, and from one request later is kept;
// a stub not shorter than its result is never sent.
func TestMaskEdges(t *testing.T) {
	bash := func(id string, req, size int) observation {
		return observe(id, "bash", []byte(`{"command":"go test"}`), req, strings.Repeat("y", size), true)
	}
	s := &Session{obs: []observation{bash("at-min", 1, maskMinBytes), bash("over-min", 1, maskMinBytes+1), bash("edge", 6, 1000), bash("inside", 7, 1000)}}
	got := map[string]bool{}
	for _, m := range s.maskStubs(10) {
		got[m.ID] = true
	}
	if got["at-min"] || !got["over-min"] || !got["edge"] || got["inside"] {
		t.Fatalf("masked %v: want over-min and edge only", got)
	}
	longPath := strings.Repeat("d/", 200) + "f.go"
	r := func(id string) observation {
		return observe(id, "read", []byte(`{"path":"`+longPath+`"}`), 1, strings.Repeat("z", 350), true)
	}
	if s := (&Session{obs: []observation{r("a"), r("b")}}); len(s.maskStubs(2)) != 0 {
		t.Fatal("a stub longer than its 350-byte result was sent")
	}
}

// cut never splits a rune.
func TestMaskCutKeepsRunes(t *testing.T) {
	s := strings.Repeat("é", 100) // 2 bytes each
	for n := 1; n < 20; n++ {
		if got := cut(s, n); !utf8.ValidString(got) {
			t.Fatalf("cut(%d) split a rune: %q", n, got)
		}
	}
	if got := cut("short", 80); got != "short" {
		t.Fatalf("cut changed a short string: %q", got)
	}
}
