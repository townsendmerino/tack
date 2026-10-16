package server

import (
	"errors"
	"fmt"

	"github.com/townsendmerino/tack/internal/proto"
)

// Conversation builds requests that keep the message-structure rules by construction (DESIGN §6.3), and grows only at
// its tail so the server's cached prefix stays valid (DESIGN §7.2):
//
//   - assistant blocks are kept exactly as the server returned them, thinking and signatures included;
//   - Thinking is fixed when a turn begins and cannot change until the next turn;
//   - after a response with tool calls, the next user message must answer every call, results first;
//     CancelPending answers whatever is still open with error results (denial, cancel, halt, budget, unknown).
//
// It is not safe for concurrent use.
type Conversation struct {
	Model     string
	System    string
	Tools     []Tool
	MaxTokens int

	msgs     []Message
	thinking bool
	inTurn   bool
	pending  []string // tool_use ids of the last response, awaiting results
}

// ToolResult is one answered tool call.
type ToolResult struct {
	ID      string
	Result  string
	IsError bool
}

// BeginTurn starts a turn with the user's text and fixes its thinking setting.
func (c *Conversation) BeginTurn(text string, thinking bool) error {
	if c.inTurn {
		return errors.New("conversation: a turn is already open")
	}
	if len(c.pending) > 0 {
		return fmt.Errorf("conversation: %d tool calls are unanswered", len(c.pending))
	}
	c.inTurn, c.thinking = true, thinking
	c.msgs = append(c.msgs, Message{Role: User, Content: []Block{TextBlock(text)}})
	return nil
}

// AddResponse appends the model's response as received and records its tool calls as pending.
func (c *Conversation) AddResponse(content []proto.ModelBlock) error {
	if !c.inTurn {
		return errors.New("conversation: no open turn")
	}
	if len(c.pending) > 0 || c.last().Role != User {
		return errors.New("conversation: a response must follow a user message")
	}
	m := Message{Role: Assistant}
	for _, b := range content {
		m.Content = append(m.Content, Block{ModelBlock: b})
		if b.Type == "tool_use" {
			c.pending = append(c.pending, b.ID)
		}
	}
	if len(m.Content) == 0 {
		// An empty response still takes its place in the alternation; an empty text block keeps it valid.
		m.Content = []Block{TextBlock("")}
	}
	c.msgs = append(c.msgs, m)
	return nil
}

// Pending is the tool calls awaiting results, in call order.
func (c *Conversation) Pending() []string { return append([]string(nil), c.pending...) }

// AnswerTools appends the user message that answers the pending tool calls: the results, in call order, then any
// text (steering or context updates). Every pending call must have exactly one result.
func (c *Conversation) AnswerTools(results []ToolResult, text ...string) error {
	if len(c.pending) == 0 {
		return errors.New("conversation: no tool calls are pending")
	}
	byID := map[string]ToolResult{}
	for _, r := range results {
		if _, dup := byID[r.ID]; dup {
			return fmt.Errorf("conversation: two results for %q", r.ID)
		}
		byID[r.ID] = r
	}
	m := Message{Role: User}
	for _, id := range c.pending {
		r, ok := byID[id]
		if !ok {
			return fmt.Errorf("conversation: tool call %q has no result", id)
		}
		m.Content = append(m.Content, ToolResultBlock(id, r.Result, r.IsError))
		delete(byID, id)
	}
	for id := range byID {
		return fmt.Errorf("conversation: result for %q, which is not pending", id)
	}
	for _, t := range text {
		m.Content = append(m.Content, TextBlock(t))
	}
	c.msgs = append(c.msgs, m)
	c.pending = nil
	return nil
}

// CancelPending answers every pending call with an error result saying why it was not run, and returns the ids it
// answered.
func (c *Conversation) CancelPending(why string) ([]string, error) {
	ids := c.Pending()
	if len(ids) == 0 {
		return nil, nil
	}
	rs := make([]ToolResult, len(ids))
	for i, id := range ids {
		rs[i] = ToolResult{ID: id, Result: "error: not run: " + why, IsError: true}
	}
	return ids, c.AnswerTools(rs)
}

// EndTurn closes the turn. Calls still pending are an error: they must be answered first.
func (c *Conversation) EndTurn() error {
	if len(c.pending) > 0 {
		return fmt.Errorf("conversation: %d tool calls are unanswered", len(c.pending))
	}
	c.inTurn = false
	return nil
}

// Request is the next request: the stable prefix, then every message so far.
func (c *Conversation) Request() Request {
	return Request{Model: c.Model, System: c.System, Tools: c.Tools, MaxTokens: c.MaxTokens, Thinking: c.thinking,
		Messages: append([]Message(nil), c.msgs...)}
}

// Messages is the conversation so far.
func (c *Conversation) Messages() []Message { return append([]Message(nil), c.msgs...) }

func (c *Conversation) last() Message {
	if len(c.msgs) == 0 {
		return Message{}
	}
	return c.msgs[len(c.msgs)-1]
}
