package loop

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
	"github.com/townsendmerino/tack/internal/store"
)

// guardHarness is a bash session in auto mode outside the sandbox's concern (the commands asked for are refused or harmless), with the
// guard at limit, over the model's replies.
func guardHarness(t *testing.T, limit int, replies ...goinfertest.Reply) *harness {
	t.Helper()
	h := bashHarness(t, proto.ModeAsk, &approver{answer: Approval{}}, replies...)
	cfg := h.sess.cfg
	cfg.TripwireLimit = limit
	h.sess = New(cfg)
	return h
}

func bashReply(id, command string) goinfertest.Reply {
	return goinfertest.Calls("", id, "bash", `{"command":`+quote(command)+`}`)
}

func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\n':
			b.WriteString(`\n`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func hitsOf(h *harness) []proto.TripwireHit {
	var out []proto.TripwireHit
	for _, p := range h.payloads(proto.EvTripwireHit) {
		out = append(out, p.(proto.TripwireHit))
	}
	return out
}

// A counted shape is refused with a plain message and counted; the message and the recorded refusal name nothing it caught.
func TestACountedShapeIsRefusedPlainlyAndCounted(t *testing.T) {
	h := guardHarness(t, 3, bashReply("b1", `go test \-exec id`), goinfertest.Says("done"))
	h.sess.Turn(context.Background(), "go")
	f := h.finished("b1")
	if f.Outcome != proto.OutcomeDenied || f.Result != "error: not run: it needs the owner's review" {
		t.Fatalf("finished %+v", f)
	}
	if got := hitsOf(h); len(got) != 1 || got[0].ID != "b1" || got[0].Count != 1 || got[0].Command != `go test \-exec id` {
		t.Fatalf("hits %+v", got)
	}
	r := h.payloads(proto.EvPermissionResolved)[0].(proto.PermissionResolved)
	if r.Decision != proto.DecisionDeny || r.By != proto.DecidedByGuard {
		t.Errorf("resolution %+v", r)
	}
	for _, said := range append(r.Reasons, f.Result) {
		for _, name := range []string{"exec", "tripwire", "escape", "decode", "flag", "extld", "fsmonitor", "config"} {
			if strings.Contains(strings.ToLower(said), name) {
				t.Errorf("%q names %q, which is what was caught", said, name)
			}
		}
	}
	if h.sess.Held() {
		t.Error("held after one")
	}
}

// An honest command is not counted: a plain dangerous flag just asks, as before.
func TestAnHonestCommandIsNotCounted(t *testing.T) {
	h := guardHarness(t, 3,
		bashReply("b1", `find . -maxdepth 0 -exec true ';'`), bashReply("b2", `go build -ldflags '-extldflags=-static' .`),
		bashReply("b3", `git config user.name x`), goinfertest.Says("done"))
	h.sess.Turn(context.Background(), "go")
	if got := hitsOf(h); len(got) != 0 {
		t.Errorf("counted %+v", got)
	}
	if len(h.sess.Hits()) != 0 || h.sess.Held() {
		t.Error("state")
	}
}

// The third hit holds the session: the turn ends held, session.held lists the three commands, and a prompt is refused.
func TestTheThirdHitHoldsTheSession(t *testing.T) {
	h := guardHarness(t, 3, bashReply("b1", `go test \-exec id`), bashReply("b2", `go build -ldflags=-extld=./x .`),
		bashReply("b3", `git config core.fsmonitor id`), goinfertest.Says("never reached"))
	stop, err := h.sess.Turn(context.Background(), "go")
	if err != nil || stop != proto.StopHeld {
		t.Fatalf("stop %q, %v", stop, err)
	}
	if got := hitsOf(h); len(got) != 3 || got[2].Count != 3 {
		t.Fatalf("hits %+v", got)
	}
	held := h.payloads(proto.EvSessionHeld)
	if len(held) != 1 || len(held[0].(proto.SessionHeld).Commands) != 3 || held[0].(proto.SessionHeld).Commands[2] != `git config core.fsmonitor id` {
		t.Fatalf("session.held %+v", held)
	}
	if !h.sess.Held() {
		t.Fatal("not held")
	}
	if _, err := h.sess.Start("go on"); err != ErrHeld {
		t.Errorf("a prompt while held: %v", err)
	}
	end := h.payloads(proto.EvTurnEnd)
	if last := end[len(end)-1].(proto.TurnEnd); last.StopReason != proto.StopHeld {
		t.Errorf("turn.end %+v", last)
	}
}

// The count is in the log: a session resumed after two hits holds on the next one, and one resumed after the hold is held.
func TestTheCountAndTheHoldSurviveAResume(t *testing.T) {
	h := guardHarness(t, 3, bashReply("b1", `go test \-exec id`), bashReply("b2", `go build -ldflags=-extld=./x .`), goinfertest.Says("done"))
	h.sess.Turn(context.Background(), "go")
	if len(hitsOf(h)) != 2 || h.sess.Held() {
		t.Fatalf("hits %d, held %v", len(hitsOf(h)), h.sess.Held())
	}
	evs, err := h.log.Events()
	if err != nil {
		t.Fatal(err)
	}
	cfg := h.sess.cfg
	resumed, err := Resume(cfg, evs)
	if err != nil {
		t.Fatal(err)
	}
	if got := resumed.Hits(); len(got) != 2 || got[1] != `go build -ldflags=-extld=./x .` || resumed.Held() {
		t.Fatalf("resumed hits %q, held %v", got, resumed.Held())
	}
	held, err := resumed.Hit("b3", 0, `git config core.fsmonitor id`)
	if err != nil || !held {
		t.Fatalf("the third hit after a resume: held %v, %v", held, err)
	}
	evs, _ = h.log.Events()
	again, err := Resume(cfg, evs)
	if err != nil || !again.Held() || len(again.Hits()) != 3 {
		t.Fatalf("resumed after the hold: held %v, hits %v, %v", again != nil && again.Held(), again.Hits(), err)
	}
	if _, err := again.Start("x"); err != ErrHeld {
		t.Errorf("a prompt after a resume: %v", err)
	}
}

// Release lifts the hold and starts the count again, in the log: a resume after a release is not held and has no hits.
func TestReleaseLiftsTheHoldAndClearsTheCount(t *testing.T) {
	h := guardHarness(t, 1, bashReply("b1", `go test \-exec id`), goinfertest.Says("done"))
	h.sess.Turn(context.Background(), "go")
	if !h.sess.Held() {
		t.Fatal("not held at a limit of 1")
	}
	if err := h.sess.Release("user"); err != nil {
		t.Fatal(err)
	}
	if h.sess.Held() || len(h.sess.Hits()) != 0 {
		t.Error("still held")
	}
	evs, _ := h.log.Events()
	resumed, err := Resume(h.sess.cfg, evs)
	if err != nil || resumed.Held() || len(resumed.Hits()) != 0 {
		t.Errorf("resumed after release: held %v, hits %v, %v", resumed.Held(), resumed.Hits(), err)
	}
	var released int
	for _, ev := range evs {
		if ev.Type == proto.EvSessionReleased {
			released++
		}
	}
	if released != 1 {
		t.Errorf("%d session.released in the log", released)
	}
	if err := h.sess.Release("user"); err != nil { // not held: nothing recorded
		t.Fatal(err)
	}
}

// With no limit the guard counts nothing and refuses nothing.
func TestNoLimitMeansNoGuard(t *testing.T) {
	h := guardHarness(t, 0, bashReply("b1", `go test \-exec id`), goinfertest.Says("done"))
	h.sess.Turn(context.Background(), "go")
	if got := hitsOf(h); len(got) != 0 || strings.Contains(h.finished("b1").Result, "owner's review") {
		t.Errorf("hits %+v, result %q", got, h.finished("b1").Result)
	}
}

// Calls after the hold, in the same response, do not run: the third counted call holds the session and the fourth, an honest echo, is
// refused with the same plain text.
func TestCallsAfterTheHoldInTheSameResponseDoNotRun(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	h := bashHarness(t, proto.ModeAsk, &approver{answer: Approval{Allow: true}}, goinfertest.Calls("", "b1", "bash", `{"command":"go test \\-exec id"}`, "b2", "bash", `{"command":"go build -ldflags=-extld=./x ."}`,
		"b3", "bash", `{"command":"git config core.fsmonitor id"}`, "b4", "bash", `{"command":"touch `+marker+`"}`))
	cfg := h.sess.cfg
	cfg.TripwireLimit = 3
	h.sess = New(cfg)
	stop, _ := h.sess.Turn(context.Background(), "go")
	if stop != proto.StopHeld {
		t.Fatalf("stop %q", stop)
	}
	if f := h.finished("b4"); f.Outcome != proto.OutcomeDenied || f.Result != refusalText {
		t.Errorf("the call after the hold: %+v", f)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("the call after the hold ran")
	}
	if got := hitsOf(h); len(got) != 3 {
		t.Errorf("%d hits", len(got))
	}
}

func guardLog(t *testing.T, types ...any) []proto.Event {
	t.Helper()
	var out []proto.Event
	for i, p := range types {
		var typ proto.EventType
		switch p.(type) {
		case proto.TripwireHit:
			typ = proto.EvTripwireHit
		case proto.SessionHeld:
			typ = proto.EvSessionHeld
		case proto.SessionReleased:
			typ = proto.EvSessionReleased
		default:
			typ = proto.EvTurnEnd
		}
		ev, err := proto.NewEvent(int64(i+1), time.Now(), typ, 0, p)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, ev)
	}
	return out
}

// TestAForkCarriesTheParentsWholeCount: whatever the cut, the child ends with the parent's hits and hold.
func TestAForkCarriesTheParentsWholeCount(t *testing.T) {
	hit := func(c string, n int) proto.TripwireHit { return proto.TripwireHit{ID: "x", Command: c, Count: n} }
	parent := guardLog(t, proto.TurnEnd{}, hit("a", 1), hit("b", 2), hit("c", 3), proto.SessionHeld{Commands: []string{"a", "b", "c"}})
	for name, cut := range map[string]int{"before the first hit": 1, "after two": 3, "the whole": 5} {
		child := parent[:cut]
		add := CarryGuard(parent, child)
		var evs []proto.Event
		evs = append(evs, child...)
		for i, a := range add {
			ev, _ := proto.NewEvent(int64(len(child)+i+1), time.Now(), a.Type, 0, a.Payload)
			evs = append(evs, ev)
		}
		hits, held := GuardState(evs)
		if !held || len(hits) != 3 || hits[2] != "c" {
			t.Errorf("%s: hits %v held %v", name, hits, held)
		}
		if cut == 5 && len(add) != 0 {
			t.Errorf("%s: added %d events to a child that agrees", name, len(add))
		}
	}
	// The parent released after the cut: the child's own hits are released, the parent's (none, then one) are carried.
	parent = guardLog(t, hit("a", 1), hit("b", 2), proto.SessionReleased{By: "user"}, hit("z", 1))
	child := parent[:2]
	var evs []proto.Event
	evs = append(evs, child...)
	for i, a := range CarryGuard(parent, child) {
		ev, _ := proto.NewEvent(int64(len(child)+i+1), time.Now(), a.Type, 0, a.Payload)
		evs = append(evs, ev)
	}
	if hits, held := GuardState(evs); held || len(hits) != 1 || hits[0] != "z" {
		t.Errorf("after a release in the parent: hits %v held %v", hits, held)
	}
}

// loop.Fork is the one way a session is forked: a parent that was held after its cut leaves a held child with the whole count, in
// the same write as the fork, and an event the carry cannot write fails the fork with no child left behind.
func TestForkCarriesTheCountAndHoldInTheForkItself(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	l, err := st.Create("s_parent")
	if err != nil {
		t.Fatal(err)
	}
	l.Append(proto.EvSessionCreated, 0, proto.SessionCreated{Workspace: "/w", Model: "m", Mode: proto.ModeAsk})
	end, _ := l.Append(proto.EvTurnEnd, 0, proto.TurnEnd{StopReason: "end_turn"})
	for i, c := range []string{"git config core.fsmonitor a", "git config core.fsmonitor b", "git config core.fsmonitor c"} {
		l.Append(proto.EvTripwireHit, 0, proto.TripwireHit{ID: "h", Command: c, Count: i + 1})
	}
	l.Append(proto.EvSessionHeld, 0, proto.SessionHeld{Commands: []string{"a", "b", "c"}})
	l.Close()
	if err := Fork(st, "s_parent", "s_child", end.Seq); err != nil {
		t.Fatal(err)
	}
	ce, _ := st.Events("s_child", 0)
	hits, held := GuardState(ce)
	if len(hits) != 3 || !held {
		t.Errorf("the child has %d hits, held %v", len(hits), held)
	}
}
