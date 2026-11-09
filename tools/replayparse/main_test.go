package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/proto"
)

// TestReplayCountsNewUnparseableFiles: a write that creates a .go file that does not parse is not a break (the file
// never parsed) but is counted apart, so it is not lost; a write that breaks an existing file is a break.
func TestReplayCountsNewUnparseableFiles(t *testing.T) {
	var lines []string
	add := func(seq int64, typ proto.EventType, payload any) {
		ev, err := proto.NewEvent(seq, time.Time{}, typ, 0, payload)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(ev)
		lines = append(lines, string(b))
	}
	add(1, proto.EvToolCall, proto.ToolCall{ID: "w1", Name: "write", Input: []byte(`{"path":"new.go","content":"package x\nfunc {"}`)})
	add(2, proto.EvToolFinished, proto.ToolFinished{ID: "w1", Outcome: proto.OutcomeOK})
	add(3, proto.EvToolCall, proto.ToolCall{ID: "w2", Name: "write", Input: []byte(`{"path":"old.go","content":"package x\nfunc {"}`)})
	add(4, proto.EvToolFinished, proto.ToolFinished{ID: "w2", Outcome: proto.OutcomeOK})
	p := filepath.Join(t.TempDir(), "events.jsonl")
	os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
	ro := replay(p, map[string]string{"old.go": "package x\n"}, "job", "run", true)
	if ro.newUnparseable != 1 || len(ro.breaks) != 1 || ro.breaks[0].File != "old.go" {
		t.Fatalf("new unparseable %d, breaks %+v", ro.newUnparseable, ro.breaks)
	}
}
