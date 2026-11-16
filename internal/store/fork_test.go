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
