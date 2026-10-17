package goinfertest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/townsendmerino/tack/internal/proto"
)

// SSE builds a /v1/messages stream in goinfer's framing (internal/serveapp/anthropic_stream.go, as recorded in
// internal/server/goinfer/testdata/tools.json): one block per ModelBlock, a tool call's input in one
// input_json_delta, then message_delta with stop and message_stop. message_delta's usage is output_tokens only, the
// pre-G3 shape (docs/briefs/goinfer-g3-messages-cached-tokens.md) most cassettes were recorded against; use
// SSEWithReuse for a stream reporting goinfer's G3 prefilled/reused split.
func SSE(stop string, blocks ...proto.ModelBlock) string {
	return sse(stop, map[string]any{"output_tokens": 20}, blocks...)
}

// SSEWithReuse is SSE, but message_delta's usage carries goinfer's G3 split
// (docs/briefs/goinfer-g3-messages-cached-tokens.md): input is the tokens this request prefilled, reused the leading
// ones a prior turn's KV held, so input+reused is the whole prompt.
func SSEWithReuse(stop string, input, reused, output int, blocks ...proto.ModelBlock) string {
	return sse(stop, map[string]any{"input_tokens": input, "cache_read_input_tokens": reused, "output_tokens": output}, blocks...)
}

func sse(stop string, deltaUsage map[string]any, blocks ...proto.ModelBlock) string {
	var b strings.Builder
	ev := func(name string, v any) {
		j, _ := json.Marshal(v)
		fmt.Fprintf(&b, "event: %s\ndata: %s\n\n", name, j)
	}
	ev("message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": "msg_test", "type": "message",
		"role": "assistant", "model": "scripted", "content": []any{}, "stop_reason": nil, "stop_sequence": nil,
		"usage": map[string]any{"input_tokens": 100, "output_tokens": 0}}})
	ev("ping", map[string]any{"type": "ping"})
	for i, blk := range blocks {
		switch blk.Type {
		case "text":
			ev("content_block_start", map[string]any{"type": "content_block_start", "index": i, "content_block": map[string]any{"type": "text", "text": ""}})
			ev("content_block_delta", map[string]any{"type": "content_block_delta", "index": i, "delta": map[string]any{"type": "text_delta", "text": blk.Text}})
		case "thinking":
			ev("content_block_start", map[string]any{"type": "content_block_start", "index": i, "content_block": map[string]any{"type": "thinking", "thinking": "", "signature": ""}})
			ev("content_block_delta", map[string]any{"type": "content_block_delta", "index": i, "delta": map[string]any{"type": "thinking_delta", "thinking": blk.Thinking}})
			ev("content_block_delta", map[string]any{"type": "content_block_delta", "index": i, "delta": map[string]any{"type": "signature_delta", "signature": blk.Signature}})
		case "tool_use":
			ev("content_block_start", map[string]any{"type": "content_block_start", "index": i, "content_block": map[string]any{"type": "tool_use", "id": blk.ID, "name": blk.Name, "input": map[string]any{}}})
			ev("content_block_delta", map[string]any{"type": "content_block_delta", "index": i, "delta": map[string]any{"type": "input_json_delta", "partial_json": string(blk.Input)}})
		}
		ev("content_block_stop", map[string]any{"type": "content_block_stop", "index": i})
	}
	ev("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stop, "stop_sequence": nil}, "usage": deltaUsage})
	ev("message_stop", map[string]any{"type": "message_stop"})
	return b.String()
}

// Reply is one scripted answer to a /v1/messages request: an SSE body, or an error status with a JSON body.
type Reply struct {
	Status     int // 0 means 200
	RetryAfter string
	Body       string
}

// Says is a reply that says s and ends the turn.
func Says(s string) Reply {
	return Reply{Body: SSE("end_turn", proto.ModelBlock{Type: "text", Text: s})}
}

// Calls is a reply with one text block (may be empty) and tool calls, given as id, name, input triples.
func Calls(text string, calls ...string) Reply {
	var bs []proto.ModelBlock
	if text != "" {
		bs = append(bs, proto.ModelBlock{Type: "text", Text: text})
	}
	for i := 0; i+2 < len(calls); i += 3 {
		bs = append(bs, proto.ModelBlock{Type: "tool_use", ID: calls[i], Name: calls[i+1], Input: json.RawMessage(calls[i+2])})
	}
	return Reply{Body: SSE("tool_use", bs...)}
}

// Scripted is a stand-in goinfer-serve: /v1/models serves one model with a window, /v1/messages answers with the next
// scripted reply, /v1/messages/count_tokens answers Tokens. It stands in for a model when a test needs a
// conversation a real model will not reliably produce; the recorded cassettes keep its wire format honest.
type Scripted struct {
	Model  string
	Window int
	Tokens int

	mu      sync.Mutex
	replies []Reply
	// Requests holds every /v1/messages body received, in order.
	Requests []json.RawMessage
}

// NewScripted returns a scripted server that will give replies in order.
func NewScripted(model string, window int, replies ...Reply) *Scripted {
	return &Scripted{Model: model, Window: window, replies: replies}
}

// ServeHTTP implements http.Handler.
func (s *Scripted) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch r.URL.Path {
	case "/v1/models":
		json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": []any{map[string]any{
			"id": s.Model, "object": "model", "context_window": s.Window, "fast_model": s.Model + "-fast"}}})
	case "/v1/messages/count_tokens":
		json.NewEncoder(w).Encode(map[string]any{"input_tokens": s.Tokens})
	case "/v1/messages":
		var body json.RawMessage
		_ = json.NewDecoder(r.Body).Decode(&body)
		s.Requests = append(s.Requests, body)
		if len(s.replies) == 0 {
			w.WriteHeader(500)
			fmt.Fprint(w, `{"type":"error","error":{"type":"api_error","message":"script exhausted"}}`)
			return
		}
		rep := s.replies[0]
		s.replies = s.replies[1:]
		if rep.Status != 0 && rep.Status != 200 {
			if rep.RetryAfter != "" {
				w.Header().Set("Retry-After", rep.RetryAfter)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(rep.Status)
			fmt.Fprint(w, rep.Body)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, rep.Body)
	default:
		w.WriteHeader(404)
	}
}

// Left is the number of replies not yet given.
func (s *Scripted) Left() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.replies)
}
