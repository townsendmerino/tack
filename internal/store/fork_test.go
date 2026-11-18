package store

import (
	"errors"
	"reflect"
	"testing"

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
