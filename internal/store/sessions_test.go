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
