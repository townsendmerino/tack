package pilot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
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
	if c.Run.MaxTokens != replyLimit || c.Run.MaxRequests < 100000 || c.Run.MaxErrors != 8 || c.Run.MaxWall.Hours() < 1 {
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
	p := filepath.Join(t.TempDir(), "a.log")
	read := func(banner string) (string, string) {
		os.WriteFile(p, []byte(banner+"request: POST /v1/messages model=qwen\n"), 0o644)
		return ServerFeatures(p)
	}
	// goinfer 31418112's own banner for the pilot's model (2026-10-17): the mode is derived from its chatml template.
	f, m := read("goinfer serving on http://127.0.0.1:37313 []\n" +
		"loaded \"qwen\": 28-layer model (vocab 152064) in 12.17s [chat: chatml, thinking: unmanaged]\n" +
		"  features: tools (constrainable call form) · structured output\n")
	if f != "tools (constrainable call form) · structured output" || m != "grammar (derived from the chatml template; build predates the field)" {
		t.Fatalf("31418112's banner: %q %q", f, m)
	}
	if _, m := read("loaded \"g\": 30-layer model [chat: gemma4, thinking: managed]\n  features: tools\n"); m != "not reported" {
		t.Fatalf("a template with no JSON call form: %q", m)
	}
	if _, m := read("loaded \"q\": 28-layer model [chat: chatml]\n  forced calls: post-check (no constrainable call form)\n"); m != "post-check" {
		t.Fatalf("a reported mode: %q", m)
	}
	if _, m := read("  features: tools\n"); m != "not reported" {
		t.Fatalf("no loaded line: %q", m)
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

// TestSummarizeTackCountsRestatedRefusals: a refusal that quotes the task (loop.DoneNoChangeRestated, [run]
// restate_task) counts in DoneRefused like the plain one.
func TestSummarizeTackCountsRestatedRefusals(t *testing.T) {
	p := filepath.Join(t.TempDir(), "events.jsonl")
	refusal, _ := json.Marshal(loop.DoneNoChangeRestated("Rename the flag."))
	lines := []string{
		`{"seq":3,"time":"2026-10-25T12:00:01Z","type":"tool.call","cause":2,"payload":{"id":"a","name":"done","input":{"reason":"x"}}}`,
		`{"seq":4,"time":"2026-10-25T12:00:01Z","type":"tool.finished","cause":3,"payload":{"id":"a","outcome":"ok","result":` + string(refusal) + `,"truncated":false}}`,
		`{"seq":7,"time":"2026-10-25T12:00:03Z","type":"turn.end","cause":1,"payload":{"stop_reason":"done"}}`,
	}
	os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
	if s := SummarizeTack(p); s.DoneRefused != 1 {
		t.Fatalf("summary %+v", s)
	}
}

// An A1 run's left_repo names each path outside the repository that a tool call's input reaches for: an absolute one
// and one climbing out with .., once each, sorted, but not the repository's own files or the system paths every build reads.
func TestTackLeftRepo(t *testing.T) {
	repo, out := t.TempDir(), t.TempDir()
	mirror := filepath.Join(t.TempDir(), "mirrors", "goinfer.git")
	call := func(id, name, input string) string {
		return `{"seq":1,"type":"tool.call","payload":{"id":"` + id + `","name":"` + name + `","input":` + input + `}}`
	}
	cmd := func(s string) string { b, _ := json.Marshal(map[string]string{"command": s}); return string(b) }
	lines := []string{
		call("1", "read", `{"path":"pkg/a.go"}`),
		call("2", "bash", cmd("git -C "+mirror+" log --oneline 2>/dev/null | head")),
		call("3", "bash", cmd("/usr/bin/grep -rn Foo "+filepath.Join(repo, "pkg")+" && ls ../../runs")),
		call("4", "bash", cmd("cat "+mirror+"/HEAD")),
		`{"seq":2,"type":"tool.finished","payload":{"id":"2","outcome":"ok","result":"/elsewhere/not/a/call"}}`,
	}
	p := filepath.Join(out, "events.jsonl")
	os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
	got := TackLeftRepo(p, repo)
	want := []string{mirror, filepath.Clean(filepath.Join(resolved(repo), "../../runs")), mirror + "/HEAD"}
	sort.Strings(want)
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("left_repo %q, want %q", got, want)
	}
}

// TestE1RunsWithSnapshots: every arm's config turns them on, as the released tack has them (ADR-006, Francis, 2026-11-04). Runs from
// here on are not directly comparable with earlier snapshot-off runs (DESIGN §6.8).
func TestE1RunsWithSnapshots(t *testing.T) {
	cfg := tackConfig(Server{BaseURL: "http://127.0.0.1:1", Model: "qwen"}, t.TempDir())
	if !strings.Contains(cfg, "\nsnapshots = true\n") || strings.Contains(cfg, "snapshots = false") {
		t.Error("the arm's config doesn't turn snapshots on")
	}
}

// TestSummarizeTackReadsTheTestReport: done's answer and its report are separate fields, whichever answer it is, and a
// refusal is neither.
func TestSummarizeTackReadsTheTestReport(t *testing.T) {
	for _, answer := range []string{"done", loop.DoneUnverified, loop.DoneVerifyFailed, loop.DoneNoTests} {
		result, _ := json.Marshal(answer + "\n\n[tack: what this turn did to the tests:\n- deleted a_test.go\n- added a skip to b_test.go\n]")
		p := filepath.Join(t.TempDir(), "events.jsonl")
		lines := []string{
			`{"seq":3,"time":"2026-10-17T12:00:01Z","type":"tool.call","cause":2,"payload":{"id":"a","name":"done","input":{"reason":"x"}}}`,
			`{"seq":4,"time":"2026-10-17T12:00:01Z","type":"tool.finished","cause":3,"payload":{"id":"a","outcome":"ok","result":` + string(result) + `,"truncated":false}}`,
		}
		os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
		s := SummarizeTack(p)
		if s.Done != answer || len(s.TestChanges) != 2 || s.TestChanges[1] != "added a skip to b_test.go" {
			t.Errorf("%q: %q %q", answer, s.Done, s.TestChanges)
		}
	}
}
