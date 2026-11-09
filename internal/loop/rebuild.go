package loop

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server"
	"github.com/townsendmerino/tack/internal/store"
)

// Rebuild replays a session's persisted events into the conversation they describe (principle 4: the log is the
// truth). A rebuilt conversation's next request is the one the live loop would send, byte for byte, so a resumed
// session keeps its cached prefix. A tool call that started and never finished (a crash) is answered with an error
// result saying its outcome is unknown; it is never re-run (DESIGN §6.1). base carries the session's settings: its
// model, system prompt, tools, MaxTokens, Sampling and ToolChoice, as New set them; its messages are ignored.
func Rebuild(evs []proto.Event, workspaceRoot string, base server.Conversation) (*server.Conversation, error) {
	conv, _, err := rebuild(evs, workspaceRoot, base)
	return conv, err
}

// summaryState is what a session resumed from its log needs to summarize again as the live one would: where the
// assistant messages are (cuts), the last seq a summary covered (to), and that summary's body (body).
type summaryState struct {
	cuts summaryCuts
	to   int64
	body string
}

func rebuild(evs []proto.Event, workspaceRoot string, base server.Conversation) (*server.Conversation, summaryState, error) {
	var st summaryState
	conv := &server.Conversation{Model: base.Model, System: base.System, Tools: base.Tools, MaxTokens: base.MaxTokens,
		Sampling: base.Sampling, ToolChoice: base.ToolChoice}
	var results []server.ToolResult
	cuts := &st.cuts
	var made []int64 // the seq of every event that made or added to a message
	turns := 0
	flush := func() error {
		if len(results) == 0 {
			return nil
		}
		err := conv.AnswerTools(results)
		results = nil
		return err
	}
	for _, ev := range evs {
		v, err := ev.Decode()
		if err != nil {
			return nil, st, fmt.Errorf("rebuild: seq %d: %w", ev.Seq, err)
		}
		switch p := v.(type) {
		case proto.UserMessage:
			var text strings.Builder
			for _, b := range p.Content {
				text.WriteString(b.Text)
			}
			if err := conv.BeginTurn(UserText(turns == 0, workspaceRoot, text.String()), false); err != nil {
				return nil, st, fmt.Errorf("rebuild: seq %d: %w", ev.Seq, err)
			}
			turns++
			made = append(made, ev.Seq)
		case proto.AssistantMessage:
			cuts.add(ev.Seq, len(conv.Messages()))
			made = append(made, ev.Seq)
			if err := conv.AddResponse(p.Content); err != nil {
				return nil, st, fmt.Errorf("rebuild: seq %d: %w", ev.Seq, err)
			}
		case proto.ToolFinished:
			made = append(made, ev.Seq)
			results = append(results, server.ToolResult{ID: p.ID, Result: p.Result, IsError: p.Outcome != proto.OutcomeOK})
			if len(results) == len(conv.Pending()) {
				if err := flush(); err != nil {
					return nil, st, fmt.Errorf("rebuild: seq %d: %w", ev.Seq, err)
				}
			}
		case proto.Compaction:
			for _, m := range p.Masked {
				if !conv.ReplaceResult(m.ID, m.Stub) {
					return nil, st, fmt.Errorf("rebuild: seq %d: compaction masks %q, which no result answers", ev.Seq, m.ID)
				}
			}
			// Masks first, as the live loop applies them: one may name a result the summary then removes.
			if p.Summary == "" && len(p.Summarized) > 0 {
				return nil, st, fmt.Errorf("rebuild: seq %d: a compaction lists %d summarized events but no summary", ev.Seq, len(p.Summarized))
			}
			if p.Summary != "" {
				keep, at, err := cuts.cut(p.Summarized)
				if err == nil {
					err = gapless(made, slices.Max(p.Summarized), at)
				}
				if err == nil {
					err = conv.Summarize(keep, p.Summary)
				}
				if err != nil {
					return nil, st, fmt.Errorf("rebuild: seq %d: %w", ev.Seq, err)
				}
				cuts.summarized(keep)
				st.to, st.body = slices.Max(p.Summarized), bodyOf(p.Summary)
			}
		case proto.ErrorEvent:
			if p.Class == NoToolCallClass {
				made = append(made, ev.Seq)
				if err := conv.Nudge(NoToolCallNudge); err != nil {
					return nil, st, fmt.Errorf("rebuild: seq %d: %w", ev.Seq, err)
				}
			}
		case proto.TurnEnd:
			if err := conv.EndTurn(); err != nil {
				return nil, st, fmt.Errorf("rebuild: seq %d: %w", ev.Seq, err)
			}
		}
	}
	// A crash mid-turn: answer what finished, and mark the rest outcome unknown.
	if pending := conv.Pending(); len(pending) > 0 {
		done := map[string]bool{}
		for _, r := range results {
			done[r.ID] = true
		}
		for _, id := range pending {
			if !done[id] {
				results = append(results, server.ToolResult{ID: id, IsError: true,
					Result: "error: outcome unknown: tack stopped while this call was running; it was not re-run"})
			}
		}
		if err := flush(); err != nil {
			return nil, st, err
		}
	}
	return conv, st, nil
}

// Resume returns the loop of a session reopened from its log (DESIGN §6.1): history is every persisted event so far,
// and must not end inside a turn (a crash's open turn is closed first, by core's recovery). The conversation is
// Rebuild's, so the next request keeps the cached prefix; the session's counts toward its budgets are the log's:
// requests sent, tokens the server computed, and the tool calls the loop counted: each that reached a permission
// decision or started, and done (callsRan).
//
// What the live loop keeps only in memory starts empty: the files the model has seen, so edit asks for a fresh read
// of a file first, and the observations masking reads, so results from before the resume are never masked.
func Resume(cfg Config, history []proto.Event) (*Session, error) {
	if st := store.Inspect(history); st.InTurn {
		return nil, errors.New("loop: the log ends inside a turn; close it before resuming")
	}
	s := New(cfg)
	conv, st, err := rebuild(history, cfg.WS.Root(), *s.conv)
	if err != nil {
		return nil, err
	}
	s.conv, s.cuts, s.summarizedTo, s.body = conv, st.cuts, st.to, st.body
	for _, ev := range history {
		v, err := ev.Decode()
		if err != nil {
			return nil, fmt.Errorf("resume: seq %d: %w", ev.Seq, err)
		}
		switch p := v.(type) {
		case proto.UserMessage:
			s.turns++
		case proto.RequestSent:
			if p.Request != 0 { // 0: the summarizer's, outside the conversation's numbering
				s.requests++
			}
		case proto.Usage:
			s.tokens += computedTokens(p) // the summarizer's too, as the live loop counts them
		case proto.ToolFinished:
			s.tokens += p.Tokens // a task's child, as the live loop counts it
		}
	}
	s.calls = callsRan(history)
	return s, nil
}

// callsRan counts the tool calls the live loop counted toward the session's budget: every call that reached a
// permission decision or started, and every done call answered ok. A call answered without either (malformed, a
// repeat of a failed call, not run for a budget, a cancel or a shutdown) did not count. Events name their call by its
// tool.call's seq, as their cause; one without a cause names it by id, and is matched to that id's earliest call
// not yet finished: calls run in the order they were logged, and a response may repeat an id.
func callsRan(history []proto.Event) int {
	name := map[int64]string{}
	open := map[string][]int64{} // by id, the calls not yet finished, in log order
	ran := map[int64]bool{}
	call := func(ev proto.Event, id string) int64 {
		if ev.Cause != 0 {
			return ev.Cause
		}
		if q := open[id]; len(q) > 0 {
			return q[0]
		}
		return 0
	}
	for _, ev := range history {
		v, err := ev.Decode()
		if err != nil {
			continue
		}
		switch p := v.(type) {
		case proto.ToolCall:
			name[ev.Seq] = p.Name
			open[p.ID] = append(open[p.ID], ev.Seq)
		case proto.PermissionRequested:
			ran[call(ev, p.ID)] = true
		case proto.PermissionResolved:
			ran[call(ev, p.ID)] = true
		case proto.ToolStarted:
			ran[call(ev, p.ID)] = true
		case proto.ToolFinished:
			seq := call(ev, p.ID)
			if name[seq] == "done" && p.Outcome == proto.OutcomeOK {
				ran[seq] = true
			}
			open[p.ID] = slices.DeleteFunc(open[p.ID], func(s int64) bool { return s == seq })
		}
	}
	n := 0
	for seq := range name {
		if ran[seq] {
			n++
		}
	}
	return n
}

// gapless says that no event that made a message falls after last, the last seq a summary covers, and before at, the
// assistant.message it keeps from: the summary covers every message it removes.
func gapless(made []int64, last, at int64) error {
	for _, seq := range made {
		if seq > last && seq < at {
			return fmt.Errorf("a summary covering up to seq %d removes the message from seq %d, which it does not cover", last, seq)
		}
	}
	return nil
}
