package eval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/townsendmerino/tack/internal/models"
	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server"
	"github.com/townsendmerino/tack/tools/gate"
)

// Options configures a run of `tack eval models`.
type Options struct {
	Manifest *Manifest
	Out      string // results directory: results.jsonl, check-<arm>.txt
	Arm      string // run only this arm
	FitOnly  bool   // the day fit check: load each arm and record goinfer's plan, nothing timed
	TackRev  string
	LockPath string
	Log      io.Writer     // phases, heartbeats and results as they happen
	Beat     time.Duration // heartbeat interval (default 30s)
	// SettleWait is how long an arm waits for memory pressure to return to normal before it is skipped (default 2m).
	SettleWait time.Duration
	// Pressure reads memory pressure; nil means models.MemoryPressure.
	Pressure func() (models.Pressure, error)
}

// ErrHeld is returned (wrapped) when another timed run holds the box.
var ErrHeld = errors.New("the timing lock is held")

// ArmResult is how one arm ended. Verdict is the pre-registered grade (eligible, parked or fail) of a measured arm,
// "admitted" for a measured arm of a fit check, "fallback" for a fit probe that fell back as its manifest expects,
// "void" for an arm that was not measured, and "unexpected" for a fit probe that ended otherwise without a gap (its
// GPU load now fits, or every load was refused). A fail is a result about the model on the decode path the arm names;
// a void is no result at all. Reasons is non-empty exactly for void and unexpected arms, one line per cause.
type ArmResult struct {
	Name    string
	Verdict string
	Reasons []string
}

// Run runs every selected arm of the manifest in order and returns how each ended. A timed run holds the timing lock
// throughout; the fit check only checks that no timed run holds it. An arm that fails or is void records why and the
// run goes on to the next; once ctx is done, the arms not reached are void. The error is for a run that could not
// start (the lock, the results file), never for an arm.
func Run(ctx context.Context, o Options) ([]ArmResult, error) {
	if o.Beat == 0 {
		o.Beat = 30 * time.Second
	}
	if o.Pressure == nil {
		o.Pressure = models.MemoryPressure
	}
	if o.SettleWait == 0 {
		o.SettleWait = 2 * time.Minute
	}
	if o.FitOnly {
		if err := CheckTimingLock(o.LockPath); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrHeld, err)
		}
	} else {
		lock, err := AcquireTimingLock(o.LockPath, "tack eval models "+o.Manifest.Box)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrHeld, err)
		}
		defer lock.Release()
	}
	if err := os.MkdirAll(o.Out, 0o755); err != nil {
		return nil, err
	}
	rf, err := os.OpenFile(filepath.Join(o.Out, "results.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	defer rf.Close()
	r := &runner{o: o, results: rf}
	start := time.Now()
	r.logf("e0 %s: start %s, %d arms, fit-only=%v, tack %s, goinfer %s", o.Manifest.Box, start.Format("15:04:05 MST"),
		len(o.Manifest.Arms), o.FitOnly, o.TackRev, o.Manifest.GoinferRev)
	var results []ArmResult
	for i, arm := range o.Manifest.Arms {
		if o.Arm != "" && arm.Name != o.Arm {
			continue
		}
		if ctx.Err() != nil {
			why := fmt.Sprintf("not reached: %v", ctx.Err())
			r.record(arm, "verdict", map[string]any{"verdict": "void", "void": []string{why}})
			results = append(results, ArmResult{Name: arm.Name, Verdict: "void", Reasons: []string{why}})
			continue
		}
		r.logf("e0 %s: arm %d/%d %s (started %s, elapsed %s)", o.Manifest.Box, i+1, len(o.Manifest.Arms), arm.Name,
			start.Format("15:04"), time.Since(start).Round(time.Second))
		results = append(results, r.arm(ctx, arm))
	}
	r.logf("e0 %s: done in %s", o.Manifest.Box, time.Since(start).Round(time.Second))
	return results, nil
}

// Summarize writes one line per arm, the reasons under each void or unexpected arm, and the VERDICT line, and returns
// the exit status: PASS (0) when every arm was measured, whatever its grade, and every fit probe fell back as expected;
// INCONCLUSIVE (2) when any arm is void or unexpected, or no arm ran.
func Summarize(w io.Writer, name string, results []ArmResult) int {
	var cells []gate.Cell
	flagged := map[string][]string{} // verdict → arm names, for void and unexpected
	for _, a := range results {
		c := gate.Cell{Name: a.Name, Outcome: gate.OK, Fields: []gate.Field{gate.F("verdict", a.Verdict)}}
		fmt.Fprintf(w, "%-10s %s\n", a.Verdict, a.Name)
		if len(a.Reasons) > 0 {
			c.Outcome = gate.Inconclusive
			flagged[a.Verdict] = append(flagged[a.Verdict], a.Name)
			for _, why := range a.Reasons {
				fmt.Fprintf(w, "    %s\n", why)
			}
		}
		cells = append(cells, c)
	}
	rep := gate.Reconcile(cells)
	msg := fmt.Sprintf("%s: %d of %d arms measured or fell back as expected", name, rep.Pass, len(results))
	for _, v := range []string{"void", "unexpected"} {
		if len(flagged[v]) > 0 {
			msg += fmt.Sprintf("; %s: %s", v, strings.Join(flagged[v], ", "))
		}
	}
	fmt.Fprintln(w, gate.Verdict(rep.Outcome, msg))
	return rep.Exit
}

type runner struct {
	o       Options
	mu      sync.Mutex
	results io.Writer
}

func (r *runner) logf(format string, args ...any) {
	if r.o.Log != nil {
		fmt.Fprintf(r.o.Log, time.Now().Format("15:04:05")+" "+format+"\n", args...)
	}
}

// record writes one result line.
func (r *runner) record(arm Arm, phase string, fields map[string]any) {
	rec := map[string]any{"box": r.o.Manifest.Box, "arm": arm.Name, "model_path": arm.ModelPath, "phase": phase,
		"at": time.Now().UTC().Format(time.RFC3339), "tack": r.o.TackRev, "goinfer": r.o.Manifest.GoinferRev}
	maps.Copy(rec, fields)
	b, _ := json.Marshal(rec)
	r.mu.Lock()
	defer r.mu.Unlock()
	fmt.Fprintf(r.results, "%s\n", b)
}

// progress is the heartbeat's state: the phase and its done/total count. Items differ in cost (a 16k prompt against
// a 4k one), so the heartbeat reports counts, not an estimate of time left.
type progress struct {
	mu          sync.Mutex
	phase       string
	done, total int
	phaseStart  time.Time
}

func (p *progress) enter(phase string, total int) {
	p.mu.Lock()
	p.phase, p.done, p.total, p.phaseStart = phase, 0, total, time.Now()
	p.mu.Unlock()
}

func (p *progress) step() {
	p.mu.Lock()
	p.done++
	p.mu.Unlock()
}

// arm runs one arm and records its one verdict line last. An arm is void, not failed, when anything it should have
// measured is missing: goinfer decoding on another path than the arm names, memory pressure, a load or request that
// got no answer, a check that did not finish, a timing rep that errored, or the run being stopped. A server that
// answers with an error (a load refused, a 400 for a missing tool template) is a result, graded by the rule.
func (r *runner) arm(ctx context.Context, arm Arm) (res ArmResult) {
	res.Name = arm.Name
	if arm.DecodePath == "" { // a manifest built in code skips validate
		arm.DecodePath = DecodePathFor(arm.Args)
	}
	grade, why := "", "" // the registered grade, set once fit, check and smoke have run
	window, admitted := 0, 0
	flag := func(format string, args ...any) { res.Reasons = append(res.Reasons, fmt.Sprintf(format, args...)) }
	defer func() { // the first deferred, so it runs last: after the child stops and the footprint is recorded
		if ctx.Err() != nil {
			flag("interrupted: %v", ctx.Err())
		}
		res.Verdict = grade
		f := map[string]any{"verdict": grade, "why": why, "window": window, "admitted_ctx": admitted}
		if len(res.Reasons) > 0 && grade != "unexpected" { // a gap voids whatever grade was reached before it
			res.Verdict = "void"
			f["verdict"], f["grade"] = "void", grade
		}
		if len(res.Reasons) > 0 {
			f["reasons"] = res.Reasons
		}
		r.record(arm, "verdict", f)
		if len(res.Reasons) > 0 {
			r.logf("  %s: verdict %s: %s", arm.Name, res.Verdict, strings.Join(res.Reasons, "; "))
		} else {
			r.logf("  %s: verdict %s (%s)", arm.Name, grade, why)
		}
	}()
	armStart := time.Now()
	p := &progress{}
	stopBeat := make(chan struct{})
	go func() {
		t := time.NewTicker(r.o.Beat)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				p.mu.Lock()
				r.logf("  heartbeat %s phase=%s %d/%d, phase %s, arm %s", arm.Name, p.phase, p.done, p.total,
					time.Since(p.phaseStart).Round(time.Second), time.Since(armStart).Round(time.Second))
				p.mu.Unlock()
			case <-stopBeat:
				return
			}
		}
	}()
	defer close(stopBeat)
	phase := func(name string, total int, f func()) {
		p.enter(name, total)
		r.logf("  %s: enter %s", arm.Name, name)
		t0 := time.Now()
		f()
		r.logf("  %s: leave %s after %s", arm.Name, name, time.Since(t0).Round(time.Millisecond))
	}

	// Memory pressure, sampled through the arm (the Mac's guard signal), and VRAM on CUDA boxes.
	samp := newSampler(r.o.Pressure)
	defer samp.stop()

	var dirs []string // removed when the arm ends: the admitted child keeps its socket and log there until then
	defer func() {
		for _, d := range dirs {
			os.RemoveAll(d)
		}
	}()
	var sup *models.Supervisor
	var srv server.Server
	phase("fit", len(arm.Ctx), func() {
		if pr := r.settle(ctx, arm); pr >= models.PressureWarning {
			r.record(arm, "fit", map[string]any{"skipped": "pressure", "pressure": pr.String()})
			flag("memory pressure did not return to normal (%s)", pr)
			return
		}
		for _, n := range arm.Ctx {
			dir, err := os.MkdirTemp("/tmp", "tke0")
			if err != nil {
				r.record(arm, "fit", map[string]any{"ctx": n, "error": err.Error()})
				flag("-ctx %d: %v", n, err)
				return
			}
			dirs = append(dirs, dir)
			s, err := models.NewSupervisor(models.ChildConfig{Binary: r.o.Manifest.GoinferServe, ModelPath: arm.ModelPath,
				Model: arm.Name, Ctx: n, Args: arm.Args, Dir: dir, IdleUnload: 24 * time.Hour, IdleExit: 24 * time.Hour,
				Pressure: r.o.Pressure})
			if err != nil {
				r.record(arm, "fit", map[string]any{"ctx": n, "error": err.Error()})
				flag("-ctx %d: %v", n, err)
				return
			}
			t0 := time.Now()
			c, err := s.Ensure(ctx)
			p.step()
			if err != nil {
				_ = s.Stop()
				r.record(arm, "fit", map[string]any{"ctx": n, "admitted": false, "error": err.Error(), "log_tail": tail(filepath.Join(dir, "goinfer-serve.log"), 20)})
				r.logf("  %s: -ctx %d refused: %v", arm.Name, n, err)
				if refused := (*models.RefusedError)(nil); !errors.As(err, &refused) {
					flag("-ctx %d: the load got no answer from goinfer: %v", n, err)
				}
				continue
			}
			plan, _ := s.Plan()
			// /v1/models reports the model's maximum (32768 for Qwen2.5) even where -ctx caps the resident context, so the
			// usable window is the smaller of the two.
			window, admitted, sup, srv = n, n, s, c
			if plan.ContextWindow > 0 && plan.ContextWindow < n {
				window = plan.ContextWindow
			}
			r.record(arm, "fit", map[string]any{"ctx": n, "admitted": true, "fit_line": plan.FitLine,
				"decode_path": plan.DecodePath, "want_decode_path": arm.DecodePath, "context_window": plan.ContextWindow,
				"load_ms": time.Since(t0).Milliseconds()})
			r.logf("  %s: -ctx %d admitted, window %d, decode path %q; %s", arm.Name, n, window, plan.DecodePath, plan.FitLine)
			switch fb := arm.ExpectFallback; {
			case fb == "":
				if v := VoidPath(arm.DecodePath, plan.DecodePath); v != "" {
					flag("%s", v)
				}
			case plan.DecodePath != "" && strings.HasPrefix(plan.DecodePath, fb):
				grade, why = "fallback", fmt.Sprintf("fit probe: fell back to %s as expected", fb)
			case VoidPath(arm.DecodePath, plan.DecodePath) == "":
				grade = "unexpected"
				flag("good news: the load now fits %s at -ctx %d (%s); the manifest expects a fallback to %s, "+
					"so this arm can become a measured one", arm.DecodePath, n, plan.FitLine, fb)
			default:
				flag("%s", VoidPath(fb, plan.DecodePath))
			}
			return
		}
	})
	if arm.ExpectFallback != "" { // a fit probe stops at the fit, whatever it found
		if sup != nil {
			_ = sup.Stop()
		} else if len(res.Reasons) == 0 {
			grade = "unexpected"
			flag("goinfer refused every load; the manifest expects a fallback to %s", arm.ExpectFallback)
		}
		return
	}
	if sup == nil {
		if len(res.Reasons) == 0 { // goinfer answered every load, and refused each: a result
			grade, why = "fail", "no context was admitted"
		}
		return
	}
	if len(res.Reasons) > 0 { // the wrong decode path, or a larger -ctx with no answer: nothing measured would be this arm's
		_ = sup.Stop()
		r.logf("  %s: void at fit; nothing else is measured for this arm", arm.Name)
		return
	}
	defer func() {
		_ = sup.Stop()
		rss := sup.PeakRSS()
		pmax, vram := samp.stop()
		r.record(arm, "footprint", map[string]any{"rss_peak_bytes": rss, "rss_note": "RSS: reads low under pressure on Darwin",
			"pressure_max": pmax.String(), "vram_peak_mib": vram})
	}()
	if r.o.FitOnly {
		grade, why = "admitted", fmt.Sprintf("fit only: -ctx %d, window %d", admitted, window)
		return
	}

	url, key := sup.Endpoint()
	checkOK, checkFails := true, 0
	phase("check", 1, func() {
		out, code := r.serveCheck(ctx, url, key, arm.Name)
		p.step()
		path := filepath.Join(r.o.Out, "check-"+arm.Name+".txt")
		_ = os.WriteFile(path, []byte(out), 0o644)
		checkFails = failedRows(out, code)
		checkOK = code == 0
		r.record(arm, "check", map[string]any{"exit": code, "failed_rows": checkFails, "output": path})
		if code < 0 {
			flag("goinfer-serve check did not finish (see %s)", path)
		}
	})

	smoke := [2]int{}
	malformed, unanswered := 0, 0
	var firstUnanswered error
	var unstable []int
	phase("smoke", 2*len(SmokeTasks), func() {
		first := make([]bool, len(SmokeTasks))
		for run := 0; run < 2; run++ {
			for i := range SmokeTasks {
				res, err := r.smokeOne(ctx, srv, arm, i)
				p.step()
				f := map[string]any{"run": run + 1, "result": res}
				if err != nil {
					f["error"] = err.Error()
					if se := (*server.Error)(nil); !errors.As(err, &se) { // a server.Error is an answer, a result
						if unanswered++; unanswered == 1 {
							firstUnanswered = err
						}
					}
				}
				r.record(arm, "smoke", f)
				if res.Pass {
					smoke[run]++
				}
				if res.Malformed {
					malformed++
				}
				if run == 0 {
					first[i] = res.Pass
				} else if first[i] != res.Pass {
					unstable = append(unstable, i)
				}
			}
			r.logf("  %s: smoke run %d: %d/%d", arm.Name, run+1, smoke[run], len(SmokeTasks))
		}
		r.record(arm, "smoke_summary", map[string]any{"pass": smoke, "malformed": malformed, "unstable_tasks": unstable})
	})
	if unanswered > 0 {
		flag("%d of %d smoke requests got no answer from goinfer: %v", unanswered, 2*len(SmokeTasks), firstUnanswered)
	}
	grade, why = gradeArm(admitted > 0 && window >= 8192, checkOK, checkFails, smoke, malformed)
	if window < 8192 || len(res.Reasons) > 0 {
		return
	}

	filler := repoText(r.o.Manifest.Workspace)
	// 4096, 8192 and the window minus 1024 (the registration's amendment), each at most the window minus 1024.
	var sizes []int
	for _, n := range []int{4096, 8192, window - 1024} {
		if n <= window-1024 && (len(sizes) == 0 || n > sizes[len(sizes)-1]) {
			sizes = append(sizes, n)
		}
	}
	phase("ttft", len(sizes)*ttftRequests, func() {
		for _, n := range sizes {
			if ctx.Err() != nil {
				return
			}
			if err := r.ttft(ctx, srv, arm, n, filler, p); err != nil {
				flag("TTFT at %d tokens: %v", n, err)
			}
		}
	})
	phase("decode", 3, func() {
		var rates []float64
		for i := 0; i < 3; i++ {
			s, err := decodeOnce(ctx, srv, arm.Name, arm.ProbeThinking)
			p.step()
			f := map[string]any{"rep": i + 1, "tok_per_s": s.Rate, "tokens": s.Tokens, "text_chars": s.TextChars,
				"thinking_chars": s.Thinking}
			if err != nil {
				f["error"] = err.Error()
				flag("decode rep %d: %v", i+1, err)
			} else {
				rates = append(rates, s.Rate)
			}
			r.record(arm, "decode", f)
		}
		if len(rates) > 0 {
			med, lo, hi := spread(rates)
			r.record(arm, "decode_summary", map[string]any{"median": med, "min": lo, "max": hi})
			r.logf("  %s: decode median %.1f tok/s", arm.Name, med)
		}
	})
	return res
}

// gradeArm is the pre-registered per-arm rule (docs/measurements/e0-preregistration-2026-10-16.md, "Decision rule").
func gradeArm(windowOK, checkOK bool, checkFails int, smoke [2]int, malformed int) (string, string) {
	lo := min(smoke[0], smoke[1])
	switch {
	case !windowOK:
		return "fail", "no window of at least 8192 was admitted"
	case checkOK && lo >= 17 && malformed == 0:
		return "eligible", fmt.Sprintf("smoke %d/%d, check ok", smoke[0], smoke[1])
	case lo >= 14 && malformed == 0 && (checkOK || checkFails == 1):
		return "parked", fmt.Sprintf("smoke %d/%d, check failed rows %d", smoke[0], smoke[1], checkFails)
	}
	return "fail", fmt.Sprintf("smoke %d/%d, malformed %d, check ok=%v failed rows %d", smoke[0], smoke[1], malformed, checkOK, checkFails)
}

var failedRe = regexp.MustCompile(`(?m)^(\d+) of \d+ checks FAILED`)

// failedRows reads goinfer-serve check's summary line ("N of M checks FAILED", internal/servecheck/cmd.go). A failing
// exit with no summary counts as an unknown number of failures (-1).
func failedRows(out string, exit int) int {
	if m := failedRe.FindStringSubmatch(out); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	if exit == 0 {
		return 0
	}
	return -1
}

func (r *runner) serveCheck(ctx context.Context, url, key, model string) (string, int) {
	cctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(cctx, r.o.Manifest.GoinferServe, "check", "-api-key", key, "-model", model, url)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ee, ok := errors.AsType[*exec.ExitError](err); ok {
			return string(out), ee.ExitCode()
		}
		return string(out) + "\n" + err.Error(), -1
	}
	return string(out), 0
}

func smokeRequest(model, workspace string, i int) server.Request {
	zero := 0.0
	return server.Request{Model: model, System: p0SystemPrompt, Tools: p0Tools, MaxTokens: 256,
		Temperature: &zero, Messages: []server.Message{{Role: server.User,
			Content: []server.Block{server.TextBlock(p0UserText(workspace, SmokeTasks[i].Prompt))}}}}
}

func (r *runner) smokeOne(ctx context.Context, srv server.Server, arm Arm, i int) (SmokeResult, error) {
	resp, err := srv.Stream(ctx, smokeRequest(arm.Name, r.o.Manifest.Workspace, i), nil)
	if err != nil {
		return SmokeResult{Task: i, Why: "request failed"}, err
	}
	return ScoreSmoke(i, resp), nil
}

// ttft measures time to the first streamed token at about n prompt tokens, in three cells of three repeats, each after
// one warm-up (E0 amendment 11):
//   - cold: a fresh nonce at the start of the system prompt, so nothing is reused;
//   - append: each repeat is the previous request with its reply and 512 more tokens appended (appended), as tack's
//     loop grows a conversation; the warm-up is the chain's base, sized so the last repeat is about n;
//   - rewrite: the previous prompt with only its last 512 tokens changed, a secondary figure for how far the server
//     rolls back.
//
// It returns an error when the size could not be built or any request, warm-ups included, errored.
func (r *runner) ttft(ctx context.Context, srv server.Server, arm Arm, n int, filler string, p *progress) error {
	chars, counted, err := sizeFiller(ctx, srv, arm.Name, filler, n, arm.ProbeThinking)
	if err != nil {
		r.record(arm, "ttft", map[string]any{"size": n, "error": err.Error()})
		return err
	}
	failed := 0
	var firstErr error
	tailChars := chars * 512 / n
	prefix := filler[:chars-tailChars]
	tailAt := func(i int) string { // distinct tails of the same length, from further into the text
		off := (chars + i*tailChars) % (len(filler) - tailChars)
		return filler[off : off+tailChars]
	}
	measure := func(cell string, rep int, req server.Request) []proto.ModelBlock {
		d, kind, reply, err := firstTokenOf(ctx, srv, req)
		p.step()
		f := map[string]any{"size": n, "counted": counted, "cell": cell, "rep": rep, "ttft_ms": d.Milliseconds(), "first_kind": kind}
		if err != nil {
			f["error"] = err.Error()
			if failed++; failed == 1 {
				firstErr = fmt.Errorf("%s rep %d: %w", cell, rep, err)
			}
		}
		if rep > 0 {
			r.record(arm, "ttft", f)
		}
		return reply
	}
	req := func(system, user string) server.Request {
		return ttftRequest(arm.Name, system, user, arm.ProbeThinking)
	}
	nonce := func() string { return fmt.Sprintf("run %d\n\n", time.Now().UnixNano()) }
	for rep := 0; rep <= 3; rep++ { // rep 0 is the warm-up
		measure("cold", rep, req(nonce()+p0SystemPrompt, prefix+tailAt(rep)))
	}
	chain := req(p0SystemPrompt, filler[:chars-3*tailChars]) // three appends of a tail each bring it to about n
	for rep := 0; rep <= 3; rep++ {
		reply := measure("append", rep, chain)
		chain = appended(chain, reply, tailAt(20+rep))
	}
	for rep := 0; rep <= 3; rep++ {
		measure("rewrite", rep, req(p0SystemPrompt, prefix+tailAt(10+rep)))
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d requests failed, first %w", failed, ttftRequests, firstErr)
	}
	return nil
}

// sizeFiller finds how many characters of filler make the request about n tokens by the server's own count_tokens.
func sizeFiller(ctx context.Context, srv server.Server, model, filler string, n int, thinking bool) (chars, counted int, err error) {
	lo, hi := 0, len(filler)
	for lo < hi && hi-lo > 64 {
		mid := (lo + hi) / 2
		c, err := srv.CountTokens(ctx, ttftRequest(model, p0SystemPrompt, filler[:mid], thinking))
		if err != nil {
			return 0, 0, err
		}
		if c <= n {
			lo, counted = mid, c
		} else {
			hi = mid
		}
	}
	if counted < n*9/10 {
		return 0, 0, fmt.Errorf("the repository text reaches only %d of %d tokens", counted, n)
	}
	return lo, counted, nil
}

// ttftRequests is the requests ttft sends at one size: three cells of a warm-up and three repeats.
const ttftRequests = 3 * 4

// ttftMaxTokens is what a first-token request allows. One token is not enough: a reply that opens with tokens that
// never stream (Harmony's channel header, Qwen3's <think>) would show none. The probe times the first streamed delta,
// so the few tokens before it add at most a few decode steps to the measured time.
const ttftMaxTokens = 16

// The probes ask for thinking only when the arm says so (Arm.ProbeThinking). Asking turns the template's thinking on
// (goinfer's resolveThink), so by default they send what the smoke and tack's own turns send, thinking disabled
// (internal/server/goinfer/client.go:toWire). A model whose reasoning cannot be turned off (Harmony) sets it, so its
// reasoning streams as thinking deltas instead of being dropped; decodeOnce refuses a reply that held tokens back
// either way.
func ttftRequest(model, system, user string, thinking bool) server.Request {
	zero := 0.0
	return server.Request{Model: model, System: system, Tools: p0Tools, MaxTokens: ttftMaxTokens, Temperature: &zero,
		Thinking: thinking, Messages: []server.Message{{Role: server.User, Content: []server.Block{server.TextBlock(user)}}}}
}

// firstToken is the time to the first streamed delta of text or thinking, and that delta's kind.
func firstToken(ctx context.Context, srv server.Server, model, system, user string, thinking bool) (time.Duration, string, error) {
	d, kind, _, err := firstTokenOf(ctx, srv, ttftRequest(model, system, user, thinking))
	return d, kind, err
}

// firstTokenOf is firstToken for a whole request, and the reply's content, which an appended request carries forward.
func firstTokenOf(ctx context.Context, srv server.Server, req server.Request) (time.Duration, string, []proto.ModelBlock, error) {
	t0 := time.Now()
	var first time.Duration
	kind := ""
	resp, err := srv.Stream(ctx, req, func(d server.Delta) {
		if first == 0 && d.Text != "" {
			first, kind = time.Since(t0), d.Kind
		}
	})
	if err == nil && first == 0 {
		err = fmt.Errorf("no text or thinking was streamed within %d tokens", ttftMaxTokens)
	}
	var content []proto.ModelBlock
	if resp != nil {
		content = resp.Content
	}
	return first, kind, content, err
}

// appended is prev followed by its reply and a user message holding text, as tack's loop extends a conversation: the
// reply's blocks as received (an empty reply as one empty text block), then a tool result for each call it made, then
// the text. Nothing before the reply changes, so a server that keeps the previous request's prefix reuses all of it.
func appended(prev server.Request, reply []proto.ModelBlock, text string) server.Request {
	asst := server.Message{Role: server.Assistant}
	var user server.Message
	user.Role = server.User
	for _, b := range reply {
		asst.Content = append(asst.Content, server.Block{ModelBlock: b})
		if b.Type == "tool_use" {
			user.Content = append(user.Content, server.ToolResultBlock(b.ID, "ok", false))
		}
	}
	if len(asst.Content) == 0 {
		asst.Content = []server.Block{server.TextBlock("")}
	}
	user.Content = append(user.Content, server.TextBlock(text))
	next := prev
	next.Messages = append(slices.Clone(prev.Messages), asst, user)
	return next
}

// decodeSample is one decode measurement: its rate over the tokens it streamed, and what streamed.
type decodeSample struct {
	Rate                float64
	Tokens              int // the reply's output tokens, as the server counted them
	TextChars, Thinking int // characters streamed as text and as thinking
}

// decodeOnce times a 256-token reply between its first and last streamed delta, text or thinking. A reply whose
// streamed characters number fewer than its counted tokens held tokens back from the stream, and its rate would count
// work it did not time: that is an error, not a rate.
func decodeOnce(ctx context.Context, srv server.Server, model string, thinking bool) (decodeSample, error) {
	zero := 0.0
	req := server.Request{Model: model, MaxTokens: 256, Temperature: &zero, Thinking: thinking, Messages: []server.Message{{Role: server.User,
		Content: []server.Block{server.TextBlock("Write a long, detailed story about a lighthouse keeper. Keep writing until you are stopped.")}}}}
	var first, last time.Time
	var s decodeSample
	resp, err := srv.Stream(ctx, req, func(d server.Delta) {
		if d.Text == "" {
			return
		}
		now := time.Now()
		if first.IsZero() {
			first = now
		}
		last = now
		if d.Kind == "thinking" {
			s.Thinking += len(d.Text)
		} else {
			s.TextChars += len(d.Text)
		}
	})
	if err != nil {
		return s, err
	}
	s.Tokens = resp.Usage.OutputTokens
	el := last.Sub(first).Seconds()
	if s.Tokens < 2 || el <= 0 {
		return s, fmt.Errorf("too few tokens (%d) to time", s.Tokens)
	}
	if streamed := s.TextChars + s.Thinking; streamed < s.Tokens {
		return s, fmt.Errorf("%d tokens counted but only %d characters streamed: the reply held tokens back, so its rate would count work it did not time", s.Tokens, streamed)
	}
	s.Rate = float64(s.Tokens-1) / el
	return s, nil
}

// repoText is the workspace's Go source, concatenated in path order: real code to size prompts with.
func repoText(root string) string {
	var paths []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "testdata") {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(p, ".go") {
			paths = append(paths, p)
		}
		return nil
	})
	sort.Strings(paths)
	var b strings.Builder
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		rel, _ := filepath.Rel(root, p)
		fmt.Fprintf(&b, "// file: %s\n%s\n", rel, data)
	}
	return b.String()
}

func spread(xs []float64) (med, lo, hi float64) {
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	return s[len(s)/2], s[0], s[len(s)-1]
}

func tail(path string, n int) string {
	b, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// sampler records the highest memory pressure and, where nvidia-smi exists, the highest GPU memory used, once a
// second while an arm runs.
type sampler struct {
	mu   sync.Mutex
	pmax models.Pressure
	vram int
	done chan struct{}
	once sync.Once
}

func newSampler(pressure func() (models.Pressure, error)) *sampler {
	s := &sampler{done: make(chan struct{})}
	smi, _ := exec.LookPath("nvidia-smi")
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			if pr, err := pressure(); err == nil {
				s.mu.Lock()
				s.pmax = max(s.pmax, pr)
				s.mu.Unlock()
			}
			if smi != "" && runtime.GOOS == "linux" {
				out, err := exec.Command(smi, "--query-gpu=memory.used", "--format=csv,noheader,nounits").Output()
				if v, perr := strconv.Atoi(strings.TrimSpace(strings.Split(string(out), "\n")[0])); err == nil && perr == nil {
					s.mu.Lock()
					s.vram = max(s.vram, v)
					s.mu.Unlock()
				}
			}
			select {
			case <-t.C:
			case <-s.done:
				return
			}
		}
	}()
	return s
}

func (s *sampler) stop() (models.Pressure, int) {
	s.once.Do(func() { close(s.done) })
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pmax, s.vram
}

// settle waits up to SettleWait for memory pressure to return to normal before an arm loads: the previous arm's child
// has just exited, and the OS takes a moment to lower its level. It returns the level it ended at.
func (r *runner) settle(ctx context.Context, arm Arm) models.Pressure {
	deadline := time.Now().Add(r.o.SettleWait)
	for {
		pr, err := r.o.Pressure()
		if err != nil || pr < models.PressureWarning || time.Now().After(deadline) || ctx.Err() != nil {
			return pr
		}
		r.logf("  %s: memory pressure is %s; waiting up to %s for normal", arm.Name, pr, time.Until(deadline).Round(time.Second))
		select {
		case <-time.After(settlePoll):
		case <-ctx.Done():
			return pr
		}
	}
}

// settlePoll is how often settle re-reads memory pressure.
var settlePoll = 2 * time.Second

// DecodePathFor is the decode path an arm with these goinfer-serve flags measures: metal-resident or cuda-resident for
// a GPU backend (goinfer has no staged GPU path), cpu for the CPU, and "" when the flags name no backend.
func DecodePathFor(args []string) string {
	backend := ""
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-backend" || args[i] == "--backend" {
			backend = args[i+1]
		}
	}
	switch backend {
	case "metal", "cuda":
		return backend + "-resident"
	case "cpu":
		return "cpu"
	}
	return ""
}

// VoidPath reports why a server whose banner gave decode path got is not measuring want, or "" when got begins with
// want. A banner that gives no decode path is void too: nothing shows the arm runs on the path it names. goinfer
// declines a GPU quietly (a load that does not fit the device, a quantization the backend does not support) and
// serves on the CPU, answering every request; only the banner says so.
func VoidPath(want, got string) string {
	switch {
	case want == "":
		return fmt.Sprintf("the arm names no decode path; goinfer is decoding on %q", got)
	case got == "":
		return fmt.Sprintf("wanted decode path %s; goinfer's banner gave none", want)
	case !strings.HasPrefix(got, want):
		return fmt.Sprintf("wanted decode path %s; goinfer is decoding on %q", want, got)
	}
	return ""
}
