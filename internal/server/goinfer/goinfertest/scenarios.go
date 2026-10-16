// Package goinfertest holds the scripted exchanges the goinfer client's cassettes record (tools/cassettes) and its
// tests replay. Recorder and tests share them so they send the same requests: a replayed cassette only matches when
// the client still produces exactly what was recorded.
package goinfertest

import (
	"context"
	"encoding/json"

	"github.com/townsendmerino/tack/internal/server"
)

// Tools is the tool set the client cassettes use: two tools, so goinfer constrains calls with its union grammar. The
// arguments are enums so the tiny model's constrained call closes in a few tokens; free strings run to max_tokens.
var Tools = []server.Tool{
	{Name: "grep", Description: "Search file contents with a regular expression.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"pattern":{"type":"string","enum":["func main"]}},"required":["pattern"]}`)},
	{Name: "read", Description: "Read a file.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","enum":["cmd/tack/main.go"]}},"required":["path"]}`)},
}

// GrepResult is the tool result the tool scenario answers every call with.
const GrepResult = "cmd/tack/main.go:10:func main() {"

var zero = 0.0

// Text sends one plain request.
func Text(ctx context.Context, s server.Server, model string, onDelta func(server.Delta)) (*server.Response, error) {
	conv := &server.Conversation{Model: model, System: "You are terse.", MaxTokens: 16}
	_ = conv.BeginTurn("Reply with exactly: hello world", false)
	req := conv.Request()
	req.Temperature = &zero
	return s.Stream(ctx, req, onDelta)
}

// ToolConversation is the tool scenario's conversation before its first request.
func ToolConversation(model string) *server.Conversation {
	conv := &server.Conversation{Model: model, System: "You answer questions about a Go repository. Use the tools.",
		Tools: Tools, MaxTokens: 1024}
	_ = conv.BeginTurn("Use grep to find where func main is defined, then tell me the file.", false)
	return conv
}

// ToolLoop runs the tool scenario: the first request must call a tool; every call is answered with GrepResult; it
// stops when a response calls nothing, or after three requests. It returns every response.
func ToolLoop(ctx context.Context, s server.Server, model string) ([]*server.Response, error) {
	conv := ToolConversation(model)
	var out []*server.Response
	for range 3 {
		req := conv.Request()
		req.Temperature = &zero
		if len(conv.Messages()) == 1 {
			req.ToolChoice = "any" // the tiny model only calls a tool when it must
		} else {
			req.MaxTokens = 32 // the reply after the tool result is noise; keep the cassette small
		}
		resp, err := s.Stream(ctx, req, nil)
		if err != nil {
			return out, err
		}
		out = append(out, resp)
		if err := conv.AddResponse(resp.Content); err != nil {
			return out, err
		}
		ids := conv.Pending()
		if len(ids) == 0 {
			return out, nil
		}
		var rs []server.ToolResult
		for _, id := range ids {
			rs = append(rs, server.ToolResult{ID: id, Result: GrepResult})
		}
		if err := conv.AnswerTools(rs); err != nil {
			return out, err
		}
	}
	return out, nil
}
