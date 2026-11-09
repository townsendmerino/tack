package core

import (
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/sandbox"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
	"github.com/townsendmerino/tack/internal/store"
)

// TestHalt: a halt stops the running turn mid-request (the request to the model server is cancelled), records
// daemon.halted then turn.end halted, and refuses new turns until Resume.
func TestHalt(t *testing.T) {
	inRequest, cancelled := make(chan struct{}, 1), make(chan struct{}, 1)
	probes := goinfertest.NewScripted("m", 16384) // answers everything but the generation itself
	f := newFixtureOn(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			probes.ServeHTTP(w, r)
			return
		}
		_, _ = io.Copy(io.Discard, r.Body) // the server notices the client going away only once the body is read
		select {
		case inRequest <- struct{}{}:
		default:
		}
		<-r.Context().Done() // generation runs until the client goes away
		select {
		case cancelled <- struct{}{}:
		default:
		}
	}))
	sid := f.session(t)
	if err := f.client.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: sid}, nil); err != nil {
		t.Fatal(err)
	}
	prompt := func() error {
		return f.client.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: sid,
			Content: []proto.ContentBlock{proto.Text("hello")}}, nil)
	}
	if err := prompt(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-inRequest:
	case <-time.After(10 * time.Second):
		t.Fatal("the turn never reached the model server")
	}
	f.core.Halt(proto.DaemonHalted{Trigger: proto.HaltByCommand, By: "test", Reason: "drill"})
	evs := f.waitTurnEnd(t)
	select {
	case <-cancelled:
	case <-time.After(10 * time.Second):
		t.Fatal("the model server's request was not cancelled")
	}
	var types []proto.EventType
	for _, ev := range evs {
		types = append(types, ev.Type)
	}
	n := len(evs)
	if n < 2 || evs[n-2].Type != proto.EvDaemonHalted {
		t.Fatalf("daemon.halted must precede turn.end: %v", types)
	}
	if v, _ := evs[n-2].Decode(); v.(proto.DaemonHalted).Reason != "drill" {
		t.Fatalf("daemon.halted %+v", v)
	}
	if v, _ := evs[n-1].Decode(); v.(proto.TurnEnd).StopReason != proto.StopHalted {
		t.Fatalf("turn.end %+v", v)
	}

	err := prompt()
	var perr *proto.Error
	if !errors.As(err, &perr) || perr.Code != proto.CodeHalted || !strings.Contains(perr.Message, "tack resume") {
		t.Fatalf("a prompt while halted: %v", err)
	}
	f.core.Resume()
	if err := prompt(); err != nil {
		t.Fatalf("a prompt after resume: %v", err)
	}
	<-inRequest
	f.core.Halt(proto.DaemonHalted{Trigger: proto.HaltByFile})
	f.waitTurnEnd(t)
}

// TestHaltRecordsARefusedSweep: Halt sweeps each session's own target, and a refused sweep is recorded in that
// session's log as daemon.halt_refused with the processes it listed. The sweep here is a stand-in; nothing is signalled.
func TestHaltRecordsARefusedSweep(t *testing.T) {
	f := newFixture(t)
	sid := f.session(t)
	if err := f.client.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: sid}, nil); err != nil {
		t.Fatal(err)
	}
	old := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	var swept []sandbox.Target
	f.core.opt.Sweep = func(tg sandbox.Target, dryRun bool) (sandbox.Report, error) {
		swept = append(swept, tg)
		if dryRun {
			t.Error("Halt asked for a dry run")
		}
		return sandbox.Report{Older: []sandbox.Proc{{PID: 4242, Name: "icdd", Start: old}}, Refused: "1 process(es) carry mark …"}, nil
	}
	f.core.Halt(proto.DaemonHalted{Trigger: proto.HaltByCommand, By: "test"})
	if len(swept) != 1 || !strings.HasPrefix(swept[0].Mark, "s-") || swept[0].Since.IsZero() || time.Since(swept[0].Since) > time.Minute {
		t.Fatalf("swept %+v; want the session's own target", swept)
	}
	select {
	case ev := <-f.events:
		if ev.Type == proto.EvSessionCreated { // the subscription's replay
			ev = <-f.events
		}
		v, _ := ev.Decode()
		r, ok := v.(proto.DaemonHaltRefused)
		if !ok || len(r.Processes) != 1 || r.Processes[0].PID != 4242 || !r.Processes[0].Start.Equal(old) || r.Reason == "" {
			t.Fatalf("event %s %+v", ev.Type, v)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no daemon.halt_refused")
	}
}

// TestHaltLeavesLogsAsTurnsNeedThem: a Halt whose sweep refuses writes daemon.halt_refused to each session's log.
// For an idle session that takes the log's lock back, and the Halt gives it up again, so tack rewind can still write
// the log; it never gives up a running turn's (the check and the idle are made under the session's lock, as a turn's
// end makes them).
func TestHaltLeavesLogsAsTurnsNeedThem(t *testing.T) {
	f := newFixture(t)
	id := f.session(t)
	f.core.opt.Sweep = func(sandbox.Target, bool) (sandbox.Report, error) {
		return sandbox.Report{Refused: "1 process(es) carry mark …"}, nil
	}
	f.core.Halt(proto.DaemonHalted{Trigger: proto.HaltByCommand, By: "test"})
	l, err := f.core.opt.Store.OpenLog(id)
	if err != nil {
		t.Fatalf("an idle session's log after a Halt wrote to it: %v, want it free", err)
	}
	evs, _ := f.core.opt.Store.Events(id, 0)
	l.Close()
	if evs[len(evs)-1].Type != proto.EvDaemonHaltRefused {
		t.Fatalf("the log ends %s, want daemon.halt_refused", evs[len(evs)-1].Type)
	}
}

// TestHaltKeepsARunningTurnsLog: a Halt that records a refused sweep while a turn is still running leaves the log
// with the turn, which gives it up at its end.
func TestHaltKeepsARunningTurnsLog(t *testing.T) {
	f := newFixtureOn(t, stuckModel())
	st := f.core.opt.Store
	id := f.session(t)
	f.core.opt.Sweep = func(sandbox.Target, bool) (sandbox.Report, error) {
		return sandbox.Report{Refused: "1 process(es) carry mark …"}, nil
	}
	release := make(chan struct{})
	hold := func() { // the turn, cancelled by the Halt, has not ended when Halt records
		select {
		case <-release:
		case <-time.After(5 * time.Second):
		}
	}
	turnEndHook.Store(&hold)
	t.Cleanup(func() { turnEndHook.Store(nil) })
	if err := f.client.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: id, Content: []proto.ContentBlock{proto.Text("x")}}, nil); err != nil {
		t.Fatal(err)
	}
	f.core.Halt(proto.DaemonHalted{Trigger: proto.HaltByCommand, By: "test"})
	if _, err := st.OpenLog(id); !errors.Is(err, store.ErrLogBusy) {
		t.Fatalf("a running turn's log after Halt recorded beside it: %v, want ErrLogBusy, still the turn's", err)
	}
	close(release)
	for deadline := time.Now().Add(2 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		l, err := st.OpenLog(id)
		if err == nil {
			l.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the log once the turn ended: %v, want it free", err)
		}
	}
}

// TestHaltPassesOnWhatIsWrittenAroundIt: a turn ending while Halt records a refused sweep cannot give the log up
// between Halt taking it back and appending to it, so nothing tack rewind writes is swallowed: every event in the log
// reaches the subscriber.
func TestHaltPassesOnWhatIsWrittenAroundIt(t *testing.T) {
	inRequest := make(chan struct{}, 1)
	probes := goinfertest.NewScripted("m", 16384)
	f := newFixtureOn(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			probes.ServeHTTP(w, r)
			return
		}
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case inRequest <- struct{}{}:
		default:
		}
		<-r.Context().Done()
	}))
	st := f.core.opt.Store
	id := f.session(t)
	ws := f.workspace(t)
	if err := f.client.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: id}, nil); err != nil {
		t.Fatal(err)
	}
	f.core.opt.Sweep = func(sandbox.Target, bool) (sandbox.Report, error) {
		return sandbox.Report{Refused: "1 process(es) carry mark …"}, nil
	}
	between := make(chan struct{})
	turnEnd := func() { // the turn ends only once Halt has taken the log back
		select {
		case <-between:
		case <-time.After(5 * time.Second):
		}
	}
	turnEndHook.Store(&turnEnd)
	haltHook = func() { // and tack rewind writes the moment the log is free
		close(between)
		deadline := time.Now().Add(500 * time.Millisecond)
		for time.Now().Before(deadline) {
			if rw, err := st.OpenLog(id); err == nil {
				rw.Append(proto.EvRewind, 1, proto.Rewind{To: 1, Workspace: ws})
				rw.Close()
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	t.Cleanup(func() { turnEndHook.Store(nil); haltHook = nil })
	if err := f.client.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: id, Content: []proto.ContentBlock{proto.Text("hello")}}, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-inRequest:
	case <-time.After(10 * time.Second):
		t.Fatal("the turn never reached the model server")
	}
	f.core.Halt(proto.DaemonHalted{Trigger: proto.HaltByCommand, By: "test"})
	heard := map[int64]bool{}
	for deadline := time.After(5 * time.Second); !heard[-1]; {
		select {
		case ev := <-f.events:
			heard[ev.Seq] = true
			if ev.Type == proto.EvDaemonHaltRefused {
				heard[-1] = true
			}
		case <-deadline:
			t.Fatalf("no daemon.halt_refused; heard %v", heard)
		}
	}
	// The turn's end comes after Halt's record, and gives the log back; what it emitted follows.
	for deadline := time.Now().Add(2 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		l, err := st.OpenLog(id)
		if err == nil {
			l.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the session's log once the turn ended: %v, want it free", err)
		}
	}
	for quiet := false; !quiet; {
		select {
		case ev := <-f.events:
			heard[ev.Seq] = true
		case <-time.After(100 * time.Millisecond):
			quiet = true
		}
	}
	evs, err := st.Events(id, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range evs {
		if !heard[ev.Seq] {
			t.Errorf("the subscriber never heard %s at seq %d", ev.Type, ev.Seq)
		}
	}
}

// TestHaltNeverOvertakesATurnsEvent: a turn's daemon.halted, recorded as Halt records its refused sweep, reaches a live
// subscriber. Halt's record waits until the turn has appended daemon.halted, and the turn's emit of it waits until
// Halt is done: without one lock across each event's append and fan-out, halt_refused takes the higher seq, goes out
// first, and the subscriber's seq filter drops daemon.halted.
func TestHaltNeverOvertakesATurnsEvent(t *testing.T) {
	f := newFixtureOn(t, stuckModel())
	st := f.core.opt.Store
	id := f.session(t)
	if err := f.client.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: id}, nil); err != nil {
		t.Fatal(err)
	}
	f.core.opt.Sweep = func(sandbox.Target, bool) (sandbox.Report, error) {
		return sandbox.Report{Refused: "1 process(es) carry mark …"}, nil
	}
	haltDone := make(chan struct{})
	hold := func(ev proto.Event) { // the turn's daemon.halted goes out only once Halt has recorded
		if ev.Type == proto.EvDaemonHalted {
			select {
			case <-haltDone:
			case <-time.After(2 * time.Second):
			}
		}
	}
	emitHook.Store(&hold)
	haltHook = func() { // and Halt records only once the turn has appended daemon.halted, or gives up waiting
		for deadline := time.Now().Add(500 * time.Millisecond); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
			evs, _ := st.Events(id, 0)
			for _, ev := range evs {
				if ev.Type == proto.EvDaemonHalted {
					return
				}
			}
		}
	}
	t.Cleanup(func() { emitHook.Store(nil); haltHook = nil })
	if err := f.client.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: id, Content: []proto.ContentBlock{proto.Text("x")}}, nil); err != nil {
		t.Fatal(err)
	}
	f.core.Halt(proto.DaemonHalted{Trigger: proto.HaltByCommand, By: "test"})
	close(haltDone)
	heard := map[int64]bool{}
	for deadline := time.After(5 * time.Second); ; {
		select {
		case ev := <-f.events:
			heard[ev.Seq] = true
			if ev.Type != proto.EvTurnEnd {
				continue
			}
		case <-deadline:
			t.Fatalf("no turn.end; heard %v", heard)
		}
		break
	}
	for quiet := false; !quiet; {
		select {
		case ev := <-f.events:
			heard[ev.Seq] = true
		case <-time.After(100 * time.Millisecond):
			quiet = true
		}
	}
	evs, err := st.Events(id, 0)
	if err != nil {
		t.Fatal(err)
	}
	var types []proto.EventType
	for _, ev := range evs {
		types = append(types, ev.Type)
		if !heard[ev.Seq] {
			t.Errorf("the subscriber never heard %s at seq %d", ev.Type, ev.Seq)
		}
	}
	if !slices.Contains(types, proto.EvDaemonHalted) || !slices.Contains(types, proto.EvDaemonHaltRefused) {
		t.Fatalf("the log %v lacks daemon.halted or daemon.halt_refused", types)
	}
}
