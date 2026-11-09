package core

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/sandbox"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
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
