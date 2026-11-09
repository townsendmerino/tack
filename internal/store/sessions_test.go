package store

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/proto"
)

// TestOneWriterPerLog: while one Log has a session's log open for appending, opening it again fails with ErrLogBusy,
// in this process or another, so a daemon's recovery cannot append to a session a `tack run` is writing; once the
// log is closed, it opens.
func TestOneWriterPerLog(t *testing.T) {
	s := newStore(t)
	l, err := s.Create("s_1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.OpenLog("s_1"); !errors.Is(err, ErrLogBusy) {
		t.Fatalf("OpenLog while it is open: %v, want ErrLogBusy", err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	l2, err := s.OpenLog("s_1")
	if err != nil {
		t.Fatalf("OpenLog after Close: %v", err)
	}
	if _, err := s.OpenLog("s_1"); !errors.Is(err, ErrLogBusy) {
		t.Fatalf("OpenLog while reopened: %v, want ErrLogBusy", err)
	}
	l2.Close()
}

// TestSessionsListsEveryLog: Sessions names every session with a log, in id order, which is creation order.
func TestSessionsListsEveryLog(t *testing.T) {
	s := newStore(t)
	var want []proto.SessionID
	for i := range 3 {
		id := NewSessionID(time.Date(2026, 11, 8, 12, 0, i, 0, time.UTC))
		l, err := s.Create(id)
		if err != nil {
			t.Fatal(err)
		}
		l.Close()
		want = append(want, id)
	}
	os.MkdirAll(filepath.Join(s.Root(), "sessions", "s_empty"), 0o700) // a directory with no log is not a session
	got, err := s.Sessions()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("Sessions = %v, want %v", got, want)
	}
}

// TestSessionIDsCannotLeaveTheStore: a session id comes from a client, and becomes a path. One that is not "s_" and
// letters and digits is refused before any file is touched.
func TestSessionIDsCannotLeaveTheStore(t *testing.T) {
	s := newStore(t)
	for _, id := range []proto.SessionID{"", "s_", "../s_1", "s_../../x", "s_1/../../x", "s_a.b", "x_1", "s_1\x00"} {
		if ValidSessionID(id) {
			t.Errorf("ValidSessionID(%q) = true", id)
		}
		if _, err := s.Create(id); err == nil {
			t.Errorf("Create(%q) succeeded", id)
		}
		if _, err := s.OpenLog(id); err == nil || errors.Is(err, os.ErrNotExist) {
			t.Errorf("OpenLog(%q) = %v, want an invalid-id error", id, err)
		}
		if _, err := s.Events(id, 0); err == nil || errors.Is(err, os.ErrNotExist) {
			t.Errorf("Events(%q) = %v, want an invalid-id error", id, err)
		}
	}
	if id := NewSessionID(time.Now()); !ValidSessionID(id) {
		t.Errorf("ValidSessionID(%q) = false for a new id", id)
	}
}

// TestReopenReadsUnderTheLock: Reopen returns the log's events with the writer's lock held, so they are the events
// it will append after, and a second Reopen while it is held is ErrLogBusy.
func TestReopenReadsUnderTheLock(t *testing.T) {
	s := newStore(t)
	l, _ := s.Create("s_1")
	l.Append(proto.EvTurnEnd, 0, proto.TurnEnd{StopReason: proto.StopEndTurn})
	l.Close()
	l, evs, err := s.Reopen("s_1")
	if err != nil || len(evs) != 1 || l.Head() != 1 {
		t.Fatalf("Reopen = %d events, head %d, %v; want the one event", len(evs), l.Head(), err)
	}
	defer l.Close()
	if _, _, err := s.Reopen("s_1"); !errors.Is(err, ErrLogBusy) {
		t.Fatalf("a second Reopen: %v, want ErrLogBusy", err)
	}
}

// TestAnIdleLogIsFreeAndCatchesUp: between turns a Log gives up its writer's lock (Idle), so another process (tack
// rewind) may write the log; its next Append takes the lock back and reads past what the other appended, so seqs stay
// dense, and it holds the lock again until the next Idle.
func TestAnIdleLogIsFreeAndCatchesUp(t *testing.T) {
	s := newStore(t)
	end := proto.TurnEnd{StopReason: proto.StopEndTurn}
	l, err := s.Create("s_1")
	if err != nil {
		t.Fatal(err)
	}
	l.Append(proto.EvTurnEnd, 0, end)
	if err := l.Idle(); err != nil {
		t.Fatal(err)
	}
	other, err := s.OpenLog("s_1")
	if err != nil {
		t.Fatalf("OpenLog while the first Log is idle: %v, want the lock free", err)
	}
	if ev, err := other.Append(proto.EvTurnEnd, 0, end); err != nil || ev.Seq != 2 {
		t.Fatalf("the other writer's append: seq %d, %v", ev.Seq, err)
	}
	other.Close()
	foreign, err := l.Wake()
	if err != nil || len(foreign) != 1 || foreign[0].Seq != 2 {
		t.Fatalf("Wake = %v, %v; want the other writer's event, seq 2", foreign, err)
	}
	if again, err := l.Wake(); err != nil || len(again) != 0 {
		t.Fatalf("a second Wake = %v, %v; want nothing: the Log is awake", again, err)
	}
	ev, err := l.Append(proto.EvTurnEnd, 0, end)
	if err != nil || ev.Seq != 3 || l.Head() != 3 {
		t.Fatalf("after idle: seq %d, head %d, %v; want 3, past the other writer's", ev.Seq, l.Head(), err)
	}
	if _, err := s.OpenLog("s_1"); !errors.Is(err, ErrLogBusy) {
		t.Fatalf("OpenLog once the first Log has written again: %v, want ErrLogBusy", err)
	}
	l.Close()
	if _, err := l.Append(proto.EvTurnEnd, 0, end); err == nil {
		t.Fatal("Append after Close succeeded")
	}
	if evs, _ := s.Events("s_1", 0); len(evs) != 3 {
		t.Fatalf("%d events, want 3, dense", len(evs))
	}
}

// TestWakeSyncsATornTailItCuts: a torn last line another writer left is cut when an idle Log wakes, and the cut is
// made durable, as Reopen makes it.
func TestWakeSyncsATornTailItCuts(t *testing.T) {
	s := newStore(t)
	l, _ := s.Create("s_1")
	l.Append(proto.EvTurnEnd, 0, proto.TurnEnd{StopReason: proto.StopEndTurn})
	l.Idle()
	p, _ := s.logPath("s_1")
	f, _ := os.OpenFile(p, os.O_WRONLY|os.O_APPEND, 0)
	f.WriteString(`{"seq":2,"time":"2026-11-08T12:00:00Z","ty`)
	f.Close()
	syncs := countSyncs(l)
	if _, err := l.Wake(); err != nil {
		t.Fatal(err)
	}
	if *syncs != 1 {
		t.Fatalf("%d syncs after cutting the torn tail; want 1", *syncs)
	}
	if ev, err := l.Append(proto.EvTurnEnd, 0, proto.TurnEnd{StopReason: proto.StopEndTurn}); err != nil || ev.Seq != 2 {
		t.Fatalf("after the cut: seq %d, %v; want 2", ev.Seq, err)
	}
	l.Close()
}

// TestAFreshStoreHasNoSessions: Open makes sessions/, so a store no session has been made in lists none, without error,
// and a daemon's first start recovers nothing rather than failing.
func TestAFreshStoreHasNoSessions(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	if ids, err := s.Sessions(); err != nil || len(ids) != 0 {
		t.Fatalf("a fresh store's sessions: %v, %v; want none", ids, err)
	}
}

// TestAHoldIsExclusive: one Hold per session, in this process or another; a released hold can be taken again, and a
// session with no directory has none to take.
func TestAHoldIsExclusive(t *testing.T) {
	s := newStore(t)
	l, err := s.Create("s_1")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	h, err := s.Hold("s_1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Hold("s_1"); !errors.Is(err, ErrSessionHeld) {
		t.Fatalf("a second hold: %v, want ErrSessionHeld", err)
	}
	if err := h.Release(); err != nil {
		t.Fatal(err)
	}
	h, err = s.Hold("s_1")
	if err != nil {
		t.Fatalf("a hold after release: %v", err)
	}
	h.Release()
	if _, err := s.Hold("s_2"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("holding a session that does not exist: %v, want ErrNotExist", err)
	}
}
