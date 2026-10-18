package pilot

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/townsendmerino/tack/internal/loop"
	"github.com/townsendmerino/tack/internal/proto"
)

// A1 is tack's own arm (pilot pre-registration, amendment 9): `tack run --edit` in the repository instead of opencode.

// tackConfig is the config.toml an A1 run gets: the pilot's server through the capture and the registered Sampling.
// tack's consecutive-failure budget is its design's, 8 (owner, after the task-7 smoke); every other budget is out of
// reach, so the cap otherwise limits the run, as it does opencode's. max_tokens is opencode's reply limit.
func tackConfig(s Server, out string) string {
	f := func(k string) any { return Sampling[k] }
	return fmt.Sprintf(`main = "pilot"
state_dir = %q

[servers.pilot]
kind = "goinfer-remote"
url = %q
model = %q
token_file = %q
temperature = %v
top_p = %v
top_k = %v

[run]
max_wall = "24h"
max_requests = 1000000
max_tool_calls = 1000000
max_tokens = %d
turn_tokens = 1000000000
max_errors = 8
session_tokens = 1000000000
session_tool_calls = 1000000
`, filepath.Join(out, "state"), s.BaseURL, s.Model, filepath.Join(out, "token"), f("temperature"), f("top_p"), f("top_k"), replyLimit)
}

// RunTack runs `tack run --edit --instructions whole --json` once in repo with the statement, under cap. The config,
// the API key (mode 0600), tack's state, its event stream (events.jsonl) and its stderr go in out. At the cap tack is
// sent SIGTERM, so it cancels its turn and its shell kills what each command started, then SIGKILL after 10 s.
func RunTack(ctx context.Context, tack string, s Server, repo, statement, out string, cap time.Duration) Result {
	r := Result{Arm: "A1", Exit: -1}
	err := os.MkdirAll(out, 0o755)
	if err == nil {
		err = os.WriteFile(filepath.Join(out, "token"), []byte(s.APIKey+"\n"), 0o600)
	}
	if err == nil {
		err = os.WriteFile(filepath.Join(out, "config.toml"), []byte(tackConfig(s, out)), 0o600)
	}
	if err != nil {
		r.Err = err.Error()
		return r
	}
	events, err1 := os.Create(filepath.Join(out, "events.jsonl"))
	stderr, err2 := os.Create(filepath.Join(out, "stderr.log"))
	if err := errors.Join(err1, err2); err != nil {
		r.Err = err.Error()
		return r
	}
	defer events.Close()
	defer stderr.Close()
	cmd := exec.Command(tack, "run", "--config", filepath.Join(out, "config.toml"), "--workspace", repo, "--edit",
		"--instructions", "whole", "--json", statement)
	cmd.Dir = repo
	cmd.Stdout, cmd.Stderr = events, stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	start := time.Now()
	if err := cmd.Start(); err != nil {
		r.Err = err.Error()
		return r
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	stop := func() error {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		select {
		case err := <-done:
			return err
		case <-time.After(10 * time.Second):
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			return <-done
		}
	}
	timer := time.NewTimer(cap)
	defer timer.Stop()
	select {
	case err = <-done:
	case <-timer.C:
		r.CapHit = true
		err = stop()
	case <-ctx.Done():
		err = stop()
		r.Err = ctx.Err().Error()
	}
	r.WallSecs = time.Since(start).Seconds()
	var ee *exec.ExitError
	switch {
	case r.CapHit:
	case err == nil:
		r.Exit = 0
	case errors.As(err, &ee):
		r.Exit = ee.ExitCode()
	default:
		r.Err = err.Error()
	}
	return r
}

// TackSummary is what an A1 run's events.jsonl says: tool calls by name, the requests sent, how the turn ended, and
// how many times done was refused for an unchanged workspace (loop.DoneNoChange, R13's loop mitigation).
type TackSummary struct {
	ToolCalls   map[string]int `json:"tool_calls"`
	Requests    int            `json:"requests"`
	StopReason  string         `json:"stop_reason"`
	DoneRefused int            `json:"done_refused"`
	Budget      string         `json:"budget,omitempty"` // the budget that tripped, if any
	Errors      []string       `json:"errors,omitempty"`
}

// SummarizeTack reads tack's event stream for an A1 run.
func SummarizeTack(path string) TackSummary {
	s := TackSummary{ToolCalls: map[string]int{}}
	f, err := os.Open(path)
	if err != nil {
		return s
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for sc.Scan() {
		var ev proto.Event
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		v, err := ev.Decode()
		if err != nil {
			continue
		}
		switch p := v.(type) {
		case proto.ToolCall:
			s.ToolCalls[p.Name]++
		case proto.RequestSent:
			s.Requests++
		case proto.ToolFinished:
			if p.Result == loop.DoneNoChange {
				s.DoneRefused++
			}
		case proto.TurnEnd:
			s.StopReason = p.StopReason
		case proto.BudgetExceeded:
			s.Budget = p.Budget
		case proto.ErrorEvent:
			s.Errors = append(s.Errors, p.Class+": "+p.Message)
		}
	}
	return s
}

// Timed is one model request with the time it took on both sides: goinfer's own figures from its request log, and the
// client's from the capture. TailnetMs is the client's total less goinfer's: the time between the two machines.
type Timed struct {
	Prompt, Prefilled           int
	ServerTTFTms, ServerTotalMs int
	ClientTTFBms, ClientTotalMs int
	TailnetMs                   int
}

// PairTimings pairs a run's goinfer request lines with its captured requests' timings, in order. Only model requests
// are paired: the capture's POSTs to /v1/messages and /v1/chat/completions, not count_tokens or model listings. A
// count mismatch pairs nothing and says why, rather than misaligning the two.
func PairTimings(reqs []Request, requestsDir string) ([]Timed, error) {
	metas, _ := filepath.Glob(filepath.Join(requestsDir, "*.meta"))
	sort.Strings(metas)
	var client []CaptureTiming
	for _, m := range metas {
		b, err := os.ReadFile(m)
		if err != nil {
			continue
		}
		f := strings.Fields(string(b))
		if len(f) < 2 || f[0] != "POST" || (f[1] != "/v1/messages" && f[1] != "/v1/chat/completions") {
			continue
		}
		if t, ok := ReadTiming(m); ok {
			client = append(client, t)
		}
	}
	var model []Request
	for _, r := range reqs {
		if strings.Contains(r.Raw, "POST /v1/messages ") || strings.Contains(r.Raw, "POST /v1/chat/completions ") {
			model = append(model, r)
		}
	}
	if len(client) != len(model) {
		return nil, fmt.Errorf("%d model requests captured, %d in goinfer's log: not paired", len(client), len(model))
	}
	out := make([]Timed, len(model))
	for i, r := range model {
		c := client[i]
		out[i] = Timed{Prompt: r.Prompt, Prefilled: r.Prompt - r.Reused, ServerTTFTms: r.TTFTms, ServerTotalMs: r.Totalms,
			ClientTTFBms: c.TTFBms, ClientTotalMs: c.TotalMs, TailnetMs: c.TotalMs - r.Totalms}
	}
	return out, nil
}

// jsonCallForms are the chat-template families whose tool calls goinfer constrains with a grammar from the first token
// under a forced tool call (goinfer 33532c3b's note: chatml, mellum2, mistral, llama3).
var jsonCallForms = map[string]bool{"chatml": true, "mellum2": true, "mistral": true, "llama3": true}

// ServerFeatures reads goinfer-serve's startup banner from its log: the "features:" line, verbatim, and the forced
// tool-call enforcement mode. The mode is a banner line naming forced calls if there is one. Otherwise, for a server
// that predates goinfer's report (ForcedToolCall), it is derived from the loaded model's chat template, "[chat: X" on
// its "loaded" line: a family with a JSON call form gives "grammar (derived from the X template; build predates the
// field)" (owner, 2026-10-17, amendment 9). Any other template is "not reported": nothing else is assumed.
func ServerFeatures(serveLog string) (features, forcedCallMode string) {
	f, err := os.Open(serveLog)
	if err != nil {
		return "", "not reported"
	}
	defer f.Close()
	template := ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "request:") {
			break // the banner is over
		}
		if rest, ok := strings.CutPrefix(line, "features:"); ok && features == "" {
			features = strings.TrimSpace(rest)
		}
		if _, rest, ok := strings.Cut(line, "[chat: "); ok && template == "" && strings.HasPrefix(line, "loaded ") {
			template, _, _ = strings.Cut(rest, ",")
			template = strings.TrimSuffix(strings.TrimSpace(template), "]")
		}
		low := strings.ToLower(line)
		if i := strings.Index(low, "forced call"); i >= 0 {
			for _, mode := range []string{"grammar", "post-check", "off"} {
				if strings.Contains(low[i:], mode) {
					forcedCallMode = mode
					break
				}
			}
		}
	}
	switch {
	case forcedCallMode != "":
	case jsonCallForms[template]:
		forcedCallMode = "grammar (derived from the " + template + " template; build predates the field)"
	default:
		forcedCallMode = "not reported"
	}
	return features, forcedCallMode
}

// ForcedToolCall asks the server how a forced tool call (tool_choice any or required) is enforced on model: "grammar",
// "post-check" or "off", from the model's forced_tool_call in /v1/models (goinfer 33532c3b and later). It returns ""
// when the server does not report it (an older goinfer, or a model whose template takes no tools).
func ForcedToolCall(ctx context.Context, baseURL, key, model string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/v1/models", nil)
	if err != nil {
		return ""
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var list struct {
		Data []struct {
			ID             string `json:"id"`
			ForcedToolCall string `json:"forced_tool_call"`
		} `json:"data"`
	}
	if json.NewDecoder(resp.Body).Decode(&list) != nil {
		return ""
	}
	for _, m := range list.Data {
		if m.ID == model {
			return m.ForcedToolCall
		}
	}
	return ""
}
