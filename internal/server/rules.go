package server

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Validate enforces the Messages API's message-structure rules on a request before it is sent (DESIGN §6.3).
// goinfer does not check them itself, so this is the only place they are checked:
//
//   - every tool_use in an assistant message is answered by exactly one tool_result in the next message, which is a
//     user message, and no tool_result answers an id that was not called;
//   - in that message the tool results come first, and text (steering, context updates) after them;
//   - the conversation starts with a user message and roles alternate;
//   - the last message is the user's, since a request asks the model to speak next.
//
// The thinking rules hold by construction: Conversation keeps assistant blocks as received and fixes Thinking for a
// turn.
func Validate(req Request) error {
	if len(req.Messages) == 0 {
		return errors.New("rules: no messages")
	}
	if req.MaxTokens <= 0 {
		return errors.New("rules: max_tokens must be positive")
	}
	for i, m := range req.Messages {
		want := User
		if i%2 == 1 {
			want = Assistant
		}
		if m.Role != want {
			return fmt.Errorf("rules: message %d is %s, want %s: the conversation starts with the user and alternates", i, m.Role, want)
		}
		if len(m.Content) == 0 {
			return fmt.Errorf("rules: message %d is empty", i)
		}
	}
	if req.Messages[len(req.Messages)-1].Role != User {
		return errors.New("rules: the last message must be the user's")
	}

	for i, m := range req.Messages {
		var called []string // tool_use ids of the previous assistant message, in order
		if i > 0 && m.Role == User {
			for _, b := range req.Messages[i-1].Content {
				if b.Type == "tool_use" {
					called = append(called, b.ID)
				}
			}
		}
		answered := map[string]bool{}
		seenText := false
		for j, b := range m.Content {
			switch b.Type {
			case "tool_result":
				if m.Role != User {
					return fmt.Errorf("rules: message %d block %d: a tool_result belongs in a user message", i, j)
				}
				if seenText {
					return fmt.Errorf("rules: message %d block %d: tool results come first, before any text", i, j)
				}
				if !contains(called, b.ToolUseID) {
					return fmt.Errorf("rules: message %d answers tool_use %q, which the previous message did not call", i, b.ToolUseID)
				}
				if answered[b.ToolUseID] {
					return fmt.Errorf("rules: message %d answers tool_use %q twice", i, b.ToolUseID)
				}
				answered[b.ToolUseID] = true
			case "tool_use":
				if m.Role != Assistant {
					return fmt.Errorf("rules: message %d block %d: tool_use belongs in an assistant message", i, j)
				}
				if b.ID == "" || b.Name == "" {
					return fmt.Errorf("rules: message %d block %d: tool_use needs an id and a name", i, j)
				}
				if !json.Valid(b.Input) {
					return fmt.Errorf("rules: message %d block %d: tool_use input is not JSON", i, j)
				}
			case "text":
				seenText = true
			case "thinking", "redacted_thinking":
				if m.Role != Assistant {
					return fmt.Errorf("rules: message %d block %d: thinking belongs in an assistant message", i, j)
				}
			default:
				return fmt.Errorf("rules: message %d block %d: unknown block type %q", i, j, b.Type)
			}
		}
		for _, id := range called {
			if !answered[id] {
				return fmt.Errorf("rules: tool_use %q (message %d) has no tool_result in message %d", id, i-1, i)
			}
		}
	}
	return nil
}

func contains(s []string, x string) bool {
	for _, v := range s {
		if v == x {
			return true
		}
	}
	return false
}
