// Package goinfer is tack's client for goinfer-serve's Anthropic-compatible API: /v1/messages (streaming, tools),
// /v1/messages/count_tokens and /v1/models (DESIGN §6.4). Shapes follow goinfer's internal/serveapp/anthropic.go,
// anthropic_stream.go and openai.go.
package goinfer

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server"
)

// Client talks to one goinfer-serve.
type Client struct {
	name    string
	baseURL string
	apiKey  string
	http    *http.Client
	now     func() time.Time
}

// New returns a client for the server at baseURL (e.g. "http://127.0.0.1:8080"). name identifies it in events
// ("goinfer@local"). apiKey may be empty when the server has none. hc nil means a client with no overall timeout:
// a generation is bounded by its context.
func New(name, baseURL, apiKey string, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{}
	}
	return &Client{name: name, baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey, http: hc, now: time.Now}
}

// Name implements server.Server.
func (c *Client) Name() string { return c.name }

// wire types

type wireBlock struct {
	Type      string          `json:"type"`
	Text      *string         `json:"text,omitempty"`
	Thinking  *string         `json:"thinking,omitempty"`
	Signature *string         `json:"signature,omitempty"`
	Data      string          `json:"data,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   *string         `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

type wireMessage struct {
	Role    string      `json:"role"`
	Content []wireBlock `json:"content"`
}

type wireThinking struct {
	Type string `json:"type"`
}

type wireChoice struct {
	Type string `json:"type"`
}

type wireRequest struct {
	Model       string        `json:"model"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	System      string        `json:"system,omitempty"`
	Messages    []wireMessage `json:"messages"`
	Tools       []server.Tool `json:"tools,omitempty"`
	Thinking    *wireThinking `json:"thinking,omitempty"`
	Temperature *float64      `json:"temperature,omitempty"`
	TopP        *float64      `json:"top_p,omitempty"`
	TopK        *int          `json:"top_k,omitempty"`
	ToolChoice  *wireChoice   `json:"tool_choice,omitempty"`
	Stream      bool          `json:"stream,omitempty"`
}

//go:fix inline
func str(s string) *string { return new(s) }

func toWire(req server.Request, stream bool) wireRequest {
	w := wireRequest{Model: req.Model, MaxTokens: req.MaxTokens, System: req.System, Tools: req.Tools,
		Temperature: req.Temperature, TopP: req.TopP, TopK: req.TopK, Stream: stream}
	if req.ToolChoice != "" {
		w.ToolChoice = &wireChoice{Type: req.ToolChoice}
	}
	if req.Thinking {
		w.Thinking = &wireThinking{Type: "enabled"}
	} else {
		w.Thinking = &wireThinking{Type: "disabled"}
	}
	for _, m := range req.Messages {
		wm := wireMessage{Role: string(m.Role)}
		for _, b := range m.Content {
			wb := wireBlock{Type: b.Type}
			switch b.Type {
			case "text":
				wb.Text = new(b.Text)
			case "thinking":
				wb.Thinking, wb.Signature = new(b.Thinking), new(b.Signature)
			case "redacted_thinking":
				wb.Data = b.Data
			case "tool_use":
				wb.ID, wb.Name, wb.Input = b.ID, b.Name, b.Input
			case "tool_result":
				wb.ToolUseID, wb.Content, wb.IsError = b.ToolUseID, new(b.Result), b.IsError
			}
			wm.Content = append(wm.Content, wb)
		}
		w.Messages = append(w.Messages, wm)
	}
	return w
}

// Stream implements server.Server: POST /v1/messages with stream true, parsing goinfer's SSE events.
func (c *Client) Stream(ctx context.Context, req server.Request, onDelta func(server.Delta)) (*server.Response, error) {
	if err := server.Validate(req); err != nil {
		return nil, err
	}
	start := c.now()
	resp, err := c.post(ctx, "/v1/messages", toWire(req, true))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, readError(resp)
	}
	out, err := parseStream(resp.Body, onDelta)
	if err != nil {
		return nil, err
	}
	out.Usage.Wall = c.now().Sub(start)
	checkToolCalls(out, req.Tools)
	return out, nil
}

// checkToolCalls marks tool calls the loop cannot run: input that is not a JSON object, or a tool not offered.
func checkToolCalls(r *server.Response, tools []server.Tool) {
	offered := map[string]bool{}
	for _, t := range tools {
		offered[t.Name] = true
	}
	for _, b := range r.Content {
		if b.Type != "tool_use" {
			continue
		}
		var obj map[string]json.RawMessage
		why := ""
		switch {
		case !offered[b.Name]:
			why = fmt.Sprintf("no tool named %q", b.Name)
		case json.Unmarshal(b.Input, &obj) != nil:
			why = "input is not a JSON object"
		}
		if why != "" {
			if r.Malformed == nil {
				r.Malformed = map[string]string{}
			}
			r.Malformed[b.ID] = why
		}
	}
}

type sseBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	Signature string          `json:"signature"`
	Data      string          `json:"data"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
}

type sseEvent struct {
	Type    string `json:"type"`
	Index   int    `json:"index"`
	Message struct {
		Usage struct {
			InputTokens int `json:"input_tokens"`
		} `json:"usage"`
	} `json:"message"`
	ContentBlock sseBlock `json:"content_block"`
	Delta        struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		Thinking    string `json:"thinking"`
		Signature   string `json:"signature"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	Usage struct {
		OutputTokens int `json:"output_tokens"`
		// InputTokens and CacheReadInputTokens are pointers so a server that doesn't report them (pre-G3 goinfer:
		// message_delta's usage carries only output_tokens) leaves message_start's whole-prompt InputTokens and a
		// nil Reused untouched, rather than zeroing them. goinfer's G3 (tack's brief,
		// docs/briefs/goinfer-g3-messages-cached-tokens.md) reports both here on every message_delta: input_tokens
		// is now only the tokens this request prefilled, cache_read_input_tokens the leading ones a prior turn's
		// KV held, and the whole prompt is their sum — message_delta's usage replaces message_start's (how the
		// Anthropic SDK accumulates it), so this is the one place that carries the split.
		InputTokens          *int `json:"input_tokens"`
		CacheReadInputTokens *int `json:"cache_read_input_tokens"`
	} `json:"usage"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
	Reason string `json:"reason"`
}

// parseStream reads an SSE body to message_stop. A stream that ends without message_stop is an error: the response
// is incomplete.
func parseStream(body io.Reader, onDelta func(server.Delta)) (*server.Response, error) {
	type open struct {
		block   proto.ModelBlock
		partial strings.Builder
		sawJSON bool
	}
	blocks := map[int]*open{}
	var order []int
	out := &server.Response{}
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	var data strings.Builder
	finished := false
	dispatch := func() error {
		if data.Len() == 0 {
			return nil
		}
		raw := data.String()
		data.Reset()
		var ev sseEvent
		if err := json.Unmarshal([]byte(raw), &ev); err != nil {
			return fmt.Errorf("goinfer: bad SSE data %q: %w", raw, err)
		}
		switch ev.Type {
		case "message_start":
			// Pre-reuse-decided: goinfer sends this before the prefill decides how much of the prefix it reused, so
			// it is the whole prompt and carries no cache_read_input_tokens. message_delta's usage (above) replaces
			// this once the reuse is known, on a server new enough to report it.
			out.Usage.InputTokens = ev.Message.Usage.InputTokens
		case "content_block_start":
			b := ev.ContentBlock
			ob := &open{block: proto.ModelBlock{Type: b.Type, Text: b.Text, Thinking: b.Thinking,
				Signature: b.Signature, Data: b.Data, ID: b.ID, Name: b.Name}}
			if b.Type == "tool_use" {
				ob.block.Input = b.Input
			}
			blocks[ev.Index] = ob
			order = append(order, ev.Index)
		case "content_block_delta":
			ob := blocks[ev.Index]
			if ob == nil {
				return fmt.Errorf("goinfer: delta for unopened block %d", ev.Index)
			}
			switch ev.Delta.Type {
			case "text_delta":
				ob.block.Text += ev.Delta.Text
				if onDelta != nil {
					onDelta(server.Delta{Index: ev.Index, Kind: "text", Text: ev.Delta.Text})
				}
			case "thinking_delta":
				ob.block.Thinking += ev.Delta.Thinking
				if onDelta != nil {
					onDelta(server.Delta{Index: ev.Index, Kind: "thinking", Text: ev.Delta.Thinking})
				}
			case "signature_delta":
				ob.block.Signature += ev.Delta.Signature
			case "input_json_delta":
				ob.partial.WriteString(ev.Delta.PartialJSON)
				ob.sawJSON = true
			}
		case "content_block_stop":
			if ob := blocks[ev.Index]; ob != nil && ob.sawJSON {
				ob.block.Input = json.RawMessage(ob.partial.String())
			}
		case "message_delta":
			out.StopReason = ev.Delta.StopReason
			out.Usage.OutputTokens = ev.Usage.OutputTokens
			if ev.Usage.InputTokens != nil {
				out.Usage.InputTokens = *ev.Usage.InputTokens
			}
			if ev.Usage.CacheReadInputTokens != nil {
				out.Usage.Reused = ev.Usage.CacheReadInputTokens
			}
		case "message_stop":
			finished = true
		case "error":
			return &server.Error{Status: 0, Type: ev.Error.Type, Message: ev.Error.Message}
		}
		return nil
	}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if err := dispatch(); err != nil {
				return nil, err
			}
		case strings.HasPrefix(line, "data:"):
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("goinfer: reading stream: %w", err)
	}
	if err := dispatch(); err != nil {
		return nil, err
	}
	if !finished {
		return nil, fmt.Errorf("goinfer: stream ended without message_stop")
	}
	for _, i := range order {
		b := blocks[i].block
		if b.Type == "tool_use" && len(b.Input) == 0 {
			b.Input = json.RawMessage(`{}`)
		}
		out.Content = append(out.Content, b)
	}
	return out, nil
}

// CountTokens implements server.Server: POST /v1/messages/count_tokens, rendered exactly as /v1/messages would be.
func (c *Client) CountTokens(ctx context.Context, req server.Request) (int, error) {
	resp, err := c.post(ctx, "/v1/messages/count_tokens", toWire(req, false))
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, readError(resp)
	}
	var out struct {
		InputTokens *int `json:"input_tokens"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.InputTokens == nil {
		return 0, fmt.Errorf("goinfer: count_tokens: no input_tokens in response (%v)", err)
	}
	return *out.InputTokens, nil
}

// Models implements server.Server: GET /v1/models. context_window is per entry and absent when unknown.
func (c *Client) Models(ctx context.Context) ([]server.ModelInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/models", nil)
	if err != nil {
		return nil, err
	}
	c.auth(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, readError(resp)
	}
	var out struct {
		Data []struct {
			ID            string `json:"id"`
			ContextWindow int    `json:"context_window"`
			FastModel     string `json:"fast_model"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("goinfer: /v1/models: %w", err)
	}
	var ms []server.ModelInfo
	for _, d := range out.Data {
		ms = append(ms, server.ModelInfo{ID: d.ID, ContextWindow: d.ContextWindow, FastModel: d.FastModel})
	}
	return ms, nil
}

func (c *Client) post(ctx context.Context, path string, body any) (*http.Response, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	c.auth(req)
	return c.http.Do(req)
}

func (c *Client) auth(req *http.Request) {
	if c.apiKey != "" {
		req.Header.Set("x-api-key", c.apiKey)
	}
}

// readError turns a non-200 response into a *server.Error. goinfer answers in three shapes: Anthropic's
// {"type":"error","error":{type,message}}, OpenAI's {"error":{message,type}}, and its halt gate's
// {"error":"halted","reason":…}.
func readError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	e := &server.Error{Status: resp.StatusCode}
	if ra, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil {
		e.RetryAfter = time.Duration(ra) * time.Second
	}
	var obj struct {
		Error  json.RawMessage `json:"error"`
		Reason string          `json:"reason"`
	}
	if json.Unmarshal(body, &obj) == nil && len(obj.Error) > 0 {
		var s string
		var inner struct{ Type, Message string }
		if json.Unmarshal(obj.Error, &s) == nil {
			e.Type, e.Message = s, obj.Reason
		} else if json.Unmarshal(obj.Error, &inner) == nil {
			e.Type, e.Message = inner.Type, inner.Message
		}
	}
	if e.Type == "" {
		e.Type, e.Message = http.StatusText(resp.StatusCode), strings.TrimSpace(string(body))
	}
	return e
}
