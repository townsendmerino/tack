package tui

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/proto/fixtures"
)

var update = flag.Bool("update", false, "rewrite the golden frames in testdata")

// stream is one session's events from a protocol fixture, in the order the daemon sent them.
type stream struct {
	name string // the fixture's name and the session's last four characters
	evs  []proto.Event
}

// fixtureStreams reads every session/event notification in proto/fixtures, one stream per session: the recorded event
// streams both sides of the protocol test against (DESIGN §9.3).
func fixtureStreams(t *testing.T) []stream {
	t.Helper()
	names, err := fs.Glob(fixtures.FS, "*.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	var out []stream
	for _, name := range names {
		b, err := fixtures.FS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		bySession := map[string]int{}
		sc := bufio.NewScanner(bytes.NewReader(b))
		sc.Buffer(nil, 1<<20)
		for sc.Scan() {
			var line struct {
				Msg struct {
					Method string `json:"method"`
					Params struct {
						Session string      `json:"session"`
						Event   proto.Event `json:"event"`
					} `json:"params"`
				} `json:"msg"`
			}
			if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if line.Msg.Method != proto.MethodSessionEvent {
				continue
			}
			s := line.Msg.Params.Session
			i, ok := bySession[s]
			if !ok {
				i = len(out)
				bySession[s] = i
				out = append(out, stream{name: strings.TrimSuffix(name, ".jsonl") + "-" + s[len(s)-4:]})
			}
			out[i].evs = append(out[i].evs, line.Msg.Params.Event)
		}
		if err := sc.Err(); err != nil {
			t.Fatal(err)
		}
	}
	if len(out) < 10 {
		t.Fatalf("%d streams in proto/fixtures: the reader is broken", len(out))
	}
	return out
}

// frames is each frame a stream is checked at: after its last event, and, when it asks the user, right after its first
// question, while the question is open.
func frames(s stream) map[string][]proto.Event {
	f := map[string][]proto.Event{s.name: s.evs}
	for i, ev := range s.evs {
		if ev.Type == proto.EvPermissionRequested {
			f[s.name+"-asking"] = s.evs[:i+1]
			break
		}
	}
	return f
}

var sizes = [][2]int{{80, 24}, {40, 12}}

// TestFramesMatchTheirGoldens renders every fixture stream at 80×24 and 40×12 and compares each frame with its golden
// in testdata; -update rewrites them, to be read before they are committed.
func TestFramesMatchTheirGoldens(t *testing.T) {
	for _, s := range fixtureStreams(t) {
		for name, evs := range frames(s) {
			for _, sz := range sizes {
				var v View
				for _, ev := range evs {
					v.Apply(ev)
				}
				got := v.Render(sz[0], sz[1])
				path := filepath.Join("testdata", fmt.Sprintf("%s_%dx%d.golden", name, sz[0], sz[1]))
				if *update {
					if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
						t.Fatal(err)
					}
					continue
				}
				want, err := os.ReadFile(path)
				if err != nil {
					t.Errorf("%s: %v (run with -update)", name, err)
					continue
				}
				if got != string(want) {
					t.Errorf("%s at %dx%d:\n got:\n%s\nwant:\n%s", name, sz[0], sz[1], got, want)
				}
			}
		}
	}
}

// TestAFrameFillsItsSize: every frame of every stream, at every size down to 10×3, is exactly height lines, none wider
// than width cells and none with trailing space, and its last line is the status line.
func TestAFrameFillsItsSize(t *testing.T) {
	for _, s := range fixtureStreams(t) {
		var v View
		for _, ev := range s.evs {
			v.Apply(ev)
			for _, sz := range [][2]int{{80, 24}, {40, 12}, {20, 5}, {10, 3}} {
				lines := strings.Split(v.Render(sz[0], sz[1]), "\n")
				if len(lines) != sz[1] {
					t.Fatalf("%s seq %d at %dx%d: %d lines", s.name, ev.Seq, sz[0], sz[1], len(lines))
				}
				for i, l := range lines {
					if n := utf8.RuneCountInString(l); n > sz[0] || strings.TrimRight(l, " ") != l {
						t.Fatalf("%s seq %d at %dx%d: line %d is %d cells or ends in a space: %q", s.name, ev.Seq, sz[0], sz[1], i, n, l)
					}
				}
				if st := v.Status.Line(); lines[sz[1]-1] != fit(st, sz[0]) {
					t.Fatalf("%s seq %d: last line %q, want the status %q", s.name, ev.Seq, lines[sz[1]-1], st)
				}
			}
		}
	}
}

func ev(t *testing.T, seq int64, typ proto.EventType, payload any) proto.Event {
	t.Helper()
	e := event(t, typ, payload)
	e.Seq = seq
	return e
}

// TestDeltasStreamThenTheMessageReplacesThem: the open block's deltas show as they arrive, and the finished message
// replaces them, so a delta the message disagrees with is gone.
func TestDeltasStreamThenTheMessageReplacesThem(t *testing.T) {
	var v View
	v.Apply(ev(t, 1, proto.EvUserMessage, proto.UserMessage{Content: []proto.ContentBlock{proto.Text("where is A?")}}))
	v.Apply(ev(t, 0, proto.EvAssistantDelta, proto.AssistantDelta{Index: 0, Kind: "text", Text: "It is in "}))
	v.Apply(ev(t, 0, proto.EvAssistantDelta, proto.AssistantDelta{Index: 0, Kind: "text", Text: "b.g"}))
	if f := v.Render(40, 6); !strings.Contains(f, "It is in b.g") {
		t.Fatalf("while streaming:\n%s", f)
	}
	v.Apply(ev(t, 2, proto.EvAssistantMessage, proto.AssistantMessage{Request: 1, StopReason: "end_turn",
		Content: []proto.ModelBlock{{Type: "text", Text: "It is in a.go."}}}))
	if f := v.Render(40, 6); !strings.Contains(f, "It is in a.go.") || strings.Contains(f, "b.g") {
		t.Fatalf("once finished:\n%s", f)
	}
}

// TestAnAnswerClosesTheQuestion: an open question shows its keys; its resolution replaces them with who decided, so a
// resolved question never offers keys.
func TestAnAnswerClosesTheQuestion(t *testing.T) {
	var v View
	v.Apply(ev(t, 1, proto.EvToolCall, proto.ToolCall{ID: "c1", Name: "bash", Input: json.RawMessage(`{"command":"rm x"}`)}))
	v.Apply(ev(t, 2, proto.EvPermissionRequested, proto.PermissionRequested{ID: "c1", Tool: "bash", Mode: proto.ModeAsk,
		Command: "rm x", Parsed: []string{"/bin/rm x"}, Reasons: []string{"rm deletes files"}}))
	if f := v.Render(60, 10); !strings.Contains(f, "[a]llow") || !strings.Contains(f, "rm deletes files") {
		t.Fatalf("asking:\n%s", f)
	}
	v.Apply(ev(t, 3, proto.EvPermissionResolved, proto.PermissionResolved{ID: "c1", Decision: proto.DecisionDeny, By: proto.DecidedByUser,
		Scope: "once", Reasons: []string{"the user declined"}}))
	if f := v.Render(60, 10); strings.Contains(f, "[a]llow") || !strings.Contains(f, "denied by user") {
		t.Fatalf("answered:\n%s", f)
	}
}

// TestScrollShowsEarlierLines: Scroll moves the frame up the transcript, and is clamped at its top.
func TestScrollShowsEarlierLines(t *testing.T) {
	var v View
	for i := 1; i <= 30; i++ {
		v.Apply(ev(t, int64(i), proto.EvUserMessage, proto.UserMessage{Content: []proto.ContentBlock{proto.Text(fmt.Sprintf("line %02d", i))}}))
	}
	if f := v.Render(20, 6); !strings.Contains(f, "line 30") || strings.Contains(f, "line 01") {
		t.Fatalf("at the bottom:\n%s", f)
	}
	v.Scroll = 1000
	if f := v.Render(20, 6); !strings.Contains(f, "line 01") || strings.Contains(f, "line 30") {
		t.Fatalf("scrolled to the top:\n%s", f)
	}
}
