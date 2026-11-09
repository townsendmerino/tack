package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/proto"
)

func event(t *testing.T, typ proto.EventType, payload any) proto.Event {
	t.Helper()
	ev, err := proto.NewEvent(0, time.Now(), typ, 0, payload)
	if err != nil {
		t.Fatal(err)
	}
	return ev
}

// TestStatusLineShowsAFirstLoad: the status line says "loading model on nobara" while the session's model server
// loads (DESIGN §8.2, §10), and drops it once the server is ready; then it shows the window's fill and reused share.
func TestStatusLineShowsAFirstLoad(t *testing.T) {
	var s Status
	s.Apply(event(t, proto.EvSessionCreated, proto.SessionCreated{Workspace: "/w", Model: "main", Mode: proto.ModePlan}))
	s.Apply(event(t, proto.EvModelStatus, proto.ModelStatus{Server: "nobara", State: "loading", Message: "loading model on nobara"}))
	if line := s.Line(); !strings.Contains(line, "loading model on nobara") || !strings.Contains(line, "plan") {
		t.Fatalf("while loading: %q", line)
	}
	s.Apply(event(t, proto.EvModelStatus, proto.ModelStatus{Server: "nobara", State: "ready"}))
	reused := 9860
	s.Window = 16384
	s.Apply(event(t, proto.EvUsage, proto.Usage{Request: 1, Server: "goinfer@nobara", Prompt: 11240, Reused: &reused}))
	line := s.Line()
	if strings.Contains(line, "loading") || !strings.Contains(line, "11.2k/16.4k") || !strings.Contains(line, "88% reused") {
		t.Fatalf("once serving: %q; want no load, and the window's fill with its reused share", line)
	}
}

// TestStatusLineShowsAMove: a turn moved off this machine's model under memory pressure says so until its server
// serves.
func TestStatusLineShowsAMove(t *testing.T) {
	var s Status
	s.Apply(event(t, proto.EvModelStatus, proto.ModelStatus{Server: "nobara", State: "moved", Message: "memory pressure is critical on this machine; using nobara"}))
	if line := s.Line(); !strings.Contains(line, "using nobara") || !strings.Contains(line, "nobara") {
		t.Fatalf("after a move: %q", line)
	}
	s.Apply(event(t, proto.EvModelStatus, proto.ModelStatus{Server: "nobara", State: "ready"}))
	if line := s.Line(); strings.Contains(line, "using nobara") {
		t.Fatalf("once serving: %q", line)
	}
}

// TestStatusLineDropsAMoveOnceARequestGoesOut: a moved turn's notice lasts until a request goes out, on whatever
// server: one back on this machine's model says no "ready", nor does a remote that was already serving.
func TestStatusLineDropsAMoveOnceARequestGoesOut(t *testing.T) {
	var s Status
	s.Apply(event(t, proto.EvModelStatus, proto.ModelStatus{Server: "nobara", State: "moved", Message: "memory pressure is warning on this machine; using nobara"}))
	s.Apply(event(t, proto.EvRequestSent, proto.RequestSent{Request: 1, Model: "q", Server: "goinfer@local"}))
	if line := s.Line(); strings.Contains(line, "using nobara") || !strings.Contains(line, "goinfer@local") {
		t.Fatalf("a turn back on this machine: %q; want the move gone and the local server named", line)
	}
}

// TestStatusLineIgnoresTheSummarizer: the summarizer's request (request 0, outside the conversation) changes neither the
// model nor the prompt the line shows.
func TestStatusLineIgnoresTheSummarizer(t *testing.T) {
	var s Status
	s.Apply(event(t, proto.EvRequestSent, proto.RequestSent{Request: 1, Model: "q", Server: "goinfer@local"}))
	s.Apply(event(t, proto.EvUsage, proto.Usage{Request: 1, Server: "goinfer@local", Prompt: 9000}))
	before := s.Line()
	s.Apply(event(t, proto.EvRequestSent, proto.RequestSent{Request: 0, Model: "q-fast", Server: "goinfer@local"}))
	s.Apply(event(t, proto.EvUsage, proto.Usage{Request: 0, Server: "goinfer@local", Prompt: 3000}))
	if after := s.Line(); after != before || strings.Contains(after, "q-fast") {
		t.Errorf("the summarizer moved the line: %q, was %q", after, before)
	}
}

// TestStatusLineNeverShowsAnUnreportedReuseAsZero: a server that reports no reuse gets the fill alone, not "0% reused"
// (DESIGN principle 6, measured or labelled).
func TestStatusLineNeverShowsAnUnreportedReuseAsZero(t *testing.T) {
	var s Status
	reused := 500
	s.Apply(event(t, proto.EvUsage, proto.Usage{Request: 1, Server: "openai@x", Prompt: 1000, Reused: &reused}))
	s.Apply(event(t, proto.EvUsage, proto.Usage{Request: 2, Server: "openai@x", Prompt: 1200}))
	if line := s.Line(); strings.Contains(line, "reused") || !strings.Contains(line, "1.2k") {
		t.Errorf("no reuse reported: %q; want the fill alone", line)
	}
}
