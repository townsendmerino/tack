package core

import (
	"errors"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
)

// session/fork makes a session from the parent's log up to a turn's end, which prompts and runs like any session and leaves the
// parent as it was.
func TestForkMakesASessionThatRunsOnItsOwn(t *testing.T) {
	f := newFixture(t, goinfertest.Says("first answer"), goinfertest.Says("second answer"))
	parent := f.session(t)
	f.client.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: parent}, nil)
	f.prompt(t, parent, "first question")
	got := f.waitTurnEnd(t)
	end := got[len(got)-1].Seq
	f.waitTurnFreed(t, parent)
	before, _ := f.core.opt.Store.Events(parent, 0)

	cl, _, _ := f.privileged(t)
	var r proto.SessionForkResult
	if err := cl.Call(proto.MethodSessionFork, proto.SessionForkParams{Session: parent, AtSeq: end}, &r); err != nil {
		t.Fatal(err)
	}
	if r.Session == "" || r.Session == parent {
		t.Fatalf("child %q", r.Session)
	}
	f.prompt(t, r.Session, "another way")
	var child []proto.Event
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(time.Millisecond) {
		child, _ = f.core.opt.Store.Events(r.Session, 0)
		if int64(len(child)) > end && child[len(child)-1].Type == proto.EvTurnEnd {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the child's turn never ended: %v", types(child))
		}
	}
	for i := int64(1); i < end; i++ { // all but the first, which names the fork
		if string(child[i].Payload) != string(before[i].Payload) || child[i].Type != before[i].Type {
			t.Errorf("event %d of the child is not the parent's", i+1)
		}
	}
	after, _ := f.core.opt.Store.Events(parent, 0)
	if len(after) != len(before) {
		t.Errorf("the parent grew from %d to %d events", len(before), len(after))
	}
}

func TestForkIsRefusedWhereItCannotWork(t *testing.T) {
	f := newFixture(t, goinfertest.Says("answer"))
	parent := f.session(t)
	f.client.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: parent}, nil)
	f.prompt(t, parent, "question")
	got := f.waitTurnEnd(t)
	end := got[len(got)-1].Seq
	f.waitTurnFreed(t, parent)
	var pe *proto.Error

	// No token: the client that made the session cannot fork it.
	if err := f.unprivileged(t).Call(proto.MethodSessionFork, proto.SessionForkParams{Session: parent, AtSeq: end}, nil); !errors.As(err, &pe) || pe.Code != proto.CodeUnauthorized {
		t.Errorf("unprivileged: %v", err)
	}
	cl, _, _ := f.privileged(t)
	for name, p := range map[string]struct {
		params proto.SessionForkParams
		code   int
	}{
		"inside a turn": {proto.SessionForkParams{Session: parent, AtSeq: 2}, proto.CodeInvalidParams},
		"past the head": {proto.SessionForkParams{Session: parent, AtSeq: end + 5}, proto.CodeInvalidParams},
		"seq 0":         {proto.SessionForkParams{Session: parent, AtSeq: 0}, proto.CodeInvalidParams},
		"no such":       {proto.SessionForkParams{Session: "s_nope", AtSeq: 1}, proto.CodeUnknownSession},
	} {
		if err := cl.Call(proto.MethodSessionFork, p.params, nil); !errors.As(err, &pe) || pe.Code != p.code {
			t.Errorf("%s: %v, want code %d", name, err, p.code)
		}
	}
	if ids, _ := f.core.opt.Store.Sessions(); len(ids) != 1 {
		t.Errorf("a refused fork left a session: %v", ids)
	}
}
