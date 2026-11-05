package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/townsendmerino/tack/internal/proto"
)

// Server is a model server: goinfer over its Messages API now, OpenAI-compatible servers later (DESIGN §6.4).
type Server interface {
	// Name identifies the server in events, as name@place ("goinfer@nobara").
	Name() string
	// Stream sends req, calls onDelta for streamed text as it arrives, and returns the finished response. req must
	// pass Validate; Stream refuses one that does not, without sending it.
	Stream(ctx context.Context, req Request, onDelta func(Delta)) (*Response, error)
	// CountTokens counts req's prompt with the model's own tokenizer.
	CountTokens(ctx context.Context, req Request) (int, error)
	// Models lists the served models.
	Models(ctx context.Context) ([]ModelInfo, error)
}

// Request is a server-neutral request. System and Tools are the stable prefix (DESIGN §7.2); Messages grow at the tail.
type Request struct {
	Model     string
	System    string
	Tools     []Tool
	Messages  []Message
	MaxTokens int
	// Thinking asks for the model's thinking. It must not change within a turn (Conversation enforces it).
	Thinking bool
	// Temperature, TopP and TopK, when set, override the server's default sampling.
	Temperature *float64
	TopP        *float64
	TopK        *int
	// ToolChoice is "" (the server's default, auto), "any" (the model must call a tool) or "none".
	ToolChoice string
}

// Tool is a tool's name, description and JSON Schema for its input.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// Role is a message's author.
type Role string

const (
	User      Role = "user"
	Assistant Role = "assistant"
)

// Message is one message of the conversation.
type Message struct {
	Role    Role
	Content []Block
}

// Block is one block of a message. Assistant messages carry the model's blocks (proto.ModelBlock: text, thinking,
// tool_use) exactly as received; user messages carry text and tool results.
type Block struct {
	proto.ModelBlock
	// ToolUseID, Result and IsError are set for a tool_result block (Type "tool_result").
	ToolUseID string
	Result    string
	IsError   bool
}

// TextBlock makes a text block.
func TextBlock(s string) Block { return Block{Type: "text", Text: s} }

// ToolResultBlock makes a tool_result block.
func ToolResultBlock(id, result string, isError bool) Block {
	return Block{Type: "tool_result", ToolUseID: id, Result: result, IsError: isError}
}

// Delta is streamed text for the open block.
type Delta struct {
	Index int
	Kind  string // "text" or "thinking"
	Text  string
}

// Response is the finished response.
type Response struct {
	Content    []proto.ModelBlock
	StopReason string
	Usage      Usage
	// Malformed maps a tool_use id to why the call is unusable: input that is not a JSON object, or a tool the request
	// did not offer. The loop answers each with an error result and counts it (DESIGN §6.3).
	Malformed map[string]string
}

// Usage is what the server reported about a request. Fields the server does not report are nil.
type Usage struct {
	// InputTokens is the prompt tokens this request prefilled when Reused is set; from a server that does not report
	// reuse it is the whole prompt. The whole prompt is InputTokens plus *Reused.
	InputTokens  int
	OutputTokens int
	Reused       *int // prompt tokens served from the cached prefix
	Wall         time.Duration
}

// ModelInfo is one served model.
type ModelInfo struct {
	ID            string
	ContextWindow int    // 0 when the server does not say
	FastModel     string // the thinking-off alias, when the server has one
}

// Error is a server's error response.
type Error struct {
	Status     int
	Type       string
	Message    string
	RetryAfter time.Duration
}

func (e *Error) Error() string { return fmt.Sprintf("%d %s: %s", e.Status, e.Type, e.Message) }

// NoToolCall reports whether the server refused a reply because a forced tool call (tool_choice any) came back
// without one. goinfer answers so only for a model whose calls it checks after generation (forced_tool_call
// "post-check" in /v1/models); under "grammar" a reply without a call cannot be generated. The text is goinfer's
// errNoForcedCall (goinfer internal/serveapp/tool_turn.go), sent as a stream error event.
func (e *Error) NoToolCall() bool {
	return strings.Contains(e.Message, "tool_choice required a tool call and the model answered without one")
}

// Retryable reports whether the request can be sent again after RetryAfter: the server's queue was full (goinfer
// answers 529 on /v1/messages, 429 elsewhere, 503 when its in-flight cap is reached).
func (e *Error) Retryable() bool {
	switch e.Status {
	case 429, 529:
		return true
	case 503:
		return e.Type != "halted"
	}
	return false
}
