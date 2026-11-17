package core

import (
	"net"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/townsendmerino/tack/internal/perms"
	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/sandbox"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
	"github.com/townsendmerino/tack/internal/store"
)

func guardOptions(o *Options) {
	o.TripwireThreshold = 3
	o.Edit = func(root string, _ sandbox.Target) (EditKit, error) {
		return EditKit{Shell: noShell{}, Engine: &perms.Engine{Root: root, Sandboxed: true, Path: os.Getenv("PATH")}}, nil
	}
}

// client connects to c with the token, attended or not.
func client(t *testing.T, c *Core, attended bool) (*Client, chan proto.Event) {
	t.Helper()
	a, b := net.Pipe()
	t.Cleanup(func() { a.Close() })
	go c.Serve(c.ctx, b)
	cl := NewClient(a)
	events := make(chan proto.Event, 256)
	cl.OnEvent = func(p proto.SessionEventParams) { events <- p.Event }
	if err := cl.Call(proto.MethodInitialize, proto.InitializeParams{Client: proto.ClientInfo{Name: "t", Version: "0", Attended: attended},
		Protocol: proto.Version, Token: "tok"}, nil); err != nil {
		t.Fatal(err)
	}
	return cl, events
}

// TestTheThirdCountedCallHoldsASessionAndOnlyAPersonReleasesIt: three calls with a shape that has no honest use hold the session: the
// turn ends held, a prompt is refused (CodeHeld), a client no one is at cannot release it, one a person is at can, and the session then
// takes a prompt. The hold survives a fork (the child is held too) and a daemon restart (a new core over the same store).
func TestTheThirdCountedCallHoldsASessionAndOnlyAPersonReleasesIt(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	script := goinfertest.NewScripted("m", 16384,
		goinfertest.Calls("", "b1", "bash", `{"command":"go test \\-exec id"}`), goinfertest.Calls("", "b2", "bash", `{"command":"git config core.fsmonitor id"}`),
		goinfertest.Calls("", "b3", "bash", `{"command":"go build -ldflags=-extld=./x ."}`), goinfertest.Says("never"), goinfertest.Says("after the release"))
	ts := httptest.NewServer(script)
	t.Cleanup(ts.Close)
	c, _, _ := daemonOn(t, st, ts.URL, guardOptions)
	person, events := client(t, c, true)
	headless, _ := client(t, c, false)
	var sess proto.SessionNewResult
	if err := person.Call(proto.MethodSessionNew, proto.SessionNewParams{Workspace: gitWorkspace(t, ""), Model: "main", Mode: proto.ModeAsk}, &sess); err != nil {
		t.Fatal(err)
	}
	person.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: sess.Session}, nil)
	prompt := func(cl *Client, sid proto.SessionID) error {
		return cl.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: sid, Content: []proto.ContentBlock{proto.Text("go")}}, nil)
	}
	if err := prompt(person, sess.Session); err != nil {
		t.Fatal(err)
	}
	var hits, held int
	var end proto.TurnEnd
	for _, ev := range untilTurnEnd(t, events) {
		v, _ := ev.Decode()
		switch p := v.(type) {
		case proto.TripwireHit:
			hits++
		case proto.SessionHeld:
			held++
			if len(p.Commands) != 3 {
				t.Errorf("session.held lists %v", p.Commands)
			}
		case proto.TurnEnd:
			end = p
		}
	}
	if hits != 3 || held != 1 || end.StopReason != proto.StopHeld {
		t.Fatalf("hits %d, held %d, stop %q", hits, held, end.StopReason)
	}
	if err := prompt(person, sess.Session); errCode(err) != proto.CodeHeld {
		t.Errorf("a prompt while held: %v", err)
	}
	// Who may release.
	un := c.opt.ClientToken
	_ = un
	if err := headless.Call(proto.MethodSessionRelease, proto.SessionReleaseParams{Session: sess.Session}, nil); err == nil {
		t.Error("a client no person is at released the session")
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	// A daemon restart over the same store: still held, and the fork of a held session is held.
	c2, _, _ := daemonOn(t, st, ts.URL, guardOptions)
	person2, events2 := client(t, c2, true)
	if err := prompt(person2, sess.Session); errCode(err) != proto.CodeHeld {
		t.Fatalf("a prompt after a restart: %v", err)
	}
	var fork proto.SessionForkResult
	evs, _ := st.Events(sess.Session, 0)
	if err := person2.Call(proto.MethodSessionFork, proto.SessionForkParams{Session: sess.Session, AtSeq: evs[len(evs)-1].Seq}, &fork); err != nil {
		t.Fatal(err)
	}
	if err := prompt(person2, fork.Session); errCode(err) != proto.CodeHeld {
		t.Errorf("a prompt in the fork of a held session: %v", err)
	}
	// Release by the person: recorded, and the session takes a prompt.
	person2.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: sess.Session}, nil)
	if err := person2.Call(proto.MethodSessionRelease, proto.SessionReleaseParams{Session: sess.Session}, nil); err != nil {
		t.Fatal(err)
	}
	await(t, events2, proto.EvSessionReleased) // the replay of the held turn comes first, then the live release
	if err := prompt(person2, sess.Session); err != nil {
		t.Fatalf("a prompt after release: %v", err)
	}
	await(t, events2, proto.EvTurnEnd)
}

// TestAnUnprivilegedClientCannotRelease: the token is needed, whatever else.
func TestAnUnprivilegedClientCannotRelease(t *testing.T) {
	f := newFixture(t)
	sid := f.session(t)
	if err := f.unprivileged(t).Call(proto.MethodSessionRelease, proto.SessionReleaseParams{Session: sid}, nil); errCode(err) != proto.CodeUnauthorized {
		t.Errorf("an unprivileged release: %v", err)
	}
}

// TestRefusedEditedRulesCountTowardTheHold: an edit of a shown rule that widens it is refused, and counts; the third holds the session
// and ends the turn held. An edit that narrows is not counted.
func TestRefusedEditedRulesCountTowardTheHold(t *testing.T) {
	f := newFixture(t, goinfertest.Calls("", "b1", "bash", `{"command":"ls -la"}`), goinfertest.Says("done"))
	f.core.opt.TripwireThreshold = 3
	eng := &perms.Engine{Sandboxed: true, Path: os.Getenv("PATH")}
	f.core.opt.Edit = func(root string, _ sandbox.Target) (EditKit, error) {
		eng.Root = root
		return EditKit{Shell: noShell{}, Engine: eng}, nil
	}
	sid, err := f.sessionIn(t, gitWorkspace(t, ""), proto.ModeAsk)
	if err != nil {
		t.Fatal(err)
	}
	cl, events, _ := f.privileged(t)
	cl.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: sid}, nil)
	if err := cl.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: sid, Content: []proto.ContentBlock{proto.Text("list")}}, nil); err != nil {
		t.Fatal(err)
	}
	qev := await(t, events, proto.EvPermissionRequested)
	v, _ := qev.Decode()
	shown := v.(proto.PermissionRequested).Rules[0]
	widen := proto.PermissionRespondParams{Decision: proto.DecisionAllow, Scope: "session", Rule: "ls"}
	for i := 1; i <= 3; i++ {
		if err := respondWith(cl, sid, qev, widen); errCode(err) != proto.CodeInvalidParams {
			t.Fatalf("widening %d: %v", i, err)
		}
		if hits := guardHits(f, sid); hits != i {
			t.Fatalf("after %d widening edits the log has %d tripwire.hit", i, hits)
		}
	}
	end := untilTurnEnd(t, events)
	if v, _ := end[len(end)-1].Decode(); v.(proto.TurnEnd).StopReason != proto.StopHeld {
		t.Errorf("the turn ended %+v", v)
	}
	// An honest edit that narrows is not counted: a fresh session.
	f2, cl2, events2, sid2, _, q2 := bashQuestion(t)
	f2.core.opt.TripwireThreshold = 3
	if err := respondWith(cl2, sid2, q2, proto.PermissionRespondParams{Decision: proto.DecisionAllow, Scope: "session", Rule: shown + " /tmp"}); err != nil {
		t.Fatal(err)
	}
	untilTurnEnd(t, events2)
	if hits := guardHits(f2, sid2); hits != 0 {
		t.Errorf("a narrowing edit counted: %d", hits)
	}
}

func guardHits(f *fixture, sid proto.SessionID) int {
	evs, _ := f.core.opt.Store.Events(sid, 0)
	n := 0
	for _, ev := range evs {
		if ev.Type == proto.EvTripwireHit {
			n++
		}
	}
	return n
}
