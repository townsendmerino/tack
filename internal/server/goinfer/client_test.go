package goinfer_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server"
	"github.com/townsendmerino/tack/internal/server/cassette"
	"github.com/townsendmerino/tack/internal/server/goinfer"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
)

// replay returns a client answering from the named recorded cassette, and the replayer to check with Done.
func replay(t *testing.T, name string) (*goinfer.Client, *cassette.Replayer) {
	t.Helper()
	c, err := cassette.Load(filepath.Join("testdata", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	r := cassette.NewReplayer(c)
	return goinfer.New("goinfer@local", "http://goinfer.test", "", r.Client()), r
}

// synthetic returns a client answering from hand-built interactions.
func synthetic(its ...cassette.Interaction) (*goinfer.Client, *cassette.Replayer) {
	r := cassette.NewReplayer(&cassette.Cassette{Note: "synthetic", Interactions: its})
	return goinfer.New("goinfer@local", "http://goinfer.test", "", r.Client()), r
}

func TestModelsRecorded(t *testing.T) {
	c, r := replay(t, "models")
	ms, err := c.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 1 || ms[0].ID != "tiny" || ms[0].ContextWindow != 4096 || ms[0].FastModel != "tiny-fast" {
		t.Fatalf("models = %+v", ms)
	}
	if err := r.Done(); err != nil {
		t.Fatal(err)
	}
}

func TestStreamTextRecorded(t *testing.T) {
	c, r := replay(t, "text")
	var streamed strings.Builder
	resp, err := goinfertest.Text(context.Background(), c, "tiny", func(d server.Delta) { streamed.WriteString(d.Text) })
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Content) != 1 || resp.Content[0].Type != "text" || resp.Content[0].Text != streamed.String() {
		t.Fatalf("content %+v, streamed %q", resp.Content, streamed.String())
	}
	if resp.StopReason != "max_tokens" || resp.Usage.InputTokens != 74 || resp.Usage.OutputTokens != 16 {
		t.Fatalf("stop %q usage %+v", resp.StopReason, resp.Usage)
	}
	if resp.Usage.Reused == nil || *resp.Usage.Reused != 0 { // a cold request under G3 reports a measured 0
		t.Fatalf("Reused = %v, want a pointer to 0", resp.Usage.Reused)
	}
	if err := r.Done(); err != nil {
		t.Fatal(err)
	}
}

// TestStreamG3Reuse is a synthetic goinfer G3 stream (docs/briefs/goinfer-g3-messages-cached-tokens.md):
// message_delta's usage carries the prefilled/reused split, which replaces message_start's whole-prompt figure.
// InputTokens must come back as the prefilled count alone, not the whole prompt — callers that want the whole
// prompt sum it with Reused themselves (internal/loop/loop.go does, for the ledger's Usage.Prompt).
func TestStreamG3Reuse(t *testing.T) {
	conv := &server.Conversation{Model: "tiny", MaxTokens: 16}
	_ = conv.BeginTurn("hi", false)
	req := conv.Request()
	body := goinfertest.SSEWithReuse("end_turn", 35, 59, 16, proto.ModelBlock{Type: "text", Text: "hello"})
	c, _ := synthetic(cassette.Interaction{Request: recordedRequest(t, req), Response: sseResp(body)})
	resp, err := c.Stream(context.Background(), req, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Usage.InputTokens != 35 {
		t.Errorf("InputTokens = %d, want 35 (prefilled only, not the 100 message_start reported before the reuse was decided)", resp.Usage.InputTokens)
	}
	if resp.Usage.Reused == nil || *resp.Usage.Reused != 59 {
		t.Fatalf("Reused = %v, want a pointer to 59", resp.Usage.Reused)
	}
	if resp.Usage.OutputTokens != 16 {
		t.Errorf("OutputTokens = %d, want 16", resp.Usage.OutputTokens)
	}
}

// TestStreamG3ColdRequestReusedIsZeroNotNil is break-it-first for the nil/zero distinction events.go's Usage doc
// comment requires ("a nil field is one the server did not report; it is never shown as zero"): a cold request
// under G3 still reports cache_read_input_tokens, as a real 0, so Reused must point at 0, not stay nil.
func TestStreamG3ColdRequestReusedIsZeroNotNil(t *testing.T) {
	conv := &server.Conversation{Model: "tiny", MaxTokens: 16}
	_ = conv.BeginTurn("hi", false)
	req := conv.Request()
	body := goinfertest.SSEWithReuse("end_turn", 100, 0, 16, proto.ModelBlock{Type: "text", Text: "hello"})
	c, _ := synthetic(cassette.Interaction{Request: recordedRequest(t, req), Response: sseResp(body)})
	resp, err := c.Stream(context.Background(), req, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Usage.Reused == nil || *resp.Usage.Reused != 0 {
		t.Fatalf("Reused = %v, want a pointer to 0 (measured, not unknown)", resp.Usage.Reused)
	}
}

// TestToolLoopRecorded replays a real constrained tool call and the follow-up request carrying its result. The second
// request only matches the recording if the client sent back the tool_use as received and the tool_result after it.
func TestToolLoopRecorded(t *testing.T) {
	c, r := replay(t, "tools")
	resps, err := goinfertest.ToolLoop(context.Background(), c, "tiny")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Done(); err != nil {
		t.Fatal(err)
	}
	first := resps[0]
	if first.StopReason != "tool_use" || len(first.Content) != 1 || first.Content[0].Name != "grep" || first.Malformed != nil {
		t.Fatalf("first response %+v", first)
	}
	var in map[string]string
	if err := json.Unmarshal(first.Content[0].Input, &in); err != nil || in["pattern"] != "func main" {
		t.Fatalf("tool input %s: %v", first.Content[0].Input, err)
	}
	// G3 from a real server: the second request extends the first, so goinfer reused the first's 949-token prompt and
	// prefilled only the 167 new tokens. message_start said 1116, the whole prompt, before the reuse was decided; the
	// client reports the split from message_delta.
	if u := first.Usage; u.InputTokens != 949 || u.Reused == nil || *u.Reused != 0 {
		t.Errorf("first usage: InputTokens %d, Reused %v; want 949 prefilled, 0 reused", u.InputTokens, u.Reused)
	}
	if u := resps[1].Usage; u.InputTokens != 167 || u.Reused == nil || *u.Reused != 949 {
		t.Errorf("second usage: InputTokens %d, Reused %v; want 167 prefilled, 949 reused", u.InputTokens, u.Reused)
	}
}

func TestCountTokensRecorded(t *testing.T) {
	c, r := replay(t, "count_tokens")
	n, err := c.CountTokens(context.Background(), goinfertest.ToolConversation("tiny").Request())
	if err != nil || n != 949 {
		t.Fatalf("count = %d, %v", n, err)
	}
	if err := r.Done(); err != nil {
		t.Fatal(err)
	}
}

// TestCassetteCatchesAChangedRequest is the replayer's own break-it-first check: the recorded text request, sent
// with one word changed, must not get the recorded answer.
func TestCassetteCatchesAChangedRequest(t *testing.T) {
	c, r := replay(t, "text")
	conv := &server.Conversation{Model: "tiny", System: "You are terse!", MaxTokens: 16}
	_ = conv.BeginTurn("Reply with exactly: hello world", false)
	if _, err := c.Stream(context.Background(), conv.Request(), nil); err == nil {
		t.Fatal("a changed request was answered from the cassette")
	}
	if err := r.Done(); err == nil || !strings.Contains(err.Error(), "body.system") {
		t.Fatalf("Done = %v, want a mismatch naming body.system", err)
	}
}

func TestErrors(t *testing.T) {
	conv := &server.Conversation{Model: "tiny", MaxTokens: 8}
	_ = conv.BeginTurn("hi", false)
	req := conv.Request()
	cases := []struct {
		name      string
		resp      cassette.Resp
		status    int
		typ       string
		retryable bool
		after     time.Duration
	}{
		{"queue full", cassette.Resp{Status: 529, ContentType: "application/json", RetryAfter: "1",
			Body: `{"type":"error","error":{"type":"overloaded_error","message":"queue full"}}`}, 529, "overloaded_error", true, time.Second},
		{"halted", cassette.Resp{Status: 503, ContentType: "application/json",
			Body: `{"error":"halted","reason":"owner"}`}, 503, "halted", false, 0},
		{"inflight cap", cassette.Resp{Status: 503, ContentType: "application/json", RetryAfter: "2",
			Body: `{"error":{"message":"too many requests in flight","type":"api_error"}}`}, 503, "api_error", true, 2 * time.Second},
		{"overflow", cassette.Resp{Status: 400, ContentType: "application/json",
			Body: `{"type":"error","error":{"type":"invalid_request_error","message":"prompt is too long: 5000 tokens > 4096 maximum"}}`}, 400, "invalid_request_error", false, 0},
	}
	for _, tc := range cases {
		c, _ := synthetic(cassette.Interaction{Request: recordedRequest(t, req), Response: tc.resp})
		_, err := c.Stream(context.Background(), req, nil)
		var se *server.Error
		if !errors.As(err, &se) || se.Status != tc.status || se.Type != tc.typ || se.Retryable() != tc.retryable || se.RetryAfter != tc.after {
			t.Errorf("%s: %#v", tc.name, err)
		}
	}
}

func TestStreamWithoutMessageStopIsAnError(t *testing.T) {
	conv := &server.Conversation{Model: "tiny", MaxTokens: 8}
	_ = conv.BeginTurn("hi", false)
	req := conv.Request()
	body := goinfertest.SSE("end_turn", proto.ModelBlock{Type: "text", Text: "partial"})
	body = body[:strings.Index(body, "event: message_stop")]
	c, _ := synthetic(cassette.Interaction{Request: recordedRequest(t, req), Response: sseResp(body)})
	if _, err := c.Stream(context.Background(), req, nil); err == nil {
		t.Fatal("a truncated stream was accepted")
	}
}

// recordedRequest is what the client sends for req, as a cassette records it.
func recordedRequest(t *testing.T, req server.Request) cassette.Req {
	t.Helper()
	rec := cassette.NewRecorder("", roundTripFunc(func() string {
		return goinfertest.SSE("end_turn", proto.ModelBlock{Type: "text", Text: "x"})
	}))
	c := goinfer.New("goinfer@local", "http://goinfer.test", "", rec.Client())
	if _, err := c.Stream(context.Background(), req, nil); err != nil {
		t.Fatal(err)
	}
	return rec.C.Interactions[0].Request
}

func sseResp(body string) cassette.Resp {
	return cassette.Resp{Status: 200, ContentType: "text/event-stream", Body: body}
}
