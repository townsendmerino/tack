package store

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/proto"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { clock = clock.Add(time.Millisecond); return clock }
	return s
}

// countSyncs makes l count its fsyncs instead of performing them.
func countSyncs(l *Log) *int {
	n := new(int)
	l.sync = func(*os.File) error { *n++; return nil }
	return n
}

// TestAppendFsyncsExactlyWhereRequired drives one turn through Append and checks the log fsyncs after the turn's
// boundaries and after tool.started (before the tool would run), and nowhere else.
func TestAppendFsyncsExactlyWhereRequired(t *testing.T) {
	s := newStore(t)
	l, err := s.Create("s_1")
	if err != nil {
		t.Fatal(err)
	}
	syncs := countSyncs(l)
	steps := []struct {
		typ     proto.EventType
		payload any
		fsync   bool
	}{
		{proto.EvUserMessage, proto.UserMessage{Content: []proto.ContentBlock{proto.Text("q")}}, true},
		{proto.EvRequestSent, proto.RequestSent{Request: 1, Server: "goinfer@local", Model: "m"}, false},
		{proto.EvAssistantDelta, proto.AssistantDelta{Kind: "text", Text: "x"}, false},
		{proto.EvToolCall, proto.ToolCall{ID: "t1", Name: "read", Input: json.RawMessage(`{}`)}, false},
		{proto.EvToolStarted, proto.ToolStarted{ID: "t1"}, true},
		{proto.EvToolFinished, proto.ToolFinished{ID: "t1", Outcome: proto.OutcomeOK, Result: "r"}, false},
		{proto.EvTurnEnd, proto.TurnEnd{StopReason: proto.StopEndTurn}, true},
	}
	for _, st := range steps {
		before := *syncs
		ev, err := l.Append(st.typ, 0, st.payload)
		if err != nil {
			t.Fatalf("%s: %v", st.typ, err)
		}
		if got := *syncs - before; (got == 1) != st.fsync || got > 1 {
			t.Errorf("%s: %d fsyncs, want fsync=%v", st.typ, got, st.fsync)
		}
		if st.typ == proto.EvAssistantDelta && ev.Seq != 0 {
			t.Errorf("live-only event got seq %d", ev.Seq)
		}
	}
	if l.Head() != 6 {
		t.Fatalf("head %d, want 6 (the delta is not persisted)", l.Head())
	}
	evs, err := s.Events("s_1", 0)
	if err != nil {
		t.Fatal(err)
	}
	for i, ev := range evs {
		if ev.Seq != int64(i+1) || ev.Type == proto.EvAssistantDelta {
			t.Fatalf("event %d: seq %d type %s", i, ev.Seq, ev.Type)
		}
	}
}

func TestReopenContinuesAndRepairsTornTail(t *testing.T) {
	s := newStore(t)
	l, _ := s.Create("s_1")
	for range 3 {
		if _, err := l.Append(proto.EvTurnEnd, 0, proto.TurnEnd{StopReason: proto.StopEndTurn}); err != nil {
			t.Fatal(err)
		}
	}
	l.Close()

	// A crash mid-append leaves half a line with no newline.
	p, _ := s.logPath("s_1")
	f, _ := os.OpenFile(p, os.O_WRONLY|os.O_APPEND, 0)
	f.WriteString(`{"seq":4,"time":"2026-10-16T12:00:00Z","ty`)
	f.Close()

	evs, err := s.Events("s_1", 0)
	if err != nil || len(evs) != 3 {
		t.Fatalf("Events with a torn tail: %d events, %v", len(evs), err)
	}
	l, err = s.OpenLog("s_1")
	if err != nil {
		t.Fatal(err)
	}
	ev, err := l.Append(proto.EvTurnEnd, 0, proto.TurnEnd{StopReason: proto.StopEndTurn})
	if err != nil || ev.Seq != 4 {
		t.Fatalf("append after reopen: seq %d, %v", ev.Seq, err)
	}
	l.Close()
	evs, err = s.Events("s_1", 3)
	if err != nil || len(evs) != 2 || evs[1].Seq != 4 {
		t.Fatalf("after repair: %+v, %v", evs, err)
	}
}

func TestCorruptLineInsideTheLogIsAnError(t *testing.T) {
	s := newStore(t)
	l, _ := s.Create("s_1")
	l.Append(proto.EvTurnEnd, 0, proto.TurnEnd{StopReason: proto.StopEndTurn})
	l.Append(proto.EvTurnEnd, 0, proto.TurnEnd{StopReason: proto.StopEndTurn})
	l.Close()
	p, _ := s.logPath("s_1")
	data, _ := os.ReadFile(p)
	lines := strings.SplitAfter(string(data), "\n")
	for name, mutated := range map[string]string{
		"garbage": "not json\n" + lines[1],
		"seq gap": strings.Replace(lines[0], `"seq":1`, `"seq":2`, 1) + lines[1],
	} {
		os.WriteFile(p, []byte(mutated), 0o600)
		if _, err := s.OpenLog("s_1"); err == nil {
			t.Errorf("%s: OpenLog accepted a corrupt log", name)
		}
	}
}

func TestCreateRefusesExistingSession(t *testing.T) {
	s := newStore(t)
	l, _ := s.Create("s_1")
	l.Close()
	if _, err := s.Create("s_1"); err == nil {
		t.Fatal("Create overwrote an existing session")
	}
}

func TestBlobs(t *testing.T) {
	s := newStore(t)
	h1, err := s.Blobs.Put([]byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	h2, _ := s.Blobs.Put([]byte("hello"))
	if h1 != h2 || h1 != "sha256:2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824" {
		t.Fatalf("handles %s %s", h1, h2)
	}
	got, err := s.Blobs.Get(h1)
	if err != nil || string(got) != "hello" {
		t.Fatalf("Get = %q, %v", got, err)
	}
	hexsum, _ := parseHandle(h1)
	os.WriteFile(s.Blobs.path(hexsum), []byte("jello"), 0o600)
	if _, err := s.Blobs.Get(h1); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Get of a changed blob: %v", err)
	}
	if _, err := s.Blobs.Get("sha256:zz"); err == nil {
		t.Fatal("bad handle accepted")
	}
}

func TestInspect(t *testing.T) {
	mk := func(typ proto.EventType, id string) proto.Event {
		var p any
		switch typ {
		case proto.EvToolStarted:
			p = proto.ToolStarted{ID: id}
		case proto.EvToolFinished:
			p = proto.ToolFinished{ID: id, Outcome: proto.OutcomeOK}
		case proto.EvUserMessage:
			p = proto.UserMessage{}
		case proto.EvTurnEnd:
			p = proto.TurnEnd{}
		}
		ev, err := proto.NewEvent(1, time.Now(), typ, 0, p)
		if err != nil {
			t.Fatal(err)
		}
		return ev
	}
	crashed := []proto.Event{
		mk(proto.EvUserMessage, ""), mk(proto.EvToolStarted, "a"), mk(proto.EvToolFinished, "a"),
		mk(proto.EvToolStarted, "b"), mk(proto.EvToolStarted, "c"), mk(proto.EvToolFinished, "c"),
	}
	got := Inspect(crashed)
	if !got.InTurn || !reflect.DeepEqual(got.UnknownTools, []string{"b"}) {
		t.Fatalf("Inspect = %+v", got)
	}
	if got := Inspect(append(crashed, mk(proto.EvTurnEnd, ""))); got.InTurn || got.UnknownTools != nil {
		t.Fatalf("closed turn: %+v", got)
	}
}

func TestNewSessionIDSortsByTime(t *testing.T) {
	a := NewSessionID(time.UnixMilli(1_700_000_000_000))
	b := NewSessionID(time.UnixMilli(1_700_000_000_001))
	if len(a) != 28 || !strings.HasPrefix(string(a), "s_") || !(a < b) {
		t.Fatalf("ids %s %s", a, b)
	}
}
