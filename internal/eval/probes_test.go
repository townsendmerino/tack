package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server/goinfer"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
)

// probeServer answers every /v1/messages request with body and keeps the last request.
func probeServer(t *testing.T, body string) (*goinfer.Client, *map[string]any) {
	t.Helper()
	var last map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &last)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, body)
	}))
	t.Cleanup(ts.Close)
	return goinfer.New("goinfer@test", ts.URL, "", nil), &last
}

// TestFirstTokenSeesAReplyThatOpensWithThinking: the first-token probe asks for thinking, allows ttftMaxTokens, and times
// the first streamed delta of either kind, so a reply whose opening tokens never stream as text (Harmony's channel
// header, Qwen3's <think>) is still timed. A reply that streams nothing is an error.
func TestFirstTokenSeesAReplyThatOpensWithThinking(t *testing.T) {
	srv, last := probeServer(t, goinfertest.SSEWithReuse("max_tokens", 100, 0, 16,
		proto.ModelBlock{Type: "thinking", Thinking: "The user"}, proto.ModelBlock{Type: "text", Text: "Hi"}))
	d, kind, err := firstToken(context.Background(), srv, "m", "sys", "hello")
	if err != nil || kind != "thinking" || d <= 0 {
		t.Fatalf("first token %s, kind %q, err %v", d, kind, err)
	}
	if mt, _ := (*last)["max_tokens"].(float64); int(mt) != ttftMaxTokens || ttftMaxTokens < 8 {
		t.Errorf("max_tokens %v, want ttftMaxTokens (%d), room for a header that never streams", (*last)["max_tokens"], ttftMaxTokens)
	}
	if th, _ := (*last)["thinking"].(map[string]any); th["type"] != "enabled" {
		t.Errorf("the probe did not ask for thinking: %v", (*last)["thinking"])
	}
	srv, _ = probeServer(t, goinfertest.SSEWithReuse("max_tokens", 100, 0, 16))
	if _, _, err := firstToken(context.Background(), srv, "m", "sys", "hello"); err == nil || !strings.Contains(err.Error(), "no text or thinking") {
		t.Errorf("a reply that streamed nothing: err %v", err)
	}
}

// TestDecodeRefusesTokensThatNeverStreamed: the decode probe asks for thinking and times thinking and text alike; a
// reply that counted more tokens than it streamed characters (reasoning held back) is an error, not an inflated rate.
func TestDecodeRefusesTokensThatNeverStreamed(t *testing.T) {
	long := strings.Repeat("word ", 80) // 400 characters
	srv, last := probeServer(t, goinfertest.SSEWithReuse("max_tokens", 50, 0, 100,
		proto.ModelBlock{Type: "thinking", Thinking: long}, proto.ModelBlock{Type: "text", Text: long}))
	s, err := decodeOnce(context.Background(), srv, "m")
	if err != nil || s.Rate <= 0 || s.Tokens != 100 || s.Thinking != 400 || s.TextChars != 400 {
		t.Fatalf("a reply that streamed everything: %+v, err %v", s, err)
	}
	if th, _ := (*last)["thinking"].(map[string]any); th["type"] != "enabled" {
		t.Errorf("the probe did not ask for thinking: %v", (*last)["thinking"])
	}
	// 256 tokens counted, 40 characters streamed: most of the reply never reached the stream.
	srv, _ = probeServer(t, goinfertest.SSEWithReuse("max_tokens", 50, 0, 256,
		proto.ModelBlock{Type: "text", Text: "Once upon"}, proto.ModelBlock{Type: "text", Text: " a time, a keeper lit a"}))
	if s, err := decodeOnce(context.Background(), srv, "m"); err == nil || !strings.Contains(err.Error(), "held tokens back") {
		t.Errorf("a reply that held tokens back: %+v, err %v", s, err)
	}
}
