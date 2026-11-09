package openai_test

// An external test package: it imports loop, which imports models, which imports this package.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/townsendmerino/tack/internal/loop"
	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server"
	"github.com/townsendmerino/tack/internal/server/openai"
	"github.com/townsendmerino/tack/internal/store"
	"github.com/townsendmerino/tack/internal/tools"
)

func stream(chunks ...string) string {
	var b strings.Builder
	for _, c := range chunks {
		b.WriteString("data: " + c + "\n\n")
	}
	return b.String() + "data: [DONE]\n\n"
}

// source is a models.Source over a fixed server.
type source struct{ srv server.Server }

func (s source) Ensure(context.Context) (server.Server, error) { return s.srv, nil }
func (source) Model() string                                   { return "qwen" }
func (source) Window() int                                     { return 16384 }
func (source) Touch()                                          {}
func (source) Close() error                                    { return nil }

// TestALoopTurnOverChatCompletions: the agent loop runs a turn with a tool call over this server as over goinfer's:
// it reads the file, sends the result back as a tool message, and ends on the answer.
func TestALoopTurnOverChatCompletions(t *testing.T) {
	streams := []string{
		stream(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"read","arguments":"{\"path\":\"a.go\"}"}}]},"finish_reason":"tool_calls"}]}`,
			`{"choices":[],"usage":{"prompt_tokens":900,"completion_tokens":12}}`),
		stream(`{"choices":[{"index":0,"delta":{"content":"It is in a.go."},"finish_reason":"stop"}]}`,
			`{"choices":[],"usage":{"prompt_tokens":950,"completion_tokens":6}}`),
	}
	var mu sync.Mutex
	var bodies []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		io.WriteString(w, streams[0])
		streams = streams[1:]
	}))
	t.Cleanup(ts.Close)
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o644)
	ws, err := tools.NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	log, err := st.Create("s_openai")
	if err != nil {
		t.Fatal(err)
	}
	sess := loop.New(loop.Config{Source: source{openai.New("openai@test", ts.URL, "", ts.Client())}, Tools: tools.NewSet(nil),
		WS: ws, Log: log, Blobs: st.Blobs,
		Budget: loop.Budget{MaxRequests: 4, MaxToolCalls: 4, MaxTokens: 64, TurnTokens: 1 << 20, MaxErrors: 4,
			SessionTokens: 1 << 20, SessionToolCalls: 10, MaxWall: 60e9}})
	reason, err := sess.Turn(context.Background(), "Where is A?")
	if err != nil || reason != proto.StopEndTurn {
		t.Fatalf("reason %q, %v", reason, err)
	}
	var second map[string]any
	json.Unmarshal([]byte(bodies[1]), &second)
	msgs, _ := json.Marshal(second["messages"])
	if !strings.Contains(string(msgs), `"role":"tool","tool_call_id":"call_1"`) || !strings.Contains(string(msgs), "package a") {
		t.Errorf("the second request does not carry the read's result as a tool message:\n%s", msgs)
	}
}
