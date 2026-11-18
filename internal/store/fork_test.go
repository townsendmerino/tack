package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/proto"
)

// forkParent is a session with two finished turns and a third still running: session.created (1), user.message (2),
// turn.end (3), user.message (4), turn.end (5), user.message (6).
func forkParent(t *testing.T, s *Store) {
	t.Helper()
	l, err := s.Create("s_parent")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	say := proto.UserMessage{Content: []proto.ContentBlock{proto.Text("go")}}
	for _, step := range []struct {
		typ proto.EventType
		p   any
	}{
		{proto.EvSessionCreated, proto.SessionCreated{Workspace: "/w", Model: "main", Mode: proto.ModeAsk}},
		{proto.EvUserMessage, say}, {proto.EvTurnEnd, proto.TurnEnd{StopReason: "end_turn"}},
		{proto.EvUserMessage, say}, {proto.EvTurnEnd, proto.TurnEnd{StopReason: "end_turn"}},
		{proto.EvUserMessage, say},
	} {
		if _, err := l.Append(step.typ, 0, step.p); err != nil {
			t.Fatal(err)
		}
	}
}

// A fork's log is the parent's events up to the cut, verbatim, except that the first names the fork; the child then opens and
// appends like any session, from the next seq.
func TestForkCopiesThePrefixAndMarksTheChild(t *testing.T) {
	s := newStore(t)
	forkParent(t, s)
	if err := s.Fork("s_parent", "s_child", 3); err != nil {
		t.Fatal(err)
	}
	parent, _ := s.Events("s_parent", 0)
	child, err := s.Events("s_child", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(child) != 3 {
		t.Fatalf("%d events, want 3", len(child))
	}
	for i := 1; i < 3; i++ {
		if !reflect.DeepEqual(child[i], parent[i]) {
			t.Errorf("event %d differs: %+v vs %+v", i+1, child[i], parent[i])
		}
	}
	v, err := child[0].Decode()
	if err != nil {
		t.Fatal(err)
	}
	c := v.(proto.SessionCreated)
	if c.ForkOf != "s_parent" || c.ForkAt != 3 || c.Workspace != "/w" || c.Model != "main" || c.Mode != proto.ModeAsk || c.Parent != "" {
		t.Errorf("session.created %+v", c)
	}
	if child[0].Seq != 1 || !child[0].Time.Equal(parent[0].Time) {
		t.Errorf("first event %+v", child[0])
	}
	l, err := s.OpenLog("s_child")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	ev, err := l.Append(proto.EvUserMessage, 0, proto.UserMessage{Content: []proto.ContentBlock{proto.Text("other way")}})
	if err != nil || ev.Seq != 4 {
		t.Errorf("next event %+v, %v", ev, err)
	}
	if again, _ := s.Events("s_parent", 0); len(again) != len(parent) {
		t.Error("the parent changed")
	}
}

// A fork inside a turn is refused, since the history would end on a call with no result; so is a seq the log does not have, an
// unknown parent, a child that exists, and a cut of 0.
func TestForkRefusals(t *testing.T) {
	s := newStore(t)
	forkParent(t, s)
	if err := s.Fork("s_parent", "s_a", 6); !errors.Is(err, ErrForkInTurn) {
		t.Errorf("inside a turn: %v", err)
	}
	if err := s.Fork("s_parent", "s_a", 4); !errors.Is(err, ErrForkInTurn) {
		t.Errorf("after a user.message: %v", err)
	}
	for _, at := range []int64{0, -1, 7} {
		if err := s.Fork("s_parent", "s_a", at); !errors.Is(err, ErrForkSeq) {
			t.Errorf("at %d: %v", at, err)
		}
	}
	if err := s.Fork("s_nope", "s_a", 1); err == nil {
		t.Error("unknown parent")
	}
	if err := s.Fork("s_parent", "s_parent", 3); err == nil {
		t.Error("child exists")
	}
	if ids, _ := s.Sessions(); len(ids) != 1 {
		t.Errorf("a refused fork left a log: %v", ids)
	}
	if err := s.Fork("s_parent", "s_ok", 1); err != nil {
		t.Errorf("a fork of just the start: %v", err)
	}
}

// ForkCarrying writes the carried events inside the fork, after the copied prefix and with the next seqs, so the child exists
// with them or not at all.
func TestForkCarryingWritesTheExtraEventsInTheSameFork(t *testing.T) {
	s := newStore(t)
	forkParent(t, s)
	var sawParent, sawChild int
	err := s.ForkCarrying("s_parent", "s_child", 3, func(parent, child []proto.Event) []ForkEvent {
		sawParent, sawChild = len(parent), len(child)
		return []ForkEvent{
			{Type: proto.EvTripwireHit, Payload: proto.TripwireHit{ID: "fork", Command: "git config core.fsmonitor x", Count: 1}},
			{Type: proto.EvSessionHeld, Payload: proto.SessionHeld{Commands: []string{"git config core.fsmonitor x"}}},
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if sawParent != 6 || sawChild != 3 {
		t.Errorf("carry saw %d parent and %d child events, want 6 and 3", sawParent, sawChild)
	}
	child, _ := s.Events("s_child", 0)
	if len(child) != 5 || child[3].Type != proto.EvTripwireHit || child[4].Type != proto.EvSessionHeld || child[3].Seq != 4 || child[4].Seq != 5 {
		t.Fatalf("child %+v", child)
	}
}

// A carry that cannot be written (a payload of the wrong type for its event) fails the whole fork, and no child is left: this
// is the step between copying the prefix and carrying, which used to be two calls.
func TestAForkWhoseCarryFailsLeavesNoChild(t *testing.T) {
	s := newStore(t)
	forkParent(t, s)
	err := s.ForkCarrying("s_parent", "s_child", 3, func(_, _ []proto.Event) []ForkEvent {
		return []ForkEvent{{Type: proto.EvTripwireHit, Payload: "not a TripwireHit"}}
	})
	if err == nil {
		t.Fatal("no error")
	}
	if ids, _ := s.Sessions(); len(ids) != 1 {
		t.Errorf("a failed fork left a session: %v", ids)
	}
	if _, err := s.Events("s_child", 0); err == nil {
		t.Error("the child's log exists")
	}
}

// carryTwo is a carry of two events, so a fork has a prefix and a carried part with a point between them.
func carryTwo(_, _ []proto.Event) []ForkEvent {
	return []ForkEvent{
		{Type: proto.EvTripwireHit, Payload: proto.TripwireHit{ID: "fork", Command: "git config core.fsmonitor x", Count: 1}},
		{Type: proto.EvSessionHeld, Payload: proto.SessionHeld{Commands: []string{"git config core.fsmonitor x"}}},
	}
}

// nothingFindable asserts that the child is not a session in any way a reader finds one: not listed, not read, not reopened.
func nothingFindable(t *testing.T, s *Store, parent, child proto.SessionID, when string) {
	t.Helper()
	ids, _ := s.Sessions()
	if len(ids) != 1 || ids[0] != parent {
		t.Errorf("%s: Sessions() = %v, want only the parent", when, ids)
	}
	if evs, err := s.Events(child, 0); err == nil {
		t.Errorf("%s: Events(child) read %d events", when, len(evs))
	}
	if l, err := s.OpenLog(child); err == nil {
		l.Close()
		t.Errorf("%s: OpenLog(child) succeeded", when)
	}
}

// The property ForkCarrying is for: at no moment while a fork is written, and at no point a crash could stop it, is a child findable
// without everything it must carry. The hook stops the fork after each line, as a crash would, and the removal of what was written
// fails too, as it would after a crash: nothing a reader finds is left.
func TestAForkStoppedAnywhereLeavesNoFindableChild(t *testing.T) {
	s := newStore(t)
	forkParent(t, s)
	lines := 3 + 2 // the prefix cut at 3, and two carried events
	for stop := 1; stop <= lines; stop++ {
		removeAll = func(string) error { return errors.New("disk gone") }
		forkWriteHook = func(written int) error {
			nothingFindable(t, s, "s_parent", "s_child", fmt.Sprintf("while writing line %d", written))
			if written == stop {
				return errors.New("crash")
			}
			return nil
		}
		err := s.ForkCarrying("s_parent", "s_child", 3, carryTwo)
		forkWriteHook, removeAll = nil, os.RemoveAll
		if err == nil {
			t.Fatalf("stopped after %d lines: no error", stop)
		}
		nothingFindable(t, s, "s_parent", "s_child", fmt.Sprintf("after stopping at line %d", stop))
	}
}

// A fork that runs to the end is listed, read and reopened with all of its lines; a second fork to the same child is refused and
// leaves the first as it was.
func TestAWholeForkIsFoundAndAChildCannotBeMadeTwice(t *testing.T) {
	s := newStore(t)
	forkParent(t, s)
	if err := s.ForkCarrying("s_parent", "s_child", 3, carryTwo); err != nil {
		t.Fatal(err)
	}
	if ids, _ := s.Sessions(); len(ids) != 2 {
		t.Errorf("Sessions() = %v", ids)
	}
	l, evs, err := s.Reopen("s_child")
	if err != nil || len(evs) != 5 {
		t.Fatalf("reopen: %d events, %v", len(evs), err)
	}
	l.Close()
	if err := s.ForkCarrying("s_parent", "s_child", 1, nil); err == nil {
		t.Error("a second fork to the same child was made")
	}
	if evs, _ := s.Events("s_child", 0); len(evs) != 5 {
		t.Errorf("the first child changed: %d events", len(evs))
	}
}

// Leftovers of a fork that died are invisible to every reader, and the next fork removes the old ones.
func TestAStaleForkTempIsInvisibleAndSwept(t *testing.T) {
	s := newStore(t)
	forkParent(t, s)
	stale := filepath.Join(s.Root(), "sessions", ".fork-s_dead-123")
	if err := os.MkdirAll(stale, 0o700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(stale, "events.jsonl"), []byte(`{"seq":1}`+"\n"), 0o600)
	if ids, _ := s.Sessions(); len(ids) != 1 {
		t.Errorf("a temp is listed: %v", ids)
	}
	old := time.Now().Add(-2 * time.Hour)
	os.Chtimes(stale, old, old)
	fresh := filepath.Join(s.Root(), "sessions", ".fork-s_live-456") // a fork in progress elsewhere is left alone
	os.MkdirAll(fresh, 0o700)
	if err := s.Fork("s_parent", "s_child", 3); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); err == nil {
		t.Error("the stale temp was not swept")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Error("a recent temp was swept")
	}
}

// If the final fsync of the sessions directory fails after the rename, the child is complete and findable, so the fork is not reported as
// a failure: a transient failure is retried, and one that persists is ErrForkNotDurable, which a caller treats as made, with a warning.
func TestAForkWhoseDirectorySyncFailsIsStillMade(t *testing.T) {
	s := newStore(t)
	forkParent(t, s)
	sessions := filepath.Join(s.Root(), "sessions")
	orig := syncDir
	t.Cleanup(func() { syncDir = orig })
	failed := 0
	syncDir = func(dir string) error {
		if dir == sessions && failed < 2 {
			failed++
			return errors.New("EIO")
		}
		return orig(dir)
	}
	if err := s.ForkCarrying("s_parent", "s_a", 3, carryTwo); err != nil {
		t.Fatalf("a transient failure was not retried: %v", err)
	}
	syncDir = func(dir string) error {
		if dir == sessions {
			return errors.New("EIO")
		}
		return orig(dir)
	}
	err := s.ForkCarrying("s_parent", "s_b", 3, carryTwo)
	if !errors.Is(err, ErrForkNotDurable) {
		t.Fatalf("persistent failure: %v, want ErrForkNotDurable", err)
	}
	for _, id := range []proto.SessionID{"s_a", "s_b"} {
		if evs, err := s.Events(id, 0); err != nil || len(evs) != 5 {
			t.Errorf("%s: %d events, %v: the fork is complete and must be findable", id, len(evs), err)
		}
	}
}

// A temp whose fork is still running, even a stalled one, is not swept: the fork holds a lock on it, which dies with the process.
func TestASweepLeavesATempWhoseForkHoldsItsLock(t *testing.T) {
	s := newStore(t)
	forkParent(t, s)
	live := filepath.Join(s.Root(), "sessions", ".fork-s_live-1")
	dead := filepath.Join(s.Root(), "sessions", ".fork-s_dead-2")
	for _, d := range []string{live, dead} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	holder, err := os.OpenFile(filepath.Join(live, forkLockName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	if err := lockLog(holder); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dead, forkLockName), nil, 0o600) // a lock file nobody holds: its fork died
	old := time.Now().Add(-3 * time.Hour)
	for _, d := range []string{live, dead} { // after the files are made: making one changes the directory's time
		os.Chtimes(d, old, old)
	}
	if err := s.Fork("s_parent", "s_child", 3); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(live); err != nil {
		t.Error("a temp whose fork holds its lock was swept")
	}
	if _, err := os.Stat(dead); err == nil {
		t.Error("a dead fork's temp was not swept")
	}
	if evs, err := s.Events("s_child", 0); err != nil || len(evs) != 3 {
		t.Errorf("the fork itself: %d events, %v", len(evs), err)
	}
	if _, err := os.Stat(filepath.Join(s.Root(), "sessions", "s_child", forkLockName)); err == nil {
		t.Error("the lock file was left in the child's directory")
	}
}
