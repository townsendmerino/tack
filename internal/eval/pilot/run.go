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

// LoadTasks reads pilot.jsonl and returns tasks from..to (1-based, inclusive). Every task must be verified and carry an
// approved statement (pilot pre-registration, "Tasks"); one that does not is an error, so no draft is ever run.
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

// Oracle copies the commit's named-test files from the mirror into repo and runs the oracle tests as the miner
// verified them. Every oracle test must report pass; a skip or a build failure fails the task.
func Oracle(ctx context.Context, mirror string, t Task, repo string) (bool, map[string]string, error) {
	for _, f := range t.TestFiles {
		src, err := exec.Command("git", "-C", mirror, "show", t.Commit+":"+f).Output()
		if err != nil {
			return false, nil, fmt.Errorf("read %s at %s: %w", f, t.Commit[:8], err)
		}
		p := filepath.Join(repo, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return false, nil, err
		}
		if err := os.WriteFile(p, src, 0o644); err != nil {
			return false, nil, err
		}
	}
	res, _ := mine.RunTests(ctx, repo, t.Verified.Oracle, t.Packages)
	pass := len(t.Verified.Oracle) > 0
	for _, name := range t.Verified.Oracle {
		pass = pass && res[name] == "pass"
	}
	return pass, res, nil
}

// Request is one line of goinfer-serve's -log-requests output.
type Request struct {
	Prompt, Completion, Reused int
	TTFTms, Totalms            int
	Raw                        string
}

var reqField = regexp.MustCompile(`(prompt_tokens|completion_tokens|reused|ttft|total)=(\d+)`)

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

// EventSummary is what the run record takes from opencode's JSON events. It does not depend on the event schema: any
// object with a string "tool" field counts as a tool call, and any string that names a path outside the repository
// (an absolute path, or one climbing out with ..) marks the run left_repo. Over-flagging is the safe direction: a
// flagged run is reported both counted and excluded (pilot pre-registration, "Recorded per run").
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
	var walk func(v any)
	walk = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			if name, ok := t["tool"].(string); ok && name != "" {
				s.ToolCalls[name]++
			}
			for _, e := range t {
				walk(e)
			}
		case []any:
			for _, e := range t {
				walk(e)
			}
		case string:
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
			walk(v)
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
	Tasks     []Task
	Mirrors   string // <repo>.git bare mirrors
	Opencode  string
	A0, A0k   Arm
	Server    Server // filled by the caller once the server is up
	ServeLog  string // the server's log, for each run's request window
	Out       string
	Cap       time.Duration
	KenWarmup time.Duration // the warm-up's own limit, outside the clock
	Log       io.Writer
}

// Record is one run's line in results.jsonl.
type Record struct {
	Task         int               `json:"task"`
	Repo         string            `json:"repo"`
	Commit       string            `json:"commit"`
	Base         string            `json:"base"`
	Run          Result            `json:"run"`
	CappedSecs   float64           `json:"capped_secs"` // the wall time, or the full cap for a failed run (PAR-1)
	Pass         bool              `json:"pass"`
	Oracle       map[string]string `json:"oracle"`
	OracleErr    string            `json:"oracle_err,omitempty"`
	KenIndexSecs float64           `json:"ken_index_secs,omitempty"` // A0k's untimed warm-up
	Requests     []Request         `json:"requests"`
	Events       EventSummary      `json:"events"`
	DiffBytes    int               `json:"diff_bytes"`
	At           string            `json:"at"`
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
	total := len(o.Tasks) * 2
	done := 0
	logf := func(format string, args ...any) {
		fmt.Fprintf(o.Log, time.Now().Format("15:04:05")+" pilot: "+format+"\n", args...)
	}
	logf("start %s, %d tasks x 2 arms, cap %s per run", start.Format("15:04 MST"), len(o.Tasks), o.Cap)
	for _, t := range o.Tasks {
		for _, arm := range ArmOrder(t.N, o.A0, o.A0k) {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			done++
			rec := runOne(ctx, o, t, arm)
			if err := enc.Encode(rec); err != nil {
				return err
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
	rec := Record{Task: t.N, Repo: t.Repo, Commit: t.Commit, Base: t.Base, At: time.Now().UTC().Format(time.RFC3339)}
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
	from := size(o.ServeLog)
	rec.Run = RunOpencode(ctx, o.Opencode, o.Server, arm, repo, t.Statement, dir, o.Cap)
	rec.Requests = Requests(o.ServeLog, from, size(o.ServeLog))
	rec.Events = SummarizeEvents(filepath.Join(dir, "events.jsonl"), repo)
	diff := Diff(repo, t.Base)
	rec.DiffBytes = len(diff)
	_ = os.WriteFile(filepath.Join(dir, "diff.patch"), []byte(diff), 0o644)
	pass, res, err := Oracle(ctx, mirror, t, repo)
	rec.Pass, rec.Oracle = pass && !rec.Run.CapHit, res
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
