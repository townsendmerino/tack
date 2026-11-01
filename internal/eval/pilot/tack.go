package pilot

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
// reach, so the cap otherwise limits the run, as it does opencode's. max_tokens is opencode's reply limit. settings are
// [run] settings switched on for the arm, each one of RunSettings.
// Sandboxed commands may also read ModelsDir, so a test that loads a checkpoint runs as it would for the owner; the
// rest of the home directory, the mirrors and other runs' records stay out of their reach (DESIGN §6.6).
func tackConfig(s Server, out string, settings ...string) string {
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

[sandbox]
read = [%q]

[run]
max_wall = "24h"
max_requests = 1000000
max_tool_calls = 1000000
max_tokens = %d
turn_tokens = 1000000000
max_errors = 8
session_tokens = 1000000000
session_tool_calls = 1000000
`, filepath.Join(out, "state"), s.BaseURL, s.Model, filepath.Join(out, "token"), f("temperature"), f("top_p"), f("top_k"), ModelsDir(), replyLimit) + runLines(settings)
}

// RunSettings are the boolean [run] settings an A1 arm may switch on, each a loop change under the calibration's C6
// (e1-calibration-preregistration-2026-10-18.md §12). A name outside it is refused, so an arm cannot carry a typo
// into a measurement.
var RunSettings = map[string]bool{
	"fit_window":             true, // §4.6
	"stop_repeats":           true, // §4.5
	"mask_observations":      true, // §15.2, C9's observation masking
	"result_notes":           true, // §4.4
	"edit_messages":          true, // §4.1
	"no_instructions":        true, // §4.8
	"instructions_head":      true, // §12.12
	"refuse_duplicate_edits": true, // §12.14
	"verify_done":            true, // §4.2, §12.15
	"build_after_edit":       true, // §12.17
	"restate_task":           true, // a C6 candidate (e1-calibration-c6-status-2026-10-21.md), not yet registered
	"edit_lines":             true, // §4.9, a candidate, not yet registered
	"current_symbol":         true, // §12.20, a candidate
	"edit_tolerances":        true, // §12.22, judged by replay
	"inject_context":         true, // §13.1, C7's turn-1 injection
	"repo_map":               true, // §13.3, C7's generated repository map
	"best_of_3":              true, // §15.3, C9's best of three, written as best_of = 3 (runValues)
}

// runLines is the config lines that switch settings on, under the [run] table tackConfig ends with.
// runValues are the arm settings that set a [run] key to a value other than true.
var runValues = map[string]string{"best_of_3": "best_of = 3"}

func runLines(settings []string) string {
	var b strings.Builder
	for _, s := range settings {
		if v, ok := runValues[s]; ok {
			b.WriteString(v + "\n")
			continue
		}
		fmt.Fprintf(&b, "%s = true\n", s)
	}
	return b.String()
}

// PreflightCommand is what A1 runs in the sandbox, in the task's repository, before the model starts (`tack run
// --preflight`): every package the repository's builds and tests need must load from the module cache, since the
// sandbox can neither download a module nor write the cache. If it fails the run is void, not the model's failure
// (owner, 2026-10-18).
const PreflightCommand = "go list -deps -test ./... >/dev/null && go version"

// DownloadModules fills the module cache with what repo's packages and tests need (`go mod download`), outside the
// sandbox and before the run's clock starts, writing go's output to log.
func DownloadModules(ctx context.Context, repo string, log io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "mod", "download")
	cmd.Dir, cmd.Stdout, cmd.Stderr = repo, log, log
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go mod download: %w", err)
	}
	return nil
}

// RunTack runs `tack run --edit --instructions whole --json` once in repo with the statement, under cap, after
// PreflightCommand. tack's own files, the config, the API key (mode 0600) and its state, go in TackDir(out): tack
// refuses to run with them inside the workspace or above it. Its event stream (events.jsonl) and its stderr go in out.
// settings are the [run] settings switched on in its config (RunSettings). A failed preflight sets Void. At the cap
// tack is sent SIGTERM, so it cancels its turn and its shell kills what each command started, then SIGKILL after 10 s.
func RunTack(ctx context.Context, tack string, s Server, repo, statement, out string, cap time.Duration, settings ...string) Result {
	r := Result{Arm: "A1", Exit: -1}
	own := TackDir(out)
	err := errors.Join(os.MkdirAll(out, 0o755), os.MkdirAll(own, 0o755))
	if err == nil {
		err = os.WriteFile(filepath.Join(own, "token"), []byte(s.APIKey+"\n"), 0o600)
	}
	if err == nil {
		err = os.WriteFile(filepath.Join(own, "config.toml"), []byte(tackConfig(s, own, settings...)), 0o600)
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
	cmd := exec.Command(tack, "run", "--config", filepath.Join(own, "config.toml"), "--workspace", repo, "--edit",
		"--preflight", PreflightCommand, "--instructions", "whole", "--json", statement)
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
	if r.Exit == exitPreflight {
		r.Void = preflightError(filepath.Join(out, "stderr.log"))
	}
	return r
}

// exitPreflight is tack run's exit status when its --preflight command failed.
const exitPreflight = 8

// TackDir is where an A1 run keeps tack's own files: a sibling of out, which holds the repository.
func TackDir(out string) string { return out + "-tack" }

// preflightError is tack's line saying why the preflight failed, from its stderr.
func preflightError(stderr string) string {
	b, _ := os.ReadFile(stderr)
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "tack run: preflight") {
			return l
		}
	}
	return "tack run exited 8 (preflight failed) without saying why"
}

// TackSummary is what an A1 run's events.jsonl says: tool calls by name and how many of them failed, the requests
// sent, how the turn ended, and how many times done was refused for an unchanged workspace (loop.DoneNoChange, R13's
// loop mitigation).
type TackSummary struct {
	ToolCalls   map[string]int `json:"tool_calls"`
	Failed      map[string]int `json:"failed,omitempty"` // tool calls whose outcome was not ok, by name
	Requests    int            `json:"requests"`
	StopReason  string         `json:"stop_reason"`
	DoneRefused int            `json:"done_refused"`
	// VerifyRefused counts done refused for want of a verification (loop.VerifyRefusal), and Done is the answer of the done
	// that ended the run: "done", loop.DoneUnverified or loop.DoneVerifyFailed; "" when no done ended it.
	VerifyRefused int      `json:"verify_refused,omitempty"`
	Done          string   `json:"done,omitempty"`
	Resends       int      `json:"resends"`          // requests sent again after the server refused one for carrying no tool call
	Budget        string   `json:"budget,omitempty"` // the budget that tripped, if any
	Errors        []string `json:"errors,omitempty"`
}

// SummarizeTack reads tack's event stream for an A1 run.
func SummarizeTack(path string) TackSummary {
	s := TackSummary{ToolCalls: map[string]int{}, Failed: map[string]int{}}
	names := map[string]string{} // tool call id to name
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
			names[p.ID] = p.Name
		case proto.RequestSent:
			s.Requests++
		case proto.ToolFinished:
			if loop.IsDoneNoChange(p.Result) {
				s.DoneRefused++
			}
			if strings.HasPrefix(p.Result, loop.VerifyRefusal) {
				s.VerifyRefused++
			}
			if names[p.ID] == "done" && (p.Result == "done" || p.Result == loop.DoneUnverified || p.Result == loop.DoneVerifyFailed) {
				s.Done = p.Result
			}
			if p.Outcome != proto.OutcomeOK {
				s.Failed[names[p.ID]]++
			}
		case proto.TurnEnd:
			s.StopReason = p.StopReason
		case proto.BudgetExceeded:
			s.Budget = p.Budget
		case proto.ErrorEvent:
			if p.Class == loop.NoToolCallClass {
				s.Resends++
				continue
			}
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

// ModelsDir is ~/models on the machine running tack, where tests read checkpoints from (CLAUDE.md: model files are
// read from ~/models, never the archive).
func ModelsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "/nonexistent-models"
	}
	return filepath.Join(home, "models")
}

// systemPrefixes are paths outside a run's repository that a tool call may name without leaving it in the sense
// left_repo means: the system and toolchain paths every build reads (internal/sandbox's section read), such as
// 2>/dev/null or /usr/bin/grep.
var systemPrefixes = []string{"/dev/", "/usr/", "/bin/", "/sbin/", "/System/", "/Library/", "/opt/homebrew/",
	"/private/etc/", "/etc/", "/Applications/Xcode.app/"}

// TackLeftRepo is left_repo for an A1 run: each path outside repo that a tool call's input names, from tack's event
// stream, as EventSummary.LeftRepo is for opencode's, leaving out systemPrefixes. The sandbox denies reading outside
// the workspace (DESIGN §6.6); this records the attempts.
func TackLeftRepo(path, repo string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	root, plain := resolved(repo), filepath.Clean(repo) // a call may name the repository either way (/tmp is /private/tmp)
	var out []string
	seen := map[string]bool{}
	var walk func(v any)
	walk = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			for _, e := range t {
				walk(e)
			}
		case []any:
			for _, e := range t {
				walk(e)
			}
		case string:
			for _, m := range pathLike.FindAllStringSubmatch(t, -1) {
				p := outside(m[0], root)
				if p == "" || outside(m[0], plain) == "" || seen[p] || system(p) {
					continue
				}
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for sc.Scan() {
		var ev proto.Event
		if json.Unmarshal(sc.Bytes(), &ev) != nil || ev.Type != proto.EvToolCall {
			continue
		}
		if v, err := ev.Decode(); err == nil {
			if c, ok := v.(proto.ToolCall); ok {
				var in any
				if json.Unmarshal(c.Input, &in) == nil {
					walk(in)
				}
			}
		}
	}
	return out
}

func system(p string) bool {
	for _, s := range systemPrefixes {
		if strings.HasPrefix(p, s) || p+"/" == s {
			return true
		}
	}
	return false
}
