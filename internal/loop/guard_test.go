package loop

import (
	"context"
	"strings"
	"testing"

	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
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
