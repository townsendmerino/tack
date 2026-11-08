package loop

import (
	"errors"
	"fmt"
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
	conv := &server.Conversation{Model: base.Model, System: base.System, Tools: base.Tools, MaxTokens: base.MaxTokens,
		Sampling: base.Sampling, ToolChoice: base.ToolChoice}
	var results []server.ToolResult
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
			return nil, fmt.Errorf("rebuild: seq %d: %w", ev.Seq, err)
		}
		switch p := v.(type) {
		case proto.UserMessage:
			var text strings.Builder
			for _, b := range p.Content {
				text.WriteString(b.Text)
			}
			if err := conv.BeginTurn(UserText(turns == 0, workspaceRoot, text.String()), false); err != nil {
				return nil, fmt.Errorf("rebuild: seq %d: %w", ev.Seq, err)
			}
			turns++
		case proto.AssistantMessage:
			if err := conv.AddResponse(p.Content); err != nil {
				return nil, fmt.Errorf("rebuild: seq %d: %w", ev.Seq, err)
			}
		case proto.ToolFinished:
			results = append(results, server.ToolResult{ID: p.ID, Result: p.Result, IsError: p.Outcome != proto.OutcomeOK})
			if len(results) == len(conv.Pending()) {
				if err := flush(); err != nil {
					return nil, fmt.Errorf("rebuild: seq %d: %w", ev.Seq, err)
				}
			}
		case proto.Compaction:
			for _, m := range p.Masked {
				if !conv.ReplaceResult(m.ID, m.Stub) {
					return nil, fmt.Errorf("rebuild: seq %d: compaction masks %q, which no result answers", ev.Seq, m.ID)
				}
			}
		case proto.ErrorEvent:
			if p.Class == NoToolCallClass {
				if err := conv.Nudge(NoToolCallNudge); err != nil {
					return nil, fmt.Errorf("rebuild: seq %d: %w", ev.Seq, err)
				}
			}
		case proto.TurnEnd:
			if err := conv.EndTurn(); err != nil {
				return nil, fmt.Errorf("rebuild: seq %d: %w", ev.Seq, err)
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
			return nil, err
		}
	}
	return conv, nil
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
	conv, err := Rebuild(history, cfg.WS.Root(), *s.conv)
	if err != nil {
		return nil, err
	}
	s.conv = conv
	for _, ev := range history {
		v, err := ev.Decode()
		if err != nil {
			return nil, fmt.Errorf("resume: seq %d: %w", ev.Seq, err)
		}
		switch p := v.(type) {
		case proto.UserMessage:
			s.turns++
		case proto.RequestSent:
			s.requests++
		case proto.Usage:
			if p.Prefilled != nil {
				s.tokens += *p.Prefilled + p.Output
			} else {
				s.tokens += p.Prompt + p.Output
			}
		}
	}
	s.calls = callsRan(history)
	return s, nil
}

// callsRan counts the tool calls the live loop counted toward the session's budget: every call that reached a
// permission decision or started, and every done call answered ok. A call answered without either (malformed, a
// repeat of a failed call, not run for a budget, a cancel or a shutdown) did not count. Events name their call by its
// tool.call's seq, as their cause.
func callsRan(history []proto.Event) int {
	name := map[int64]string{}
	ran := map[int64]bool{}
	for _, ev := range history {
		switch ev.Type {
		case proto.EvToolCall:
			if v, err := ev.Decode(); err == nil {
				name[ev.Seq] = v.(proto.ToolCall).Name
			}
		case proto.EvPermissionRequested, proto.EvPermissionResolved, proto.EvToolStarted:
			ran[ev.Cause] = true
		case proto.EvToolFinished:
			if v, err := ev.Decode(); err == nil && name[ev.Cause] == "done" && v.(proto.ToolFinished).Outcome == proto.OutcomeOK {
				ran[ev.Cause] = true
			}
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
