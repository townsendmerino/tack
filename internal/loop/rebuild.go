package loop

import (
	"fmt"

	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server"
)

// Rebuild replays a session's persisted events into the conversation they describe (principle 4: the log is the
// truth). A rebuilt conversation's next request is the one the live loop would send, byte for byte, so a resumed
// session keeps its cached prefix. A tool call that started and never finished (a crash) is answered with an error
// result saying its outcome is unknown; it is never re-run (DESIGN §6.1). model and tools are the session's.
func Rebuild(evs []proto.Event, workspaceRoot, model string, tools []server.Tool, maxTokens int) (*server.Conversation, error) {
	conv := &server.Conversation{Model: model, System: SystemPrompt, Tools: tools, MaxTokens: maxTokens}
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
			text := ""
			for _, b := range p.Content {
				text += b.Text
			}
			if err := conv.BeginTurn(UserText(turns == 0, workspaceRoot, text), false); err != nil {
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
