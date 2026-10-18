package pilot

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/townsendmerino/tack/internal/config"
	"github.com/townsendmerino/tack/internal/loop"
)

// TestTackConfigLoads: the config an A1 run gets is one tack's own loader accepts, with the registered sampling and
// budgets that leave the cap as the only limit.
func TestTackConfigLoads(t *testing.T) {
	out := t.TempDir()
	os.WriteFile(filepath.Join(out, "token"), []byte("k\n"), 0o600)
	p := filepath.Join(out, "config.toml")
	os.WriteFile(p, []byte(tackConfig(Server{BaseURL: "http://127.0.0.1:1", Model: "qwen"}, out)), 0o600)
	c, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	s := c.Servers["pilot"]
	if s.URL != "http://127.0.0.1:1" || s.Model != "qwen" || *s.Temperature != 0.7 || *s.TopP != 0.8 || *s.TopK != 20 {
		t.Fatalf("server %+v", s)
	}
	if c.Run.MaxTokens != replyLimit || c.Run.MaxRequests < 100000 || c.Run.MaxErrors < 100000 || c.Run.MaxWall.Hours() < 1 {
		t.Fatalf("budgets %+v", c.Run)
	}
	var raw map[string]any
	if _, err := toml.DecodeFile(p, &raw); err != nil {
		t.Fatal(err)
	}
}

func TestSummarizeTack(t *testing.T) {
	p := filepath.Join(t.TempDir(), "events.jsonl")
	lines := []string{
		`{"seq":2,"time":"2026-10-17T12:00:00Z","type":"request.sent","cause":1,"payload":{"request":1,"server":"goinfer@pilot","model":"qwen"}}`,
		`{"seq":3,"time":"2026-10-17T12:00:01Z","type":"tool.call","cause":2,"payload":{"id":"a","name":"done","input":{"reason":"x"}}}`,
		`{"seq":4,"time":"2026-10-17T12:00:01Z","type":"tool.finished","cause":3,"payload":{"id":"a","outcome":"ok","result":"` + loop.DoneNoChange + `","truncated":false}}`,
		`{"seq":5,"time":"2026-10-17T12:00:02Z","type":"request.sent","cause":1,"payload":{"request":2,"server":"goinfer@pilot","model":"qwen"}}`,
		`{"seq":6,"time":"2026-10-17T12:00:03Z","type":"tool.call","cause":5,"payload":{"id":"b","name":"done","input":{"reason":"y"}}}`,
		`{"seq":7,"time":"2026-10-17T12:00:03Z","type":"turn.end","cause":1,"payload":{"stop_reason":"done"}}`,
	}
	os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
	s := SummarizeTack(p)
	if s.ToolCalls["done"] != 2 || s.Requests != 2 || s.StopReason != "done" || s.DoneRefused != 1 {
		t.Fatalf("summary %+v", s)
	}
}

// TestPairTimings pairs goinfer's model request lines with the capture's model requests in order, skipping
// count_tokens on either side, and refuses a mismatch.
func TestPairTimings(t *testing.T) {
	dir := t.TempDir()
	write := func(n, body string) { os.WriteFile(filepath.Join(dir, n+".meta"), []byte(body), 0o644) }
	write("0001", "GET /v1/models 200 ttfb_ms=3 total_ms=3\n")
	write("0002", "POST /v1/messages 200 ttfb_ms=900 total_ms=2100\n")
	write("0003", "POST /v1/messages 200 ttfb_ms=400 total_ms=1500\n")
	reqs := []Request{
		{Raw: "request: POST /v1/messages/count_tokens model=qwen", Prompt: 30},
		{Raw: "request: POST /v1/messages model=qwen status=200", Prompt: 11000, Reused: 0, TTFTms: 850, Totalms: 2000},
		{Raw: "request: POST /v1/messages model=qwen status=200", Prompt: 11500, Reused: 11000, TTFTms: 350, Totalms: 1420},
	}
	got, err := PairTimings(reqs, dir)
	if err != nil || len(got) != 2 {
		t.Fatalf("%+v %v", got, err)
	}
	if got[0].TailnetMs != 100 || got[0].Prefilled != 11000 || got[1].TailnetMs != 80 || got[1].Prefilled != 500 || got[1].ClientTTFBms != 400 {
		t.Fatalf("paired %+v", got)
	}
	if _, err := PairTimings(reqs[:2], dir); err == nil {
		t.Fatal("a count mismatch was paired")
	}
}

func TestServerFeatures(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.log")
	os.WriteFile(p, []byte("goinfer serving on http://127.0.0.1:1\n  features: tools (constrainable call form) · structured output\nrequest: POST /v1/messages model=qwen\n"), 0o644)
	if f, m := ServerFeatures(p); f != "tools (constrainable call form) · structured output" || m != "not reported" {
		t.Fatalf("today's banner: %q %q", f, m)
	}
	os.WriteFile(p, []byte("  features: tools\n  forced calls: post-check (no constrainable call form)\n"), 0o644)
	if _, m := ServerFeatures(p); m != "post-check" {
		t.Fatalf("a reported mode: %q", m)
	}
}

// TestForcedToolCall reads the model's forced_tool_call from /v1/models, and "" from a server that does not report it.
func TestForcedToolCall(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer k" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"object":"list","data":[{"id":"other","forced_tool_call":"off"},{"id":"qwen","forced_tool_call":"grammar"}]}`))
	}))
	defer ts.Close()
	if m := ForcedToolCall(context.Background(), ts.URL, "k", "qwen"); m != "grammar" {
		t.Fatalf("mode %q", m)
	}
	old := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"object":"list","data":[{"id":"qwen"}]}`))
	}))
	defer old.Close()
	if m := ForcedToolCall(context.Background(), old.URL, "k", "qwen"); m != "" {
		t.Fatalf("an older server: %q", m)
	}
}
