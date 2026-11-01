package loop

import (
	"context"
	"encoding/json"
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
	got := s.maskStubs(5, false)
	if len(got) != 1 || got[0].ID != "old" || !strings.Contains(got[0].Stub, "the output of `go test ./...` (request 1") {
		t.Fatalf("masked %+v", got)
	}
	if again := s.maskStubs(9, false); len(again) != 1 || again[0].ID != "recent" {
		t.Fatalf("a second compaction masked %+v, want only the newly old output", again)
	}
}

// readAt is a scripted reply that reads big.txt whole and reports a prompt of the given tokens.
func readAt(id string, prompt int) goinfertest.Reply {
	return goinfertest.Reply{Body: goinfertest.SSEWithReuse("tool_use", prompt, 0, 1,
		proto.ModelBlock{Type: "tool_use", ID: id, Name: "read", Input: json.RawMessage(bigRead)})}
}

// After a compaction the next waits for the prompt to grow maskRegrowShare of the window past the first prompt after
// it, though there is something to mask before then; it does not wait for the prompt to fall. Window 1000: request 3
// compacts at 800; the floor is request 3's 700; at 790 (request 4) it has grown 90, too little; at 810 (request 5) it
// has grown 110, so request 6 compacts. Every-request compaction would also compact before request 5; a rule that
// waits for the prompt to fall below a share would never compact again.
func TestMaskObservationsRegrows(t *testing.T) {
	h := maskHarness(t, true, 1000, readAt("t1", 800), readAt("t2", 800), readAt("t3", 700), readAt("t4", 790),
		readAt("t5", 810), goinfertest.Says("ok"))
	if _, err := h.sess.Turn(context.Background(), "read it five times"); err != nil {
		t.Fatal(err)
	}
	var at []int
	for _, p := range h.payloads(proto.EvCompaction) {
		at = append(at, p.(proto.Compaction).Request)
	}
	if fmt.Sprint(at) != "[3 6]" {
		t.Fatalf("compactions before requests %v, want [3 6]", at)
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
		s := &Session{}
		s.track(c.earlier, false)
		s.track(c.later, false)
		got := s.maskStubs(2, false)
		if (len(got) == 1 && got[0].ID == "a") != c.masked {
			t.Errorf("%s: masked %+v, want the earlier read masked: %v", name, got, c.masked)
		}
	}
}

// A read is superseded only by a later read of the same version of the file, returned whole: an edit or write of that
// file, or any call that may change files, between the two keeps the earlier read; an edit of another file, or a read-
// only call, does not; and a later read cut to fit the window covers nothing.
func TestMaskReadSupersedingVersions(t *testing.T) {
	long := strings.Repeat("x", 400)
	call := func(id, tool, input string) observation { return observe(id, tool, []byte(input), 1, long, true) }
	for name, c := range map[string]struct {
		between   []observation
		truncated bool
		masked    bool
	}{
		"nothing between":           {nil, false, true},
		"a later read cut to fit":   {nil, true, false},
		"an edit of the file":       {[]observation{call("e", "edit", `{"path":"f.go"}`)}, false, false},
		"a write of ./f.go":         {[]observation{call("e", "write", `{"path":"./f.go"}`)}, false, false},
		"a bash call":               {[]observation{call("e", "bash", `{"command":"gofmt -w ."}`)}, false, false},
		"an unknown tool":           {[]observation{call("e", "rewrite_all", `{}`)}, false, false},
		"an edit of another file":   {[]observation{call("e", "edit", `{"path":"g.go"}`)}, false, true},
		"a grep and a failed write": {[]observation{call("g", "grep", `{"pattern":"x"}`), observe("w", "write", []byte(`{"path":"g.go"}`), 1, "denied", false)}, false, true},
	} {
		s := &Session{}
		s.track(call("a", "read", `{"path":"f.go"}`), false)
		for _, o := range c.between {
			s.track(o, false)
		}
		s.track(call("b", "read", `{"path":"f.go"}`), c.truncated)
		got := s.maskStubs(2, false)
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
	for _, m := range s.maskStubs(10, false) {
		got[m.ID] = true
	}
	if got["at-min"] || !got["over-min"] || !got["edge"] || got["inside"] {
		t.Fatalf("masked %v: want over-min and edge only", got)
	}
	longPath := strings.Repeat("d/", 200) + "f.go"
	r := func(id string) observation {
		return observe(id, "read", []byte(`{"path":"`+longPath+`"}`), 1, strings.Repeat("z", 350), true)
	}
	if s := (&Session{obs: []observation{r("a"), r("b")}}); len(s.maskStubs(2, false)) != 0 {
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

// A command over several lines is one line in its stub.
func TestMaskCommandStubIsOneLine(t *testing.T) {
	s := &Session{obs: []observation{observe("m", "bash", []byte(`{"command":"cd pkg &&\n  go test ./..."}`), 1, strings.Repeat("o\n", 200), true)}}
	got := s.maskStubs(9, false)
	if len(got) != 1 || strings.Contains(got[0].Stub, "\n") || !strings.Contains(got[0].Stub, "`cd pkg && go test ./...`") {
		t.Fatalf("stubs %+v", got)
	}
}

// Through the loop: a later read cut to MaxResultBytes supersedes nothing, so past the threshold no compaction runs.
func TestMaskObservationsCutReadSupersedesNothing(t *testing.T) {
	h := maskHarness(t, true, 120,
		goinfertest.Calls("", "t1", "read", bigRead), goinfertest.Calls("", "t2", "read", bigRead), goinfertest.Says("ok"))
	h.sess.cfg.MaxResultBytes = 4000
	if _, err := h.sess.Turn(context.Background(), "read it twice"); err != nil {
		t.Fatal(err)
	}
	if n := len(h.payloads(proto.EvCompaction)); n != 0 {
		t.Fatalf("%d compactions: a read cut to the result cap superseded the earlier one", n)
	}
}

// Under a lean read tool (Config.ReadLines 80), a later whole read covers lines 1-80 only: an earlier range inside them
// is superseded, and one past line 80 is not.
func TestMaskReadSupersedingLean(t *testing.T) {
	long := strings.Repeat("x", 400)
	read := func(id, input string) observation { return observe(id, "read", []byte(input), 1, long, true) }
	for earlier, masked := range map[string]bool{
		`{"path":"f.go","offset":10,"limit":20}`:  true,
		`{"path":"f.go","offset":100,"limit":20}`: false,
	} {
		s := &Session{cfg: Config{ReadLines: 80}}
		s.track(read("a", earlier), false)
		s.track(read("b", `{"path":"f.go"}`), false)
		if got := s.maskStubs(2, false); (len(got) == 1) != masked {
			t.Errorf("%s then a whole read at 80 lines: masked %+v, want %v", earlier, got, masked)
		}
	}
}

// The other order: a whole read first, then a range. Under ReadLines 80 the whole read returned lines 1-80, so a later
// read of lines 1-100 covers it; under the default 200 it does not.
func TestMaskWholeReadThenARange(t *testing.T) {
	long := strings.Repeat("x", 400)
	read := func(id, input string) observation { return observe(id, "read", []byte(input), 1, long, true) }
	for readLines, masked := range map[int]bool{80: true, 0: false} {
		s := &Session{cfg: Config{ReadLines: readLines}}
		s.track(read("a", `{"path":"f.go"}`), false)
		s.track(read("b", `{"path":"f.go","offset":1,"limit":100}`), false)
		if got := s.maskStubs(2, false); (len(got) == 1 && got[0].ID == "a") != masked {
			t.Errorf("ReadLines %d: a whole read, then lines 1-100: masked %+v, want %v", readLines, got, masked)
		}
	}
}

// At the cliff (wide), grep and glob output and whole-file reads from before the last maskKeepRequests requests are
// stubbed, besides what mask_observations stubs; a read of a range, a recent grep, and a short output are not. Not at
// the cliff, none of them is.
func TestMaskStubsWideAtTheCliff(t *testing.T) {
	long := strings.Repeat("y", 600)
	obs := func() []observation {
		return []observation{
			observe("grep", "grep", []byte(`{"pattern":"func   Foo","path":"pkg"}`), 1, long, true),
			observe("glob", "glob", []byte(`{"pattern":"**/*.go"}`), 1, long, true),
			observe("whole", "read", []byte(`{"path":"a.go"}`), 2, long, true),
			observe("range", "read", []byte(`{"path":"b.go","offset":10,"limit":40}`), 2, long, true),
			observe("short", "grep", []byte(`{"pattern":"x"}`), 1, "a.go:1:x\n", true),
			observe("recent", "grep", []byte(`{"pattern":"Bar"}`), 6, long, true),
		}
	}
	got := map[string]string{}
	s := &Session{obs: obs()}
	for _, m := range s.maskStubs(9, true) {
		got[m.ID] = m.Stub
	}
	if len(got) != 3 || got["range"] != "" || got["short"] != "" || got["recent"] != "" ||
		!strings.Contains(got["grep"], "the output of grep for `func Foo in pkg` (request 1, 600 bytes). Run it again, narrower") ||
		!strings.Contains(got["glob"], "the output of glob for `**/*.go`") ||
		!strings.Contains(got["whole"], "this whole read of a.go (request 2, 600 bytes). Read a range with offset and limit") {
		t.Fatalf("wide stubs %q", got)
	}
	if narrow := (&Session{obs: obs()}).maskStubs(9, false); len(narrow) != 0 {
		t.Fatalf("not at the cliff, it stubbed %+v", narrow)
	}
}
