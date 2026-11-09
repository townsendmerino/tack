// Package openai is tack's model server over an OpenAI-compatible /v1/chat/completions (DESIGN §6.4; P1 step 7): goinfer's
// own, or any server speaking that API. It implements server.Server, so the loop is the same over it as over goinfer's
// Messages API (package goinfer).
//
// What the wire cannot carry: thinking. A thinking block in the history is not sent back (the API has no field for it,
// and no signature to keep), and Request.Thinking asks for nothing; a server's reasoning_content is streamed as thinking
// and kept in the response. CountTokens has no endpoint here and says so; only the eval harness calls it.
package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server"
)

// Client is one OpenAI-compatible server.
type Client struct {
	name, baseURL, apiKey string
	http                  *http.Client
	now                   func() time.Time
}

// New is a client of the server at baseURL (no trailing /v1), named name in events; apiKey, when set, goes as a Bearer
// token. hc nil means http.DefaultClient.
func New(name, baseURL, apiKey string, hc *http.Client) *Client {
	if hc == nil {
		hc = http.DefaultClient
	}
	return &Client{name: name, baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey, http: hc, now: time.Now}
}

// Name implements server.Server.
func (c *Client) Name() string { return c.name }

// ErrNoCount is CountTokens's answer: the API has no count endpoint.
var ErrNoCount = errors.New("openai: an OpenAI-compatible server has no endpoint to count tokens")

// CountTokens implements server.Server: there is no endpoint for it.
func (c *Client) CountTokens(context.Context, server.Request) (int, error) { return 0, ErrNoCount }

type wireFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
	Arguments   *string         `json:"arguments,omitempty"`
}

type wireTool struct {
	Type     string       `json:"type"`
	Function wireFunction `json:"function"`
}

type wireCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function wireFunction `json:"function"`
}

type wireMessage struct {
	Role       string     `json:"role"`
	Content    *string    `json:"content"`
	ToolCalls  []wireCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type wireRequest struct {
	Model         string          `json:"model"`
	Messages      []wireMessage   `json:"messages"`
	Tools         []wireTool      `json:"tools,omitempty"`
	ToolChoice    string          `json:"tool_choice,omitempty"`
	MaxTokens     int             `json:"max_tokens,omitempty"`
	Temperature   *float64        `json:"temperature,omitempty"`
	TopP          *float64        `json:"top_p,omitempty"`
	TopK          *int            `json:"top_k,omitempty"`
	Stream        bool            `json:"stream"`
	StreamOptions *map[string]any `json:"stream_options,omitempty"`
}

func text(s string) *string { return &s }

// toWire is req as the chat completions API takes it. A user message's tool results become tool messages, in order,
// and its text, if any, a user message after them; an assistant message keeps its text and its tool calls.
func toWire(req server.Request) wireRequest {
	w := wireRequest{Model: req.Model, MaxTokens: req.MaxTokens, Temperature: req.Temperature, TopP: req.TopP, TopK: req.TopK,
		Stream: true, StreamOptions: &map[string]any{"include_usage": true}}
	switch req.ToolChoice {
	case "any":
		w.ToolChoice = "required"
	case "none":
		w.ToolChoice = "none"
	}
	if req.System != "" {
		w.Messages = append(w.Messages, wireMessage{Role: "system", Content: text(req.System)})
	}
	for _, t := range req.Tools {
		w.Tools = append(w.Tools, wireTool{Type: "function", Function: wireFunction{Name: t.Name, Description: t.Description, Parameters: t.InputSchema}})
	}
	for _, m := range req.Messages {
		var texts []string
		var calls []wireCall
		for _, b := range m.Content {
			switch b.Type {
			case "text":
				texts = append(texts, b.Text)
			case "tool_use":
				args := string(b.Input)
				calls = append(calls, wireCall{ID: b.ID, Type: "function", Function: wireFunction{Name: b.Name, Arguments: &args}})
			case "tool_result":
				w.Messages = append(w.Messages, wireMessage{Role: "tool", ToolCallID: b.ToolUseID, Content: text(b.Result)})
			}
		}
		switch {
		case m.Role == server.Assistant:
			msg := wireMessage{Role: "assistant", ToolCalls: calls}
			if len(texts) > 0 || len(calls) == 0 {
				msg.Content = text(strings.Join(texts, ""))
			}
			w.Messages = append(w.Messages, msg)
		case len(texts) > 0:
			w.Messages = append(w.Messages, wireMessage{Role: "user", Content: text(strings.Join(texts, "\n\n"))})
		}
	}
	return w
}

// Stream implements server.Server: POST /v1/chat/completions with stream on.
func (c *Client) Stream(ctx context.Context, req server.Request, onDelta func(server.Delta)) (*server.Response, error) {
	if err := server.Validate(req); err != nil {
		return nil, err
	}
	start := c.now()
	resp, err := c.post(ctx, "/v1/chat/completions", toWire(req))
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
	markMalformed(out, req.Tools)
	return out, nil
}

type chunk struct {
	Choices []struct {
		Delta struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
			ToolCalls        []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens        int `json:"prompt_tokens"`
		CompletionTokens    int `json:"completion_tokens"`
		PromptTokensDetails *struct {
			CachedTokens *int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
}

// stopReasons maps a finish_reason to tack's stop reasons (Anthropic's names, as the loop reads them).
var stopReasons = map[string]string{"stop": "end_turn", "tool_calls": "tool_use", "length": "max_tokens"}

// parseStream reads an SSE stream of chat.completion.chunk objects into one response.
func parseStream(body io.Reader, onDelta func(server.Delta)) (*server.Response, error) {
	var thinking, content strings.Builder
	type call struct{ id, name, args string }
	calls := map[int]*call{}
	out := &server.Response{}
	finished := false
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for sc.Scan() {
		data, ok := strings.CutPrefix(sc.Text(), "data:")
		if !ok {
			continue
		}
		data = strings.TrimSpace(data)
		if data == "[DONE]" {
			break
		}
		var ch chunk
		if err := json.Unmarshal([]byte(data), &ch); err != nil {
			return nil, fmt.Errorf("openai: a stream chunk is not JSON: %w", err)
		}
		if u := ch.Usage; u != nil {
			out.Usage.InputTokens, out.Usage.OutputTokens = u.PromptTokens, u.CompletionTokens
			if d := u.PromptTokensDetails; d != nil && d.CachedTokens != nil {
				// cached_tokens are included in prompt_tokens: InputTokens is then the prefilled share (server.Usage).
				r := *d.CachedTokens
				out.Usage.Reused, out.Usage.InputTokens = &r, u.PromptTokens-r
			}
		}
		for _, choice := range ch.Choices {
			d := choice.Delta
			if d.ReasoningContent != "" {
				thinking.WriteString(d.ReasoningContent)
				onDelta(server.Delta{Index: 0, Kind: "thinking", Text: d.ReasoningContent})
			}
			if d.Content != "" {
				content.WriteString(d.Content)
				onDelta(server.Delta{Index: 1, Kind: "text", Text: d.Content})
			}
			for _, tc := range d.ToolCalls {
				cl := calls[tc.Index]
				if cl == nil {
					cl = &call{}
					calls[tc.Index] = cl
				}
				if tc.ID != "" {
					cl.id = tc.ID
				}
				if tc.Function.Name != "" {
					cl.name = tc.Function.Name
				}
				cl.args += tc.Function.Arguments
			}
			if r := choice.FinishReason; r != nil && *r != "" {
				finished = true
				if s, ok := stopReasons[*r]; ok {
					out.StopReason = s
				} else {
					out.StopReason = *r
				}
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if !finished {
		return nil, errors.New("openai: the stream ended without a finish_reason")
	}
	if thinking.Len() > 0 {
		out.Content = append(out.Content, proto.ModelBlock{Type: "thinking", Thinking: thinking.String()})
	}
	if content.Len() > 0 {
		out.Content = append(out.Content, proto.ModelBlock{Type: "text", Text: content.String()})
	}
	idx := make([]int, 0, len(calls))
	for i := range calls {
		idx = append(idx, i)
	}
	sort.Ints(idx)
	for _, i := range idx {
		cl := calls[i]
		in := json.RawMessage(cl.args)
		if strings.TrimSpace(cl.args) == "" {
			in = json.RawMessage("{}")
		}
		out.Content = append(out.Content, proto.ModelBlock{Type: "tool_use", ID: cl.id, Name: cl.name, Input: in})
	}
	return out, nil
}

// markMalformed marks the tool calls the loop cannot run: a tool not offered, or arguments that are not a JSON object.
func markMalformed(r *server.Response, tools []server.Tool) {
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

// Models implements server.Server: GET /v1/models. context_window and fast_model are goinfer's additions, absent
// elsewhere.
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
		return nil, fmt.Errorf("openai: /v1/models: %w", err)
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
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
}

// readError turns a non-200 response into a *server.Error, from OpenAI's {"error":{"message","type"}} or the raw body.
func readError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	e := &server.Error{Status: resp.StatusCode}
	if ra, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil {
		e.RetryAfter = time.Duration(ra) * time.Second
	}
	var obj struct {
		Error *struct{ Type, Message string } `json:"error"`
	}
	if json.Unmarshal(body, &obj) == nil && obj.Error != nil {
		e.Type, e.Message = obj.Error.Type, obj.Error.Message
	}
	if e.Type == "" {
		e.Type, e.Message = http.StatusText(resp.StatusCode), strings.TrimSpace(string(body))
	}
	return e
}
