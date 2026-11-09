package loop

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server"
)

// The live summarizing step (Config.Summarize; DESIGN §7.6; docs/design/p1-notes.md, step 6): the owner's trigger is
// the last prompt at maskShare of the window that masking cannot bring under it, and it summarizes the oldest half of
// the turns before the last summaryKeepRequests requests. A "turn" here is a request and its response, not a user
// message: one agentic turn may be the whole session.

// summaryKeepRequests is how many of the latest requests' messages a summary always keeps verbatim.
const summaryKeepRequests = 3

// summaryCallTimeout bounds one summarizer request, so a hung call cannot hold a turn, or a drain waiting on it, past
// it; a call that runs out is a summarizer failure, and the previous body is kept.
var summaryCallTimeout = 3 * time.Minute

// summarizeDue summarizes before request next when masking, which ran first, did not bring the prompt under maskShare
// of the window. What masking freed is counted at summaryBytesPerToken. After an attempt that changed nothing it waits,
// as masking does after a compaction, until the prompt has grown summaryRegrow past that attempt's, so a summary
// that cannot help is not paid for on every request. A new turn and a masking compaction clear that floor. It returns the tokens the summarizer's requests cost,
// for the turn's budget; the session's is counted as they are logged.
func (s *Session) summarizeDue(ctx context.Context, srv server.Server, next int, cause int64) (int, error) {
	w := s.cfg.Source.Window()
	switch {
	case w <= 0 || (s.lastPrompt-s.freed/summaryBytesPerToken)*100 < maskShare*w:
		return 0, nil
	case s.sumFloor > 0 && s.lastPrompt-s.sumFloor < summaryRegrow(w, s.sumFloor):
		return 0, nil
	}
	spent, out, err := s.summarize(ctx, srv, next, cause)
	if err == nil && out == summaryUnchanged && ctx.Err() == nil {
		s.sumFloor = max(s.lastPrompt, 1)
	}
	return spent, err
}

// summaryOutcome is how far a summary attempt got.
type summaryOutcome int

const (
	summaryTooLittle summaryOutcome = iota // too few requests to summarize: nothing read, nothing paid
	summaryUnchanged                       // a summary was built and not used: summarizeDue backs off
	summaryApplied
)

// summarize replaces the oldest half of the assistant messages not yet summarized, less the last summaryKeepRequests,
// and everything before them, with one summary message, and records it, and says how far it got. It changes nothing when
// there is too little to summarize, when the summary would not be smaller than what it replaces, or when ctx ends
// during the summarizer call. Rebuild cuts the same way from the record (summaryCuts), so a resumed session's request
// is this one's. The events it summarizes are read back from the log, so the loop keeps no copy of them.
func (s *Session) summarize(ctx context.Context, srv server.Server, next int, cause int64) (spent int, out summaryOutcome, err error) {
	var open []int64 // the assistant messages no summary covers yet, in order
	for _, seq := range s.cuts.at {
		if seq > s.summarizedTo {
			open = append(open, seq)
		}
	}
	older := len(open) - summaryKeepRequests
	if older < 1 {
		return 0, summaryTooLittle, nil
	}
	at := open[(older+1)/2] // the first assistant message kept: the oldest half, rounded up, goes
	evs, err := s.cfg.Log.Events()
	if err != nil {
		return 0, summaryUnchanged, fmt.Errorf("loop: summarize: %w", err)
	}
	var prior, stretch []proto.Event
	var covered []int64 // every event before at: the summary covers each message it removes (Rebuild's gapless)
	for _, ev := range evs {
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
		return 0, summaryUnchanged, fmt.Errorf("loop: summarize: %w", err)
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
	// Each summarizer request is logged as the conversation's are, numbered 0 since it is outside their numbering, and
	// its tokens count toward the budgets. A log write that fails ends the turn, as it does for any other event.
	var logErr error
	send := func(ctx context.Context, req server.Request) (*server.Response, error) {
		reqEv, err := s.record(proto.EvRequestSent, cause, proto.RequestSent{Request: 0, Server: srv.Name(), Model: req.Model})
		if err != nil {
			logErr = err
			return nil, err
		}
		cctx, cancel := context.WithTimeout(ctx, summaryCallTimeout)
		defer cancel()
		resp, err := srv.Stream(cctx, req, func(server.Delta) {})
		if err != nil {
			return nil, err
		}
		u := usageOf(0, srv.Name(), resp)
		if _, err := s.record(proto.EvUsage, reqEv.Seq, u); err != nil {
			logErr = err
			return nil, err
		}
		spent += computedTokens(u)
		s.tokens += computedTokens(u)
		return resp, nil
	}
	sum := Summarize(ctx, send, s.fastModel(ctx, srv), prior, stretch, s.body)
	switch {
	case logErr != nil:
		return spent, summaryUnchanged, logErr
	case ctx.Err() != nil:
		return spent, summaryUnchanged, nil // the turn is ending; the loop's own check says why
	}
	text := summaryMessage(s.cfg.WS.Root(), sum)
	if len(text) >= before {
		return spent, summaryUnchanged, nil // nothing gained
	}
	// Re-retrieval comes out of what the summary frees, at most half of it, so it never makes a useful summary
	// unusable: with nothing that fits, the summary goes in without it.
	if s.cfg.SummaryRetrieve > 0 && s.cfg.Searcher != nil {
		all := append(append([]proto.Event(nil), prior...), stretch...)
		budget := min(s.cfg.SummaryRetrieve*s.cfg.Source.Window()*summaryBytesPerToken/100, (before-len(text))/2-2)
		if sec := retrieveSection(ctx, s.cfg.Searcher, openThreads(sum.Body, AnchorsOf(all)), budget); sec != "" {
			sum.Text += "\n\n" + sec // after the anchors: bodyOf leaves it out of the next summary's previous body
			text = summaryMessage(s.cfg.WS.Root(), sum)
		}
		if ctx.Err() != nil {
			return spent, summaryUnchanged, nil // the turn is ending, as after the summarizer's call
		}
	}
	if err := s.conv.Summarize(keep, text); err != nil {
		return spent, summaryUnchanged, fmt.Errorf("loop: summarize: %w", err)
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
	s.summarizedTo, s.body, s.sumFloor = covered[len(covered)-1], sum.Body, 0
	s.compacted, s.maskFloor = true, 0
	_, err = s.record(proto.EvCompaction, cause, proto.Compaction{Request: next, Trigger: "summary", Prompt: s.lastPrompt,
		Window: s.cfg.Source.Window(), Masked: []proto.MaskedResult{}, Before: before, After: len(text), Summary: text,
		Summarized: covered})
	return spent, summaryApplied, err
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

// summaryRegrow is how far the prompt must grow past floor, the prompt at an attempt that changed nothing, before
// another summary is tried in window w: maskRegrowShare of w, but never more than half the room left above floor, so a
// floor set near a full window can still be passed.
func summaryRegrow(w, floor int) int { return min(maskRegrowShare*w/100, max(w-floor, 0)/2) }
