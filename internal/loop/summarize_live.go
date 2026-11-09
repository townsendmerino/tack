package loop

import (
	"context"
	"fmt"
	"slices"

	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server"
)

// The live summarizing step (Config.Summarize; DESIGN §7.6; docs/design/p1-notes.md, step 6): the owner's trigger is
// the last prompt at maskShare of the window that masking cannot bring under it, and it summarizes the oldest half of
// the turns before the last summaryKeepRequests requests.

// summaryKeepRequests is how many of the latest requests' messages a summary always keeps verbatim.
const summaryKeepRequests = 3

// summarizeDue summarizes before request next when masking, which ran first, did not bring the prompt under maskShare
// of the window. What masking freed is counted at summaryBytesPerToken.
func (s *Session) summarizeDue(ctx context.Context, srv server.Server, next int, cause int64) error {
	w := s.cfg.Source.Window()
	if w <= 0 || (s.lastPrompt-s.freed/summaryBytesPerToken)*100 < maskShare*w {
		return nil
	}
	return s.summarize(ctx, srv, next, cause)
}

// summarize replaces the oldest half of the assistant messages not yet summarized, less the last summaryKeepRequests,
// and everything before them, with one summary message, and records it. It changes nothing when there is too little
// to summarize, when the summary would not be smaller than what it replaces, or when ctx ends during the summarizer
// call. Rebuild cuts the same way from the record (summaryCuts), so a resumed session's request is this one's.
func (s *Session) summarize(ctx context.Context, srv server.Server, next int, cause int64) error {
	var open []int64 // the assistant messages no summary covers yet, in order
	for _, seq := range s.cuts.at {
		if seq > s.summarizedTo {
			open = append(open, seq)
		}
	}
	older := len(open) - summaryKeepRequests
	if older < 1 {
		return nil
	}
	at := open[(older+1)/2] // the first assistant message kept: the oldest half, rounded up, goes
	var prior, stretch []proto.Event
	var covered []int64 // every event before at: the summary covers each message it removes (Rebuild's gapless)
	for _, ev := range s.events {
		if ev.Seq >= at {
			break
		}
		covered = append(covered, ev.Seq)
		if ev.Seq <= s.summarizedTo {
			prior = append(prior, ev)
		} else {
			stretch = append(stretch, ev)
		}
	}
	keep, cutAt, err := s.cuts.cut(covered)
	if err == nil && cutAt != at {
		err = fmt.Errorf("the cut lands at seq %d, not %d", cutAt, at)
	}
	if err != nil {
		return fmt.Errorf("loop: summarize: %w", err)
	}
	before := 0
	var ids []string // the tool calls whose results the summary removes
	for _, m := range s.conv.Messages()[:keep] {
		for _, b := range m.Content {
			before += len(b.Text) + len(b.Thinking) + len(b.Input) + len(b.Result)
			if b.Type == "tool_result" {
				ids = append(ids, b.ToolUseID)
			}
		}
	}
	sum := Summarize(ctx, srv, s.fastModel(ctx, srv), prior, stretch, s.body)
	if ctx.Err() != nil {
		return nil // the turn is ending; the loop's own check says why
	}
	text := summaryMessage(s.cfg.WS.Root(), sum)
	if len(text) >= before {
		return nil // nothing gained
	}
	if err := s.conv.Summarize(keep, text); err != nil {
		return fmt.Errorf("loop: summarize: %w", err)
	}
	s.cuts.summarized(keep)
	for i := range s.obs { // gone from the window: masking must not stub them later
		if slices.Contains(ids, s.obs[i].id) {
			s.obs[i].masked = true
		}
	}
	if s.cfg.Sections != nil && len(ids) > 0 {
		s.cfg.Sections.Masked(ids) // their notes left with them, so their sections may load again
	}
	s.summarizedTo, s.body = covered[len(covered)-1], sum.Body
	s.compacted, s.maskFloor = true, 0
	_, err = s.record(proto.EvCompaction, cause, proto.Compaction{Request: next, Trigger: "summary", Prompt: s.lastPrompt,
		Window: s.cfg.Source.Window(), Masked: []proto.MaskedResult{}, Before: before, After: len(text), Summary: text,
		Summarized: covered})
	return err
}

// fastModel is the summarizing model: the thinking-off alias srv lists for the session's model, "" when it lists
// none. A lookup that fails is tried again at the next summary.
func (s *Session) fastModel(ctx context.Context, srv server.Server) string {
	if s.fastKnown {
		return s.fast
	}
	ms, err := srv.Models(ctx)
	if err != nil {
		return ""
	}
	for _, m := range ms {
		if m.ID == s.conv.Model {
			s.fast = m.FastModel
		}
	}
	s.fastKnown = true
	return s.fast
}
