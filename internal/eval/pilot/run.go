package pilot

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/townsendmerino/tack/internal/eval/mine"
)

// Task is a pilot task: a verified, chosen candidate whose statement the owner has approved.
type Task struct {
	N int // 1-based position in pilot.jsonl
	mine.Candidate
}

// LoadTasks reads pilot.jsonl and returns tasks from..to (1-based line positions, inclusive). A line marked removed is
// skipped, and the others keep their positions. Every other task must be verified and carry an approved statement
// (pilot pre-registration, "Tasks"); one that does not is an error, so no draft is ever run.
func LoadTasks(path string, from, to int) ([]Task, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Task
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for n := 1; sc.Scan(); n++ {
		if n < from || n > to {
			continue
		}
		var c mine.Candidate
		if err := json.Unmarshal(sc.Bytes(), &c); err != nil {
			return nil, fmt.Errorf("%s line %d: %w", path, n, err)
		}
		switch {
		case c.Removed != "":
			continue
		case c.Verified == nil || !c.Verified.OK:
			return nil, fmt.Errorf("task %d (%s %s) is not verified", n, c.Repo, c.Commit[:8])
		case c.Statement == "" || c.ReviewedBy == "":
			return nil, fmt.Errorf("task %d (%s %s) has no approved statement", n, c.Repo, c.Commit[:8])
		}
		out = append(out, Task{N: n, Candidate: c})
	}
	return out, sc.Err()
}

// ArmOrder is the order a task's arms run in: A0 first on odd-numbered tasks, A0k first on even ones, so neither arm
// always meets a cold server.
func ArmOrder(n int, a0, a0k Arm) []Arm {
	if n%2 == 1 {
		return []Arm{a0, a0k}
	}
	return []Arm{a0k, a0}
}

// OracleResult is one oracle run. Tests maps each oracle test to pass, fail, skip or build-fail; Output is go test's
// full output, standard output then standard error; Exit is its exit error, empty when it exited 0.
type OracleResult struct {
	Pass   bool
	Tests  map[string]string
	Output []byte
	Exit   string
}

// Oracle copies the commit's named-test files from the mirror into repo and runs the oracle tests as the miner
// verified them. Every oracle test must report pass; a skip or a build failure fails the task. The error is for a
// failure to set the oracle up; a test that fails is in the result.
func Oracle(ctx context.Context, mirror string, t Task, repo string) (OracleResult, error) {
	for _, f := range t.TestFiles {
		src, err := exec.Command("git", "-C", mirror, "show", t.Commit+":"+f).Output()
		if err != nil {
			return OracleResult{}, fmt.Errorf("read %s at %s: %w", f, t.Commit[:8], err)
		}
		p := filepath.Join(repo, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return OracleResult{}, err
		}
		if err := os.WriteFile(p, src, 0o644); err != nil {
			return OracleResult{}, err
		}
	}
	res, _, out, runErr := mine.RunTestsOutput(ctx, repo, t.Verified.Oracle, t.Packages)
	r := OracleResult{Tests: res, Output: out, Pass: len(t.Verified.Oracle) > 0}
	if runErr != nil {
		r.Exit = runErr.Error()
	}
	for _, name := range t.Verified.Oracle {
		r.Pass = r.Pass && res[name] == "pass"
	}
	return r, nil
}

// Request is one line of goinfer-serve's -log-requests output. Reused counts every prompt row the request did not
// prefill, Snap those of them a host snapshot restored. Fast marks a request on the -fast alias, which opencode's
// small_model (its title call) uses; the task's conversation is on the plain alias.
type Request struct {
	Prompt, Completion, Reused int
	TTFTms, Totalms            int
	Snap                       int
	Fast                       bool
	Raw                        string
}

var reqField = regexp.MustCompile(`(prompt_tokens|completion_tokens|reused|ttft|total|snap|fast)=(\d+)`)

// PromptStats is what one run's requests say about its prompt (pilot pre-registration, amendments 2 and 3).
type PromptStats struct {
	// FirstPrompt is the first plain-alias request's prompt: the arm's fixed prompt (system prompt, instruction files,
	// tool schemas) plus the statement.
	FirstPrompt int `json:"first_prompt_tokens"`
	// Statement is the statement's own tokens: the server's count of it as a lone user message, less the count of a
	// one-character message, so the chat template's framing cancels to within a token.
	Statement int `json:"statement_tokens,omitempty"`
	// Fixed is FirstPrompt less Statement, when both are known: the arm's fixed-prompt token count.
	Fixed int `json:"fixed_prompt_tokens,omitempty"`
	// Instructions counts, as Statement is counted, the instruction file opencode puts in its system prompt. Own is Fixed less their sum: opencode's own system prompt and tool schemas, plus ken-mcp's
	// for A0k, which is what compares with tack's fixed prompt.
	Instructions map[string]int `json:"instruction_tokens,omitempty"`
	Own          int            `json:"own_prompt_tokens,omitempty"`
	// Main and FastRequests count the plain-alias and -fast requests.
	Main         int `json:"main_requests"`
	FastRequests int `json:"fast_requests"`
	// Reprefills counts plain-alias requests after the first that reused less than half of the previous plain-alias
	// request's prompt: the conversation lost its cached prefix and was prefilled again. SnapRestores counts those
	// whose reuse came in part from a host snapshot, which is a copy, not a prefill.
	Reprefills   int `json:"reprefills"`
	SnapRestores int `json:"snap_restores"`
}

// Prompts computes a run's PromptStats from its requests; statement is the statement's token count, or 0 if unknown.
func Prompts(reqs []Request, statement int) PromptStats {
	s := PromptStats{Statement: statement}
	prev := 0
	for _, r := range reqs {
		if r.Fast {
			s.FastRequests++
			continue
		}
		if r.Prompt == 0 { // refused before it was tokenized
			continue
		}
		s.Main++
		if s.Main == 1 {
			s.FirstPrompt = r.Prompt
		} else if 2*r.Reused < prev {
			s.Reprefills++
		}
		if r.Snap > 0 {
			s.SnapRestores++
		}
		prev = r.Prompt
	}
	if s.FirstPrompt > 0 && statement > 0 {
		s.Fixed = s.FirstPrompt - statement
	}
	return s
}

// instructionFiles are the project instruction files opencode 1.19.0 looks for, in its order; it reads the first one found
// (its Instruction.systemPaths). The run's directory is the worktree root, so the root is the only place it looks.
var instructionFiles = []string{"AGENTS.md", "CLAUDE.md", "CONTEXT.md"}

// WithInstructions sets s.Instructions from the instruction file opencode reads in repo and, when its count is known,
// s.Own.
func (s PromptStats) WithInstructions(ctx context.Context, count func(context.Context, string) (int, error), repo string) PromptStats {
	sum, known := 0, count != nil
	for _, name := range instructionFiles {
		b, err := os.ReadFile(filepath.Join(repo, name))
		if err != nil {
			continue
		}
		n := StatementTokens(ctx, count, string(b))
		if n == 0 {
			known = false
			break
		}
		if s.Instructions == nil {
			s.Instructions = map[string]int{}
		}
		s.Instructions[name] = n
		sum += n
		break
	}
	if known && s.Fixed > 0 {
		s.Own = s.Fixed - sum
	}
	return s
}

// Requests reads the request lines goinfer-serve logged between two offsets of its log: one run's window.
func Requests(log string, from, to int64) []Request {
	f, err := os.Open(log)
	if err != nil {
		return nil
	}
	defer f.Close()
	if _, err := f.Seek(from, 0); err != nil {
		return nil
	}
	var out []Request
	sc := bufio.NewScanner(&limited{f: f, n: to - from})
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "request:") {
			continue
		}
		r := Request{Raw: line}
		for _, m := range reqField.FindAllStringSubmatch(line, -1) {
			var v int
			fmt.Sscan(m[2], &v)
			switch m[1] {
			case "prompt_tokens":
				r.Prompt = v
			case "completion_tokens":
				r.Completion = v
			case "reused":
				r.Reused = v
			case "ttft":
				r.TTFTms = v
			case "total":
				r.Totalms = v
			case "snap":
				r.Snap = v
			case "fast":
				r.Fast = v == 1
			}
		}
		out = append(out, r)
	}
	return out
}

type limited struct {
	f *os.File
	n int64
}

func (l *limited) Read(p []byte) (int, error) {
	if l.n <= 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > l.n {
		p = p[:l.n]
	}
	k, err := l.f.Read(p)
	l.n -= int64(k)
	return k, err
}

// size is a file's length, 0 if it cannot be read.
func size(path string) int64 {
	st, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return st.Size()
}

// EventSummary is what the run record takes from opencode's JSON events. It depends on the event schema only this far:
// any object with a string "tool" field counts as a tool call, and any string under a call's arguments (a value keyed
// "input", where opencode keeps them, or "arguments") that names a path outside the repository (an absolute path, or
// one climbing out with ..) marks the run left_repo. A tool's output is not scanned: file contents and search results
// name paths the call did not touch. Over-flagging the arguments is the safe direction: a flagged run is reported both
// counted and excluded (pilot pre-registration, "Recorded per run").
type EventSummary struct {
	Events    int            `json:"events"`
	ToolCalls map[string]int `json:"tool_calls"`
	LeftRepo  []string       `json:"left_repo,omitempty"`
}

// SummarizeEvents reads opencode's events.jsonl for a run in repo.
func SummarizeEvents(path, repo string) EventSummary {
	s := EventSummary{ToolCalls: map[string]int{}}
	f, err := os.Open(path)
	if err != nil {
		return s
	}
	defer f.Close()
	root := resolved(repo)
	seen := map[string]bool{}
	var walk func(v any, args bool)
	walk = func(v any, args bool) { // args: v is inside a tool call's arguments
		switch t := v.(type) {
		case map[string]any:
			if name, ok := t["tool"].(string); ok && name != "" {
				s.ToolCalls[name]++
			}
			for k, e := range t {
				walk(e, args || k == "input" || k == "arguments")
			}
		case []any:
			for _, e := range t {
				walk(e, args)
			}
		case string:
			if !args {
				return
			}
			if p := outside(t, root); p != "" && !seen[p] {
				seen[p] = true
				s.LeftRepo = append(s.LeftRepo, p)
			}
		}
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for sc.Scan() {
		var v any
		if json.Unmarshal(sc.Bytes(), &v) == nil {
			s.Events++
			walk(v, false)
		}
	}
	return s
}

// pathLike matches an absolute path or a relative one that climbs with "..", as a whole string or a word inside one.
var pathLike = regexp.MustCompile(`(?:^|[\s"'=(])((?:/[\w.@+-]+){2,}/?|(?:\.\./)+[\w./@+-]*)`)

// outside returns the first path in s that lies outside root, or "". ken's index (root/.ken) is inside root, so it is
// never flagged.
func outside(s, root string) string {
	for _, m := range pathLike.FindAllStringSubmatch(s, -1) {
		p := m[1]
		if !filepath.IsAbs(p) {
			p = filepath.Join(root, p)
		}
		p = filepath.Clean(p)
		if p == root || strings.HasPrefix(p, root+string(filepath.Separator)) {
			continue
		}
		return p
	}
	return ""
}

// Options configures a pilot job: a range of tasks, both arms each.
type Options struct {
	Tasks    []Task
	Mirrors  string // <repo>.git bare mirrors
	Opencode string
	A0, A0k  Arm
	// A1, when set, is tack's binary: the job runs tack's arm alone, `tack run --edit` (amendment 9), instead of A0
	// and A0k.
	A1 string
	// ForcedCallMode is how the server enforces a forced tool call on the model; RunPilot asks the server for it
	// (ForcedToolCall) when it is empty, and each record falls back to the banner (ServerFeatures) when neither knows.
	ForcedCallMode string
	Server         Server // filled by the caller once the server is up
	ServeLog       string // the server's log, for each run's request window
	Out            string
	Cap            time.Duration
	KenWarmup      time.Duration // the warm-up's own limit, outside the clock
	Log            io.Writer
	// CountTokens counts a lone user message on the server, for each run's statement_tokens; nil leaves them unknown.
	CountTokens func(ctx context.Context, text string) (int, error)
	TackRev     string // the tack commit the runner was built from, recorded with every run
	// Capture records every request and response raw, per run, under the run's requests/ directory; nil records none.
	// With it, the job's first run must show the registered Sampling reaching goinfer, or the job stops.
	Capture *Capture
}

// Record is one run's line in results.jsonl.
type Record struct {
	Tack       string            `json:"tack"`
	Task       int               `json:"task"`
	Repo       string            `json:"repo"`
	Commit     string            `json:"commit"`
	Base       string            `json:"base"`
	Run        Result            `json:"run"`
	CappedSecs float64           `json:"capped_secs"` // the wall time, or the full cap for a failed run (PAR-1)
	Pass       bool              `json:"pass"`
	Oracle     map[string]string `json:"oracle"`
	OracleErr  string            `json:"oracle_err,omitempty"`
	// OracleExit is go test's exit error under the oracle; its full output is the run's oracle.log.
	OracleExit string `json:"oracle_exit,omitempty"`
	// TackRun, Timed and TimingErr are A1's: tack's own event summary, and each model request's time on both sides of
	// the tailnet (PairTimings), or why they could not be paired.
	TackRun   *TackSummary `json:"tack_run,omitempty"`
	Timed     []Timed      `json:"timed,omitempty"`
	TimingErr string       `json:"timing_err,omitempty"`
	// ServerFeatures and ForcedCallMode are goinfer's own report of the server, from its banner (ServerFeatures).
	ServerFeatures string         `json:"server_features,omitempty"`
	ForcedCallMode string         `json:"forced_call_mode,omitempty"`
	KenIndexSecs   float64        `json:"ken_index_secs,omitempty"` // A0k's untimed warm-up
	Requests       []Request      `json:"requests"`
	Prompt         PromptStats    `json:"prompt"`
	Events         EventSummary   `json:"events"`
	DiffBytes      int            `json:"diff_bytes"`
	Sampling       map[string]any `json:"sampling"`                    // what opencode was configured to send
	Captured       int            `json:"captured_requests,omitempty"` // requests recorded raw in requests/
	At             string         `json:"at"`
}

// RunPilot runs every task's arms in order and appends a Record per run to Out/results.jsonl. A failure to set up a
// run (the repository, the warm-up) is recorded as that run's error and the job goes on.
func RunPilot(ctx context.Context, o Options) error {
	rf, err := os.OpenFile(filepath.Join(o.Out, "results.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer rf.Close()
	enc := json.NewEncoder(rf)
	start := time.Now()
	if o.ForcedCallMode == "" && o.Server.Direct != "" {
		o.ForcedCallMode = ForcedToolCall(ctx, o.Server.Direct, o.Server.APIKey, o.Server.Model)
	}
	total := len(o.Tasks) * 2
	done := 0
	logf := func(format string, args ...any) {
		fmt.Fprintf(o.Log, time.Now().Format("15:04:05")+" pilot: "+format+"\n", args...)
	}
	logf("start %s, %d tasks x 2 arms, cap %s per run", start.Format("15:04 MST"), len(o.Tasks), o.Cap)
	for _, t := range o.Tasks {
		arms := ArmOrder(t.N, o.A0, o.A0k)
		if o.A1 != "" {
			arms = []Arm{{Name: "A1"}}
		}
		for _, arm := range arms {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			done++
			rec := runOne(ctx, o, t, arm)
			if err := enc.Encode(rec); err != nil {
				return err
			}
			if done == 1 && o.Capture != nil { // amendment 6: the sampling must reach goinfer, or nothing after this measures it
				if err := CheckSampling(filepath.Join(o.Out, fmt.Sprintf("task-%02d", t.N), arm.Name, "requests")); err != nil {
					logf("stopping the job: %v", err)
					return err
				}
				logf("the first request carries the registered sampling: %v", Sampling)
			}
			logf("%d/%d task %d %s %s: pass=%v capped %.0f s, cap hit %v, %d requests (started %s, elapsed %s)",
				done, total, t.N, t.Repo, arm.Name, rec.Pass, rec.CappedSecs, rec.Run.CapHit, len(rec.Requests),
				start.Format("15:04"), time.Since(start).Round(time.Second))
		}
	}
	logf("done in %s", time.Since(start).Round(time.Second))
	return nil
}

func runOne(ctx context.Context, o Options, t Task, arm Arm) Record {
	rec := Record{Tack: o.TackRev, Sampling: Sampling, Task: t.N, Repo: t.Repo, Commit: t.Commit, Base: t.Base, At: time.Now().UTC().Format(time.RFC3339)}
	rec.Run = Result{Arm: arm.Name, Exit: -1}
	dir := filepath.Join(o.Out, fmt.Sprintf("task-%02d", t.N), arm.Name)
	repo := filepath.Join(dir, "repo")
	mirror := filepath.Join(o.Mirrors, t.Repo+".git")
	fail := func(err error) Record {
		rec.Run.Err = err.Error()
		rec.CappedSecs = o.Cap.Seconds()
		return rec
	}
	if err := PrepareRepo(mirror, t.Base, repo); err != nil {
		return fail(fmt.Errorf("prepare: %w", err))
	}
	if arm.KenMCP != "" { // untimed (pilot pre-registration, amendment)
		kl, _ := os.Create(filepath.Join(dir, "ken-warmup.log"))
		secs, err := WarmKen(ctx, arm.KenMCP, arm.KenModel, repo, kl, o.KenWarmup)
		kl.Close()
		if err != nil {
			return fail(fmt.Errorf("ken warm-up: %w", err))
		}
		rec.KenIndexSecs = secs
	}
	stmt := StatementTokens(ctx, o.CountTokens, t.Statement) // before the window, so its request is not the run's
	from := size(o.ServeLog)
	if o.Capture != nil {
		o.Capture.SetDir(filepath.Join(dir, "requests"))
	}
	if o.A1 != "" {
		rec.Run = RunTack(ctx, o.A1, o.Server, repo, t.Statement, dir, o.Cap)
	} else {
		rec.Run = RunOpencode(ctx, o.Opencode, o.Server, arm, repo, t.Statement, dir, o.Cap)
	}
	if o.Capture != nil {
		rec.Captured = o.Capture.Count()
		o.Capture.SetDir("")
	}
	rec.Requests = Requests(o.ServeLog, from, size(o.ServeLog))
	rec.ServerFeatures, rec.ForcedCallMode = ServerFeatures(o.ServeLog)
	if o.ForcedCallMode != "" { // the server's own answer, from /v1/models, over the banner's
		rec.ForcedCallMode = o.ForcedCallMode
	}
	if o.A1 != "" {
		// A remote server's log arrives over ssh: wait, briefly, for its lines to catch up with the capture.
		reqDir := filepath.Join(dir, "requests")
		for i := 0; i < 50; i++ {
			if timed, err := PairTimings(rec.Requests, reqDir); err == nil {
				rec.Timed = timed
				break
			}
			time.Sleep(200 * time.Millisecond)
			rec.Requests = Requests(o.ServeLog, from, size(o.ServeLog))
		}
		if rec.Timed == nil {
			_, err := PairTimings(rec.Requests, reqDir)
			rec.TimingErr = fmt.Sprint(err)
		}
		tack := SummarizeTack(filepath.Join(dir, "events.jsonl"))
		rec.TackRun = &tack
		rec.Events = EventSummary{ToolCalls: tack.ToolCalls}
	} else {
		rec.Events = SummarizeEvents(filepath.Join(dir, "events.jsonl"), repo)
	}
	rec.Prompt = Prompts(rec.Requests, stmt).WithInstructions(ctx, o.CountTokens, repo)
	diff := Diff(repo, t.Base)
	rec.DiffBytes = len(diff)
	_ = os.WriteFile(filepath.Join(dir, "diff.patch"), []byte(diff), 0o644)
	or, err := Oracle(ctx, mirror, t, repo)
	rec.Pass, rec.Oracle, rec.OracleExit = or.Pass && !rec.Run.CapHit, or.Tests, or.Exit
	_ = os.WriteFile(filepath.Join(dir, "oracle.log"), or.Output, 0o644)
	if err != nil {
		rec.OracleErr = err.Error()
		rec.Pass = false
	}
	rec.CappedSecs = rec.Run.WallSecs
	if !rec.Pass {
		rec.CappedSecs = o.Cap.Seconds()
	}
	return rec
}

// StatementTokens is the statement's token count as PromptStats.Statement defines it, or 0 when count is nil or fails.
func StatementTokens(ctx context.Context, count func(context.Context, string) (int, error), statement string) int {
	if count == nil {
		return 0
	}
	n, err := count(ctx, statement)
	one, err2 := count(ctx, "x")
	if err != nil || err2 != nil {
		return 0
	}
	return n - one + 1
}

// CheckRecord is one task's line in oracle-check.jsonl.
type CheckRecord struct {
	Tack   string            `json:"tack"`
	Task   int               `json:"task"`
	Repo   string            `json:"repo"`
	Commit string            `json:"commit"`
	Pass   bool              `json:"pass"`
	Oracle map[string]string `json:"oracle"`
	Exit   string            `json:"oracle_exit,omitempty"`
	Err    string            `json:"err,omitempty"`
	Secs   float64           `json:"secs"`
	At     string            `json:"at"`
}

// OracleCheck is the grader's own check, with no model. For each task it prepares a repository as a run does
// (PrepareRepo), but at the fix commit, and calls Oracle exactly as a run does. Every task must pass; one that does not
// cannot be graded in this environment. It writes out/task-NN/oracle-check/{repo,oracle.log} and a line per task to
// out/oracle-check.jsonl, and returns how many passed.
func OracleCheck(ctx context.Context, tasks []Task, mirrors, out, tackRev string, log io.Writer) (int, error) {
	f, err := os.OpenFile(filepath.Join(out, "oracle-check.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	passed := 0
	for i, t := range tasks {
		if ctx.Err() != nil {
			return passed, ctx.Err()
		}
		rec := CheckRecord{Tack: tackRev, Task: t.N, Repo: t.Repo, Commit: t.Commit, At: time.Now().UTC().Format(time.RFC3339)}
		dir := filepath.Join(out, fmt.Sprintf("task-%02d", t.N), "oracle-check")
		repo := filepath.Join(dir, "repo")
		t0 := time.Now()
		if err := PrepareRepo(filepath.Join(mirrors, t.Repo+".git"), t.Commit, repo); err != nil {
			rec.Err = "prepare: " + err.Error()
		} else if or, err := Oracle(ctx, filepath.Join(mirrors, t.Repo+".git"), t, repo); err != nil {
			rec.Err = "oracle: " + err.Error()
		} else {
			rec.Pass, rec.Oracle, rec.Exit = or.Pass, or.Tests, or.Exit
			_ = os.WriteFile(filepath.Join(dir, "oracle.log"), or.Output, 0o644)
		}
		rec.Secs = time.Since(t0).Seconds()
		if rec.Pass {
			passed++
		}
		if err := enc.Encode(rec); err != nil {
			return passed, err
		}
		fmt.Fprintf(log, "%s oracle-check: %d/%d task %d %s %s: pass=%v %v %s (%.0f s)\n", time.Now().Format("15:04:05"),
			i+1, len(tasks), t.N, t.Repo, t.Commit[:8], rec.Pass, rec.Oracle, rec.Err, rec.Secs)
	}
	return passed, nil
}
