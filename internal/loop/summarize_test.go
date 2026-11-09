package loop

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server"
)

// fakeSummarizer answers each Stream with its next reply, or err, and records what it was sent.
type fakeSummarizer struct {
	replies []string
	err     error
	sent    []server.Request
}

func (f *fakeSummarizer) Name() string { return "fake@test" }
func (f *fakeSummarizer) Stream(_ context.Context, req server.Request, _ func(server.Delta)) (*server.Response, error) {
	f.sent = append(f.sent, req)
	if f.err != nil {
		return nil, f.err
	}
	r := f.replies[0]
	f.replies = f.replies[1:]
	return &server.Response{Content: []proto.ModelBlock{{Type: "thinking", Thinking: "x"}, {Type: "text", Text: r}}}, nil
}
func (f *fakeSummarizer) CountTokens(context.Context, server.Request) (int, error) { return 0, nil }
func (f *fakeSummarizer) Models(context.Context) ([]server.ModelInfo, error)       { return nil, nil }

// body is a summary body with every heading, its Done section saying done.
func body(done string) string {
	var b strings.Builder
	for _, h := range SummaryHeadings {
		b.WriteString("## " + h + "\n\n")
		if h == "Done" {
			b.WriteString(done + "\n\n")
		} else {
			b.WriteString("(none)\n\n")
		}
	}
	return strings.TrimSpace(b.String())
}

// TestASummaryUpdatesThePreviousOne: the summarizer is asked to update the previous body with the stretch, on the
// thinking-off model, under the fixed system prompt, and its body sits between the user messages and the anchors.
func TestASummaryUpdatesThePreviousOne(t *testing.T) {
	srv := &fakeSummarizer{replies: []string{body("read edit.go; the test fails")}}
	prior := summaryLog(t, proto.EvUserMessage, user("Fix the rename bug."))
	stretch := session(t)[1:]
	s := Summarize(context.Background(), srv, "qwen-fast", prior, stretch, body("nothing yet"))
	if s.FellBack || s.Body != body("read edit.go; the test fails") {
		t.Fatalf("summary %+v", s)
	}
	if len(srv.sent) != 1 {
		t.Fatalf("%d calls, want 1", len(srv.sent))
	}
	req := srv.sent[0]
	msg := req.Messages[0].Content[0].Text
	if req.Model != "qwen-fast" || req.Thinking || len(req.Tools) != 0 || req.System != summarySystem || req.MaxTokens != summaryMaxTokens ||
		!strings.Contains(msg, "Previous summary:\n\n"+body("nothing yet")) || !strings.Contains(msg, "Tool result (error): --- FAIL: TestRenameKeepsMode") {
		t.Errorf("request %+v\nmessage:\n%s", req, msg)
	}
	task, later := strings.Index(s.Text, "## Task\n\nFix the rename bug."), strings.Index(s.Text, "## Later messages\n\nKeep the fix inside renameFile.")
	bodyAt, anchors := strings.Index(s.Text, "## Goal"), strings.Index(s.Text, "## Anchors (exact)")
	if task != 0 || later <= task || bodyAt <= later || anchors <= bodyAt {
		t.Errorf("summary text:\n%s", s.Text)
	}
}

// TestALongStretchIsSummarizedInChunks: each chunk updates the body the last one wrote.
func TestALongStretchIsSummarizedInChunks(t *testing.T) {
	var pairs []any
	for range 6 {
		pairs = append(pairs, proto.EvToolFinished, finished("c", proto.OutcomeOK, strings.Repeat("output line\n", summaryItemBytes/12)))
	}
	stretch := summaryLog(t, pairs...)
	srv := &fakeSummarizer{replies: []string{body("one"), body("two")}}
	s := Summarize(context.Background(), srv, "m-fast", nil, stretch, "")
	if len(srv.sent) != 2 || s.FellBack || s.Body != body("two") {
		t.Fatalf("%d calls, summary %+v", len(srv.sent), s)
	}
	if !strings.Contains(srv.sent[0].Messages[0].Content[0].Text, "Previous summary:\n\n(none yet)") ||
		!strings.Contains(srv.sent[1].Messages[0].Content[0].Text, "Previous summary:\n\n"+body("one")) {
		t.Error("the second chunk did not update the first's body")
	}
	for i, r := range srv.sent {
		if n := len(r.Messages[0].Content[0].Text); n > summaryChunkTokens*summaryBytesPerToken+len(body("one"))+200 {
			t.Errorf("chunk %d sends %d bytes", i+1, n)
		}
	}
}

// TestASummarizerFailureKeepsThePreviousBody: an error, a reply without the headings, or no model keeps the previous
// body, and the text still carries every user message and the anchors.
func TestASummarizerFailureKeepsThePreviousBody(t *testing.T) {
	for name, c := range map[string]struct {
		srv   *fakeSummarizer
		model string
		calls int
	}{
		"an error":         {&fakeSummarizer{err: errors.New("connection refused")}, "m-fast", 1},
		"no headings":      {&fakeSummarizer{replies: []string{"Here is a summary: it went fine."}}, "m-fast", 1},
		"no fast model":    {&fakeSummarizer{}, "", 0},
		"headings dropped": {&fakeSummarizer{replies: []string{strings.Replace(body("x"), "## Decisions", "Decisions", 1)}}, "m-fast", 1},
	} {
		s := Summarize(context.Background(), c.srv, c.model, nil, session(t), body("before"))
		if !s.FellBack || s.Reason == "" || s.Body != body("before") || len(c.srv.sent) != c.calls ||
			!strings.Contains(s.Text, "Keep the fix inside renameFile.") || !strings.Contains(s.Text, body("before")) ||
			!strings.Contains(s.Text, "## Anchors (exact)") {
			t.Errorf("%s: %d calls, summary %+v", name, len(c.srv.sent), s)
		}
	}
}

// TestTheFirstSummaryFallsBackToNoBody: with no previous body, a failure leaves the user messages and anchors alone,
// the fallback summary.
func TestTheFirstSummaryFallsBackToNoBody(t *testing.T) {
	s := Summarize(context.Background(), &fakeSummarizer{err: errors.New("x")}, "m-fast", nil, session(t), "")
	if !s.FellBack || s.Body != "" || s.Text != FallbackSummary(session(t)) {
		t.Errorf("summary %+v", s)
	}
}

// TestTheSummarizerPromptIsStable: the system prompt names every heading and carries nothing that changes between
// sessions, so it is a cached prefix.
func TestTheSummarizerPromptIsStable(t *testing.T) {
	for _, h := range SummaryHeadings {
		if !strings.Contains(summarySystem, h) {
			t.Errorf("the system prompt does not name %q", h)
		}
	}
	a := summaryRequest("m", "", "turns one")
	b := summaryRequest("m", body("x"), "turns two")
	if a.System != b.System {
		t.Error("the system prompt changes between calls")
	}
}
