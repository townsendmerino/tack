package loop

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
)

func TestRepeatGuardStopsAtTheLimit(t *testing.T) {
	var g RepeatGuard
	k := CallKey("grep", json.RawMessage(`{"pattern":"x","path":"."}`))
	for i := range RepeatLimit { // the first call and RepeatLimit-1 repeats
		if n, stop := g.Observe(k, "no matches"); stop || n != i {
			t.Fatalf("call %d: repeats %d, stop %v", i+1, n, stop)
		}
	}
	if n, stop := g.Observe(k, "no matches"); !stop || n != RepeatLimit {
		t.Fatalf("the %dth unchanged repeat: repeats %d, stop %v", RepeatLimit, n, stop)
	}
}

func TestRepeatGuardCountsOnlyUnchangedResults(t *testing.T) {
	var g RepeatGuard
	k, other := CallKey("read", json.RawMessage(`{"path":"a"}`)), CallKey("edit", json.RawMessage(`{"path":"a"}`))
	g.Observe(k, "v1")
	for range 5 {
		g.Observe(k, "v1")
		g.Observe(other, "ok") // calls in between do not reset k's count
	}
	if n, _ := g.Observe(k, "v1"); n != 6 {
		t.Fatalf("interleaved: %d unchanged repeats, want 6", n)
	}
	if n, _ := g.Observe(k, "v2"); n != 0 {
		t.Fatalf("a changed result counts %d, want 0", n)
	}
	if CallKey("grep", json.RawMessage(`{"a":1,"b":2}`)) != CallKey("grep", json.RawMessage(`{ "b":2, "a":1 }`)) {
		t.Fatal("key order made two calls")
	}
}

// TestStopRepeatsEndsTheTurn: through the loop, with StopRepeats on, the RepeatLimit-th unchanged repeat of a grep ends
// the turn with repeat_loop; off, the same script runs to the model's own end.
func TestStopRepeatsEndsTheTurn(t *testing.T) {
	script := func() []goinfertest.Reply {
		var rs []goinfertest.Reply
		for i := 0; i <= RepeatLimit; i++ {
			rs = append(rs, goinfertest.Calls("", "g", "grep", `{"pattern":"nothing matches this"}`))
		}
		return append(rs, goinfertest.Says("gave up"))
	}
	b := testBudget
	b.MaxRequests, b.MaxToolCalls = 20, 20
	on := newHarness(t, b, script()...)
	on.sess.cfg.StopRepeats = true
	if stop, err := on.sess.Turn(context.Background(), "find it"); err != nil || stop != proto.StopRepeatLoop {
		t.Fatalf("on: stop %q, %v", stop, err)
	}
	if n := len(on.payloads(proto.EvToolFinished)); n != RepeatLimit+1 {
		t.Fatalf("on: %d calls ran, want %d", n, RepeatLimit+1)
	}
	on.checkRebuild(t)
	off := newHarness(t, b, script()...)
	if stop, err := off.sess.Turn(context.Background(), "find it"); err != nil || stop != proto.StopEndTurn {
		t.Fatalf("off: stop %q, %v", stop, err)
	}
}
