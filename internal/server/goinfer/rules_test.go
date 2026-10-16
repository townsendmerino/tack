package goinfer_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server"
	"github.com/townsendmerino/tack/internal/server/cassette"
	"github.com/townsendmerino/tack/internal/server/goinfer"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
)

// The message-structure rules of DESIGN §6.3, one cassette test each. Each cassette is a turn with a tool call: the
// first interaction is the model's response, the second is the follow-up request the rule constrains, written out
// by hand as the rule says it must look. Replay is strict, so the test passes only if the client sends exactly that.

var tools = []server.Tool{
	{Name: "read", Description: "Read a file.", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)},
	{Name: "grep", Description: "Search.", InputSchema: json.RawMessage(`{"type":"object","properties":{"pattern":{"type":"string"}},"required":["pattern"]}`)},
}

func toolUse(id, name, input string) proto.ModelBlock {
	return proto.ModelBlock{Type: "tool_use", ID: id, Name: name, Input: json.RawMessage(input)}
}

// wireReq is the body the client sends, written as the rule requires it.
func wireReq(thinking string, messages ...any) cassette.Req {
	body := map[string]any{"model": "m", "max_tokens": 64, "system": "sys", "messages": messages,
		"tools": tools, "thinking": map[string]any{"type": thinking}, "stream": true}
	b, _ := json.Marshal(body)
	return cassette.Req{Method: "POST", Path: "/v1/messages", Body: b}
}

func msg(role string, blocks ...map[string]any) map[string]any {
	return map[string]any{"role": role, "content": blocks}
}

func text(s string) map[string]any { return map[string]any{"type": "text", "text": s} }

func use(id, name string, input map[string]any) map[string]any {
	return map[string]any{"type": "tool_use", "id": id, "name": name, "input": input}
}

func result(id, content string, isError bool) map[string]any {
	m := map[string]any{"type": "tool_result", "tool_use_id": id, "content": content}
	if isError {
		m["is_error"] = true
	}
	return m
}

func newConv() *server.Conversation {
	return &server.Conversation{Model: "m", System: "sys", Tools: tools, MaxTokens: 64}
}

// Rule: every tool call gets a result in the next message, including when it was not run (a denial, cancel, halt,
// budget trip or outcome unknown), where the result is an error saying what happened.
func TestRuleEveryToolCallGetsAResult(t *testing.T) {
	q := msg("user", text("look"))
	a := msg("assistant", use("t1", "read", map[string]any{"path": "a.go"}), use("t2", "grep", map[string]any{"pattern": "x"}))
	c, r := synthetic(
		cassette.Interaction{Request: wireReq("disabled", q), Response: sseResp(goinfertest.SSE("tool_use",
			toolUse("t1", "read", `{"path":"a.go"}`), toolUse("t2", "grep", `{"pattern":"x"}`)))},
		cassette.Interaction{Request: wireReq("disabled", q, a, msg("user",
			result("t1", "package a", false),
			result("t2", "error: not run: the user cancelled the turn", true))),
			Response: sseResp(goinfertest.SSE("end_turn", proto.ModelBlock{Type: "text", Text: "ok"}))},
	)
	conv := newConv()
	_ = conv.BeginTurn("look", false)
	resp, err := c.Stream(context.Background(), conv.Request(), nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = conv.AddResponse(resp.Content)
	if err := conv.EndTurn(); err == nil {
		t.Fatal("the turn ended with tool calls unanswered")
	}
	if err := conv.AnswerTools([]server.ToolResult{{ID: "t1", Result: "package a"}}); err == nil {
		t.Fatal("AnswerTools accepted a message that answers one of two calls")
	}
	// t1 ran; then the user cancelled, so t2 is answered with an error result.
	if err := conv.AnswerTools([]server.ToolResult{{ID: "t1", Result: "package a"},
		{ID: "t2", Result: "error: not run: the user cancelled the turn", IsError: true}}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Stream(context.Background(), conv.Request(), nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Done(); err != nil {
		t.Fatal(err)
	}

	// The same rule at the client's chokepoint: a request with an unanswered call is refused and never sent.
	bad := conv.Request()
	bad.Messages = bad.Messages[:2]
	bad.Messages = append(bad.Messages, server.Message{Role: server.User, Content: []server.Block{server.ToolResultBlock("t1", "x", false)}})
	sent := 0
	counting := goinfer.New("g", "http://goinfer.test", "", &http.Client{Transport: roundTripFunc(func() string { sent++; return "" })})
	if _, err := counting.Stream(context.Background(), bad, nil); err == nil || sent != 0 {
		t.Fatalf("an unanswered tool call was sent (err %v, sent %d)", err, sent)
	}
}

// Rule: tool results come first in that message; steering text and context updates come after them.
func TestRuleToolResultsFirst(t *testing.T) {
	q := msg("user", text("look"))
	a := msg("assistant", use("t1", "read", map[string]any{"path": "a.go"}))
	c, r := synthetic(
		cassette.Interaction{Request: wireReq("disabled", q), Response: sseResp(goinfertest.SSE("tool_use", toolUse("t1", "read", `{"path":"a.go"}`)))},
		cassette.Interaction{Request: wireReq("disabled", q, a, msg("user", result("t1", "package a", false), text("also check b.go"))),
			Response: sseResp(goinfertest.SSE("end_turn", proto.ModelBlock{Type: "text", Text: "ok"}))},
	)
	conv := newConv()
	_ = conv.BeginTurn("look", false)
	resp, _ := c.Stream(context.Background(), conv.Request(), nil)
	_ = conv.AddResponse(resp.Content)
	// The user steered while the tool ran; the steering text goes after the result.
	if err := conv.AnswerTools([]server.ToolResult{{ID: "t1", Result: "package a"}}, "also check b.go"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Stream(context.Background(), conv.Request(), nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Done(); err != nil {
		t.Fatal(err)
	}

	// Text before a result breaks the rule and is refused before sending.
	bad := conv.Request()
	last := &bad.Messages[2]
	last.Content = []server.Block{server.TextBlock("also check b.go"), server.ToolResultBlock("t1", "package a", false)}
	if err := server.Validate(bad); err == nil || !strings.Contains(err.Error(), "first") {
		t.Fatalf("Validate = %v, want the results-first rule", err)
	}
}

// Rule: thinking content goes back as received — text and signature byte for byte.
func TestRuleThinkingGoesBackAsReceived(t *testing.T) {
	const thought = "The user wants a.go.\n  Read it first; then  answer."
	const sig = "c2lnbmF0dXJlLWJ5dGVz=="
	q := msg("user", text("look"))
	a := msg("assistant", map[string]any{"type": "thinking", "thinking": thought, "signature": sig},
		use("t1", "read", map[string]any{"path": "a.go"}))
	c, r := synthetic(
		cassette.Interaction{Request: wireReq("enabled", q), Response: sseResp(goinfertest.SSE("tool_use",
			proto.ModelBlock{Type: "thinking", Thinking: thought, Signature: sig}, toolUse("t1", "read", `{"path":"a.go"}`)))},
		cassette.Interaction{Request: wireReq("enabled", q, a, msg("user", result("t1", "package a", false))),
			Response: sseResp(goinfertest.SSE("end_turn", proto.ModelBlock{Type: "text", Text: "ok"}))},
	)
	conv := newConv()
	_ = conv.BeginTurn("look", true)
	resp, err := c.Stream(context.Background(), conv.Request(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content[0].Thinking != thought || resp.Content[0].Signature != sig {
		t.Fatalf("thinking parsed as %+v", resp.Content[0])
	}
	_ = conv.AddResponse(resp.Content)
	_ = conv.AnswerTools([]server.ToolResult{{ID: "t1", Result: "package a"}})
	if _, err := c.Stream(context.Background(), conv.Request(), nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Done(); err != nil {
		t.Fatal(err)
	}
}

// Rule: thinking stays on or off for a whole turn (switching it changes the rendered prompt and costs the cached
// prefix). Both requests of a turn carry the setting the turn began with; the next turn may change it.
func TestRuleThinkingFixedForTheTurn(t *testing.T) {
	q := msg("user", text("look"))
	a := msg("assistant", use("t1", "read", map[string]any{"path": "a.go"}))
	done := msg("assistant", text("ok"))
	answer := msg("user", result("t1", "package a", false))
	c, r := synthetic(
		cassette.Interaction{Request: wireReq("enabled", q), Response: sseResp(goinfertest.SSE("tool_use", toolUse("t1", "read", `{"path":"a.go"}`)))},
		cassette.Interaction{Request: wireReq("enabled", q, a, answer), Response: sseResp(goinfertest.SSE("end_turn", proto.ModelBlock{Type: "text", Text: "ok"}))},
		cassette.Interaction{Request: wireReq("disabled", q, a, answer, done, msg("user", text("thanks"))),
			Response: sseResp(goinfertest.SSE("end_turn", proto.ModelBlock{Type: "text", Text: "np"}))},
	)
	conv := newConv()
	_ = conv.BeginTurn("look", true)
	resp, _ := c.Stream(context.Background(), conv.Request(), nil)
	_ = conv.AddResponse(resp.Content)
	if err := conv.BeginTurn("change of plan", false); err == nil {
		t.Fatal("a new turn (and thinking setting) began inside an open turn")
	}
	_ = conv.AnswerTools([]server.ToolResult{{ID: "t1", Result: "package a"}})
	resp, _ = c.Stream(context.Background(), conv.Request(), nil)
	_ = conv.AddResponse(resp.Content)
	if err := conv.EndTurn(); err != nil {
		t.Fatal(err)
	}
	_ = conv.BeginTurn("thanks", false)
	if _, err := c.Stream(context.Background(), conv.Request(), nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Done(); err != nil {
		t.Fatal(err)
	}
}

// Rule: a malformed tool call — a tool that was not offered, or input that is not a JSON object — is caught and
// reported, so the loop answers it with an error result and counts it. (On goinfer calls are grammar-constrained,
// so this is the other servers' case; the client checks every server's output the same way.)
func TestRuleMalformedToolCallsCaught(t *testing.T) {
	q := msg("user", text("look"))
	c, r := synthetic(cassette.Interaction{Request: wireReq("disabled", q), Response: sseResp(goinfertest.SSE("tool_use",
		toolUse("t1", "rm", `{"path":"/"}`), toolUse("t2", "read", `"a.go"`), toolUse("t3", "read", `{"path":"a.go"}`)))})
	conv := newConv()
	_ = conv.BeginTurn("look", false)
	resp, err := c.Stream(context.Background(), conv.Request(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Done(); err != nil {
		t.Fatal(err)
	}
	if len(resp.Malformed) != 2 || !strings.Contains(resp.Malformed["t1"], "no tool") ||
		!strings.Contains(resp.Malformed["t2"], "not a JSON object") || resp.Malformed["t3"] != "" {
		t.Fatalf("Malformed = %v", resp.Malformed)
	}
}

// roundTripFunc answers every request with 200 and the SSE body body() returns.
type roundTripFunc func() string

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(f())), Request: req}, nil
}
