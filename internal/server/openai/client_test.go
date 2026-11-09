package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server"
)

// sse is a stream of chunks, as the API sends them, then [DONE].
func sse(chunks ...string) string {
	var b strings.Builder
	for _, c := range chunks {
		b.WriteString("data: " + c + "\n\n")
	}
	b.WriteString("data: [DONE]\n\n")
	return b.String()
}

// Hand-written wire fixtures in the shape OpenAI documents for chat.completion.chunk.
var (
	textStream = sse(
		`{"choices":[{"index":0,"delta":{"role":"assistant","content":"It is "},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{"content":"in a.go."},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":1200,"completion_tokens":6,"prompt_tokens_details":{"cached_tokens":1000}}}`)
	toolStream = sse(
		`{"choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"read","arguments":""}}]},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\":"}}]},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"a.go\"}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":900,"completion_tokens":12}}`)
)

// fake serves streams in order, and records each request's body and headers.
type fake struct {
	mu      sync.Mutex
	streams []string
	status  int
	header  http.Header
	bodies  []map[string]any
	auth    []string
	paths   []string
}

func (f *fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paths = append(f.paths, r.URL.Path)
	f.auth = append(f.auth, r.Header.Get("Authorization"))
	if r.URL.Path == "/v1/models" {
		io.WriteString(w, `{"object":"list","data":[{"id":"qwen","object":"model","context_window":16384,"fast_model":"qwen-fast"}]}`)
		return
	}
	var body map[string]any
	json.NewDecoder(r.Body).Decode(&body)
	f.bodies = append(f.bodies, body)
	for k, v := range f.header {
		w.Header()[k] = v
	}
	if f.status != 0 {
		w.WriteHeader(f.status)
		io.WriteString(w, `{"error":{"message":"slow down","type":"rate_limit"}}`)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	io.WriteString(w, f.streams[0])
	f.streams = f.streams[1:]
}

func client(t *testing.T, f *fake) *Client {
	ts := httptest.NewServer(f)
	t.Cleanup(ts.Close)
	return New("openai@test", ts.URL, "k3y", ts.Client())
}

func readTool() server.Tool {
	return server.Tool{Name: "read", Description: "Read a file.", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`)}
}

// TestTheRequestIsChatCompletions: the system prompt, a tool call and its result, a nudge and the tools go as the API
// takes them; a thinking block is not sent; tool_choice any is required; the key is a Bearer token.
func TestTheRequestIsChatCompletions(t *testing.T) {
	f := &fake{streams: []string{textStream}}
	c := client(t, f)
	temp := 0.7
	req := server.Request{Model: "qwen", System: "You are tack.", MaxTokens: 256, Temperature: &temp, ToolChoice: "any",
		Tools: []server.Tool{readTool()},
		Messages: []server.Message{
			{Role: server.User, Content: []server.Block{server.TextBlock("Where is A?")}},
			{Role: server.Assistant, Content: []server.Block{{ModelBlock: proto.ModelBlock{Type: "thinking", Thinking: "hm"}},
				{ModelBlock: proto.ModelBlock{Type: "tool_use", ID: "call_1", Name: "read", Input: json.RawMessage(`{"path":"a.go"}`)}}}},
			{Role: server.User, Content: []server.Block{server.ToolResultBlock("call_1", "package a", false), server.TextBlock("Answer now.")}},
		}}
	if _, err := c.Stream(context.Background(), req, func(server.Delta) {}); err != nil {
		t.Fatal(err)
	}
	b := f.bodies[0]
	got, _ := json.Marshal(b["messages"])
	want := `[{"content":"You are tack.","role":"system"},{"content":"Where is A?","role":"user"},` +
		`{"content":null,"role":"assistant","tool_calls":[{"function":{"arguments":"{\"path\":\"a.go\"}","name":"read"},"id":"call_1","type":"function"}]},` +
		`{"content":"package a","role":"tool","tool_call_id":"call_1"},{"content":"Answer now.","role":"user"}]`
	if string(got) != want {
		t.Errorf("messages:\n got %s\nwant %s", got, want)
	}
	tools, _ := json.Marshal(b["tools"])
	if f.paths[0] != "/v1/chat/completions" || b["tool_choice"] != "required" || b["stream"] != true || b["max_tokens"] != 256.0 ||
		b["temperature"] != 0.7 || !strings.Contains(string(tools), `"type":"function"`) || f.auth[0] != "Bearer k3y" {
		t.Errorf("request %v (tools %s), path %s, auth %q", b, tools, f.paths[0], f.auth[0])
	}
	if so, _ := json.Marshal(b["stream_options"]); string(so) != `{"include_usage":true}` {
		t.Errorf("stream_options %s", so)
	}
}

// TestAStreamBecomesAResponse: text deltas, a tool call whose arguments come in pieces, the finish reason and the usage,
// with cached tokens as reuse.
func TestAStreamBecomesAResponse(t *testing.T) {
	f := &fake{streams: []string{textStream, toolStream}}
	c := client(t, f)
	var deltas []string
	r, err := c.Stream(context.Background(), server.Request{Model: "qwen", MaxTokens: 64, Tools: []server.Tool{readTool()},
		Messages: []server.Message{{Role: server.User, Content: []server.Block{server.TextBlock("q")}}}},
		func(d server.Delta) { deltas = append(deltas, d.Kind+":"+d.Text) })
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Content) != 1 || r.Content[0].Text != "It is in a.go." || r.StopReason != "end_turn" ||
		strings.Join(deltas, "|") != "text:It is |text:in a.go." {
		t.Errorf("text response %+v, deltas %q", r, deltas)
	}
	if r.Usage.Reused == nil || *r.Usage.Reused != 1000 || r.Usage.InputTokens != 200 || r.Usage.OutputTokens != 6 {
		t.Errorf("usage %+v: want 200 prefilled of 1,200, 1,000 reused", r.Usage)
	}
	r, err = c.Stream(context.Background(), server.Request{Model: "qwen", MaxTokens: 64, Tools: []server.Tool{readTool()},
		Messages: []server.Message{{Role: server.User, Content: []server.Block{server.TextBlock("q")}}}}, func(server.Delta) {})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Content) != 1 || r.Content[0].Type != "tool_use" || r.Content[0].ID != "call_1" || r.Content[0].Name != "read" ||
		string(r.Content[0].Input) != `{"path":"a.go"}` || r.StopReason != "tool_use" || len(r.Malformed) != 0 ||
		r.Usage.Reused != nil || r.Usage.InputTokens != 900 {
		t.Errorf("tool response %+v", r)
	}
}

func TestReasoningIsThinking(t *testing.T) {
	f := &fake{streams: []string{sse(
		`{"choices":[{"index":0,"delta":{"reasoning_content":"look in a.go"},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{"content":"a.go"},"finish_reason":"stop"}]}`)}}
	var kinds []string
	r, err := client(t, f).Stream(context.Background(), server.Request{Model: "qwen", MaxTokens: 8,
		Messages: []server.Message{{Role: server.User, Content: []server.Block{server.TextBlock("q")}}}},
		func(d server.Delta) { kinds = append(kinds, d.Kind) })
	if err != nil || len(r.Content) != 2 || r.Content[0].Type != "thinking" || r.Content[0].Thinking != "look in a.go" ||
		strings.Join(kinds, ",") != "thinking,text" {
		t.Errorf("%v: %+v, %q", err, r, kinds)
	}
}

// TestABadStreamOrCallIsCaught: a stream with no finish reason, a chunk that is not JSON, an unknown tool and
// arguments that are not an object.
func TestABadStreamOrCallIsCaught(t *testing.T) {
	q := server.Request{Model: "qwen", MaxTokens: 8, Tools: []server.Tool{readTool()},
		Messages: []server.Message{{Role: server.User, Content: []server.Block{server.TextBlock("q")}}}}
	for name, s := range map[string]string{
		"no finish": sse(`{"choices":[{"index":0,"delta":{"content":"a"},"finish_reason":null}]}`),
		"not JSON":  "data: {oops\n\n",
	} {
		if _, err := client(t, &fake{streams: []string{s}}).Stream(context.Background(), q, func(server.Delta) {}); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	bad := sse(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"c1","function":{"name":"rm","arguments":"{}"}},` +
		`{"index":1,"id":"c2","function":{"name":"read","arguments":"[1]"}}]},"finish_reason":"tool_calls"}]}`)
	r, err := client(t, &fake{streams: []string{bad}}).Stream(context.Background(), q, func(server.Delta) {})
	if err != nil || r.Malformed["c1"] == "" || r.Malformed["c2"] == "" || r.Content[0].ID != "c1" || r.Content[1].ID != "c2" {
		t.Errorf("%v: %+v", err, r)
	}
}

func TestAnErrorIsAServerError(t *testing.T) {
	f := &fake{status: 429, header: http.Header{"Retry-After": {"7"}}}
	_, err := client(t, f).Stream(context.Background(), server.Request{Model: "qwen", MaxTokens: 8,
		Messages: []server.Message{{Role: server.User, Content: []server.Block{server.TextBlock("q")}}}}, func(server.Delta) {})
	var se *server.Error
	if !errors.As(err, &se) || se.Status != 429 || se.Type != "rate_limit" || se.Message != "slow down" || se.RetryAfter.Seconds() != 7 {
		t.Errorf("error %#v", err)
	}
}

func TestModelsAndNoCount(t *testing.T) {
	c := client(t, &fake{})
	ms, err := c.Models(context.Background())
	if err != nil || len(ms) != 1 || ms[0].ID != "qwen" || ms[0].ContextWindow != 16384 || ms[0].FastModel != "qwen-fast" {
		t.Errorf("models %+v, %v", ms, err)
	}
	if _, err := c.CountTokens(context.Background(), server.Request{}); !errors.Is(err, ErrNoCount) {
		t.Errorf("CountTokens: %v", err)
	}
}

func simple() server.Request {
	return server.Request{Model: "qwen", MaxTokens: 8, Tools: []server.Tool{readTool()},
		Messages: []server.Message{{Role: server.User, Content: []server.Block{server.TextBlock("q")}}}}
}

// TestAnErrorInTheStreamIsAServerError: an error event mid-stream is the server's error, not a stream with no end.
func TestAnErrorInTheStreamIsAServerError(t *testing.T) {
	s := "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"a\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"error\":{\"message\":\"the model crashed\",\"type\":\"server_error\"}}\n\n"
	_, err := client(t, &fake{streams: []string{s}}).Stream(context.Background(), simple(), func(server.Delta) {})
	var se *server.Error
	if !errors.As(err, &se) || se.Type != "server_error" || se.Message != "the model crashed" {
		t.Errorf("error %#v", err)
	}
}

// TestAnEventCanSpanLines: an event's data: lines join with newlines (the SSE spec), lines may end in CRLF, and the last
// event needs no blank line after it.
func TestAnEventCanSpanLines(t *testing.T) {
	s := "data: {\"choices\":[{\"index\":0,\r\ndata: \"delta\":{\"content\":\"hi\"},\"finish_reason\":\"stop\"}]}\r\n\r\n: a comment\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":1}}"
	r, err := client(t, &fake{streams: []string{s}}).Stream(context.Background(), simple(), func(server.Delta) {})
	if err != nil || len(r.Content) != 1 || r.Content[0].Text != "hi" || r.Usage.InputTokens != 5 {
		t.Errorf("%v: %+v", err, r)
	}
}

// TestAFailedToolResultSaysSo: the API has no error flag on a tool message, so a failed result says it in its text.
func TestAFailedToolResultSaysSo(t *testing.T) {
	f := &fake{streams: []string{textStream}}
	req := simple()
	req.Messages = append(req.Messages,
		server.Message{Role: server.Assistant, Content: []server.Block{{ModelBlock: proto.ModelBlock{Type: "tool_use", ID: "c1", Name: "read", Input: json.RawMessage(`{}`)}}}},
		server.Message{Role: server.User, Content: []server.Block{server.ToolResultBlock("c1", "no such file", true)}})
	if _, err := client(t, f).Stream(context.Background(), req, func(server.Delta) {}); err != nil {
		t.Fatal(err)
	}
	msgs, _ := json.Marshal(f.bodies[0]["messages"])
	if !strings.Contains(string(msgs), `"content":"error: no such file","role":"tool"`) {
		t.Errorf("messages %s", msgs)
	}
}

// TestCallsWithNoIndexOrIDStayApart: chunks with no index are told apart by their ids, and a call with no id gets one.
func TestCallsWithNoIndexOrIDStayApart(t *testing.T) {
	s := sse(`{"choices":[{"index":0,"delta":{"tool_calls":[{"id":"a","function":{"name":"read","arguments":"{\"path\":"}}]},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"function":{"arguments":"\"x\"}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"id":"b","function":{"name":"read","arguments":"{\"path\":\"y\"}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":2,"function":{"name":"read","arguments":"{\"path\":\"z\"}"}}]},"finish_reason":"tool_calls"}]}`)
	r, err := client(t, &fake{streams: []string{s}}).Stream(context.Background(), simple(), func(server.Delta) {})
	if err != nil || len(r.Content) != 3 || r.Content[0].ID != "a" || string(r.Content[0].Input) != `{"path":"x"}` ||
		r.Content[1].ID != "b" || r.Content[2].ID != "call_2" || string(r.Content[2].Input) != `{"path":"z"}` {
		t.Errorf("%v: %+v", err, r.Content)
	}
}

// TestADroppedBodyIsNotAServerError: a stream cut mid-way is a plain error, as goinfer's client gives, so the loop
// treats it the same: not retried, a server failure for the turn.
func TestADroppedBodyIsNotAServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000") // promises more than it sends
		io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"a\"},\"finish_reason\":null}]}\n\n")
	}))
	t.Cleanup(ts.Close)
	_, err := New("o", ts.URL, "", ts.Client()).Stream(context.Background(), simple(), func(server.Delta) {})
	var se *server.Error
	if err == nil || errors.As(err, &se) {
		t.Errorf("error %#v; want a plain error", err)
	}
}

// TestATextDeltasIndexIsItsBlock: a text delta's index is its block's in the response: 0 alone, 1 after thinking.
func TestATextDeltasIndexIsItsBlock(t *testing.T) {
	for name, c := range map[string]struct {
		stream string
		want   int
	}{
		"alone":          {sse(`{"choices":[{"index":0,"delta":{"content":"a"},"finish_reason":"stop"}]}`), 0},
		"after thinking": {sse(`{"choices":[{"index":0,"delta":{"reasoning_content":"r"},"finish_reason":null}]}`, `{"choices":[{"index":0,"delta":{"content":"a"},"finish_reason":"stop"}]}`), 1},
	} {
		got := -1
		r, err := client(t, &fake{streams: []string{c.stream}}).Stream(context.Background(), simple(), func(d server.Delta) {
			if d.Kind == "text" {
				got = d.Index
			}
		})
		if err != nil || got != c.want || r.Content[got].Type != "text" {
			t.Errorf("%s: text delta index %d, want %d (%v, %+v)", name, got, c.want, err, r)
		}
	}
}
