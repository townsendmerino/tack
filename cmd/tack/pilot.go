package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/townsendmerino/tack/internal/eval"
	"github.com/townsendmerino/tack/internal/eval/pilot"
	"github.com/townsendmerino/tack/internal/models"
	"github.com/townsendmerino/tack/internal/server"
	"github.com/townsendmerino/tack/internal/server/goinfer"
	"github.com/townsendmerino/tack/tools/gate"
)

// The smoke task: ken at the pilot's task-2 base, a read-only question, so both arms exercise the server, opencode's
// tools and (for A0k) ken-mcp, without an edit to grade.
const (
	smokeRepo      = "ken"
	smokeBase      = "14796618fb7771d1b3b35d952f905772cf1580c4"
	smokeStatement = "List the Go files under internal/structural, then tell me which function extracts structure from one file."
)

// pilotCmd is `tack eval pilot` (docs/measurements/e1-pilot-preregistration-2026-10-16.md). With -smoke it runs the
// smoke task once per arm. Otherwise it runs tasks -from to -to of -tasks, both arms each, as one night job.
func pilotCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("eval pilot", flag.ContinueOnError)
	fs.SetOutput(stderr)
	smoke := fs.Bool("smoke", false, "run the smoke task once per arm")
	tasks := fs.String("tasks", "", "pilot.jsonl, with approved statements")
	from := fs.Int("from", 1, "first task (1-based)")
	to := fs.Int("to", 10, "last task")
	mirrors := fs.String("mirrors", "", "directory of bare mirrors, <repo>.git")
	opencode := fs.String("opencode", "", "the opencode binary")
	kenMCP := fs.String("ken-mcp", "", "the ken-mcp binary (A0k)")
	kenModel := fs.String("ken-model", "", "ken's embedding model directory")
	serve := fs.String("goinfer-serve", "", "the goinfer-serve binary")
	modelPath := fs.String("model-path", "", "the checkpoint, from ~/models")
	ctxN := fs.Int("ctx", 32768, "the served context (the registration's is 32768: opencode's fixed prompt alone exceeds 16384)")
	out := fs.String("out", "", "results directory (durable)")
	capFlag := fs.Duration("cap", 10*time.Minute, "wall-time cap per run (the registration's is 10m)")
	title := fs.Bool("title", true, "leave opencode's title agent on; -title=false turns it off on both arms")
	rev := fs.String("tack-rev", "", "the tack commit this binary must have been built from (checked against its build info)")
	oracleCheck := fs.Bool("oracle-check", false, "no model: run each task's oracle at its fix commit, as a run grades it; every task must pass")
	e0Results := fs.String("e0-results", "", "gate: run only if this E0 results.jsonl shows -e0-arm meeting E0's smoke rule")
	e0Arm := fs.String("e0-arm", "", "gate: the E0 arm whose smoke must pass (with -e0-results)")
	a1 := fs.Bool("a1", false, "tack's arm alone (amendment 9): tack run --edit here, against the server on -remote-host")
	remoteHost := fs.String("remote-host", "", "a1: the host serving the model, reached over ssh (nobara)")
	remoteTack := fs.String("remote-tack", "", "a1: tack's binary on the remote host, which runs pilot-serve")
	remoteOut := fs.String("remote-out", "", "a1: where the remote host keeps its copy of the server's log")
	var serveOpts serveOptionList
	fs.Var(&serveOpts, "serve-option", "a1: "+serveOptionUsage)
	var settings settingList
	fs.Var(&settings, "setting", "a1: a [run] setting switched on in every arm (a change already adopted); repeatable; only: "+settingNames())
	compare := fs.String("compare-setting", "", "a1: compare two arms of tack, A without this [run] setting and B with it, A B B A A B per task, each run after a cell check (calibration C6)")
	cellCheck := fs.Bool("cell-check", false, "a1: start the server as a job would, run the cell check once with the model loaded and idle, print it, and stop")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	defer startKeepAwake(stderr)()
	if err := goOnPath(); err != nil {
		fmt.Fprintln(stderr, "tack eval pilot:", err)
		fmt.Fprintln(stdout, gate.Verdict(gate.Inconclusive, "eval pilot: not started: "+err.Error()))
		return exitError
	}
	if (*e0Results == "") != (*e0Arm == "") {
		fmt.Fprintln(stderr, "tack eval pilot: -e0-results and -e0-arm go together")
		return exitUsage
	}
	if *e0Results != "" {
		if code, ok := e0Gate(*e0Results, *e0Arm, *out, stdout, stderr); !ok {
			return code
		}
	}
	if *oracleCheck {
		return oracleCheckCmd(*tasks, *from, *to, *mirrors, *out, *rev, stdout, stderr)
	}
	if *a1 {
		if *tasks == "" || *mirrors == "" || *out == "" || *serve == "" || *modelPath == "" || *remoteHost == "" ||
			*remoteTack == "" || *remoteOut == "" {
			fmt.Fprintln(stderr, "usage: tack eval pilot -a1 -tasks pilot.jsonl -from N -to M -mirrors DIR -out DIR "+
				"-remote-host HOST -remote-tack BIN -remote-out DIR -goinfer-serve BIN -model-path FILE [-ctx 32768] [-cap 10m]")
			return exitUsage
		}
		tackRev, err := eval.Revision(*rev)
		if err != nil {
			fmt.Fprintln(stderr, "tack eval pilot:", err)
			fmt.Fprintln(stdout, gate.Verdict(gate.Inconclusive, "eval pilot: not started: "+err.Error()))
			return exitError
		}
		ts, err := pilot.LoadTasks(*tasks, *from, *to)
		if err != nil {
			fmt.Fprintln(stderr, "tack eval pilot:", err)
			return exitError
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return a1Cmd(ctx, ts, *mirrors, *out, *capFlag, tackRev, remote{host: *remoteHost, tack: *remoteTack, serve: *serve,
			modelPath: *modelPath, out: *remoteOut, rev: *rev, ctxN: *ctxN, options: serveOpts, settings: settings, compare: *compare, cellCheck: *cellCheck}, stdout, stderr)
	}
	if (!*smoke && *tasks == "") || *mirrors == "" || *opencode == "" || *kenMCP == "" || *kenModel == "" || *serve == "" ||
		*modelPath == "" || *out == "" {
		fmt.Fprintln(stderr, "usage: tack eval pilot (-smoke | -tasks pilot.jsonl -from N -to M) -mirrors DIR -opencode BIN "+
			"-ken-mcp BIN -ken-model DIR -goinfer-serve BIN -model-path FILE -out DIR [-cap 10m]")
		return exitUsage
	}
	tackRev, err := eval.Revision(*rev)
	if err != nil {
		fmt.Fprintln(stderr, "tack eval pilot:", err)
		fmt.Fprintln(stdout, gate.Verdict(gate.Inconclusive, "eval pilot: not started: "+err.Error()))
		return exitError
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a0, a0k := pilot.Arm{Name: "A0", NoTitle: !*title}, pilot.Arm{Name: "A0k", KenMCP: *kenMCP, KenModel: *kenModel, NoTitle: !*title}
	if *smoke {
		err = withServer(ctx, *serve, *modelPath, *ctxN, *out, stderr, func(srv pilot.Server, serveLog string, capture *pilot.Capture) error {
			return runSmoke(ctx, *mirrors, *opencode, a0, a0k, srv, serveLog, capture, *out, *capFlag, tackRev, stderr)
		})
	} else {
		var ts []pilot.Task
		if ts, err = pilot.LoadTasks(*tasks, *from, *to); err == nil {
			err = withServer(ctx, *serve, *modelPath, *ctxN, *out, stderr, func(srv pilot.Server, serveLog string, capture *pilot.Capture) error {
				return pilot.RunPilot(ctx, pilot.Options{Capture: capture, Tasks: ts, Mirrors: *mirrors, Opencode: *opencode, A0: a0, A0k: a0k,
					Server: srv, ServeLog: serveLog, Out: *out, Cap: *capFlag, KenWarmup: 15 * time.Minute, Log: stderr,
					CountTokens: counter(srv), TackRev: tackRev})
			})
		}
	}
	if err != nil {
		fmt.Fprintln(stderr, "tack eval pilot:", err)
		var void voidError
		switch {
		case errors.As(err, &void):
			fmt.Fprintln(stdout, gate.Verdict(gate.Inconclusive, "eval pilot: void, no task ran: "+string(void)))
			return gate.Reconcile([]gate.Cell{{Outcome: gate.Inconclusive}}).Exit
		case errors.Is(err, eval.ErrHeld):
			return exitHeld
		}
		return exitError
	}
	return exitOK
}

// voidError is a pilot run that measured nothing: the server is not decoding on the path the run names, so both arms
// are void before any task starts.
type voidError string

func (e voidError) Error() string { return "void: " + string(e) }

// withServer takes (or, under night.py, inherits) the timing lock, starts one supervised goinfer-serve with
// -log-requests, runs fn against it, and saves the server's log into out.
func withServer(ctx context.Context, serve, modelPath string, ctxN int, out string, log io.Writer, fn func(pilot.Server, string, *pilot.Capture) error) error {
	ps, err := startPilotServer(ctx, serve, modelPath, ctxN, out, nil, log)
	if err != nil {
		return err
	}
	defer ps.stop()
	// opencode talks to goinfer through the recording proxy, so every request and response is kept raw in the run's
	// directory (pilot.Capture); tack's own count_tokens calls go to goinfer directly.
	capture, err := pilot.StartCapture(ps.url)
	if err != nil {
		return err
	}
	defer capture.Close()
	logf(log, "pilot: requests are recorded raw through %s; sampling %v", capture.URL, pilot.Sampling)
	return fn(pilot.Server{BaseURL: capture.URL, APIKey: ps.key, Model: "qwen", Context: ctxN, Direct: ps.url}, ps.serveLog, capture)
}

// pilotServer is the pilot's goinfer-serve, up and on its registered decode path.
type pilotServer struct {
	url, key, serveLog string
	plan               models.Plan
	stop               func() // stops the server, copies its log into out and releases the timing lock
}

// startPilotServer holds this machine's timing lock and starts the pilot's goinfer-serve: CUDA, one slot, every request
// logged, at ctxN. A server not on its registered decode path is void (voidError), and is stopped.
func startPilotServer(ctx context.Context, serve, modelPath string, ctxN int, out string, opts serveOptionList, log io.Writer) (*pilotServer, error) {
	lockPath, err := eval.TimingLockPath()
	if err != nil {
		return nil, err
	}
	lock, err := eval.AcquireTimingLock(lockPath, "tack eval pilot")
	if err != nil {
		return nil, fmt.Errorf("%w: %v", eval.ErrHeld, err)
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		lock.Release()
		return nil, err
	}
	dir, err := os.MkdirTemp("/tmp", "tkpilot")
	if err != nil {
		lock.Release()
		return nil, err
	}
	args := append([]string{"-backend", "cuda", "-kv-sessions", "1", "-log-requests"}, opts.args()...)
	sup, err := models.NewSupervisor(models.ChildConfig{Binary: serve, ModelPath: modelPath, Model: "qwen", Ctx: ctxN,
		Args: args, Dir: dir, IdleUnload: 24 * time.Hour, IdleExit: 24 * time.Hour})
	if err != nil {
		os.RemoveAll(dir)
		lock.Release()
		return nil, err
	}
	ps := &pilotServer{serveLog: filepath.Join(dir, "goinfer-serve.log")}
	ps.stop = func() {
		_ = sup.Stop()
		if b, err := os.ReadFile(ps.serveLog); err == nil {
			_ = os.WriteFile(filepath.Join(out, "goinfer-serve.log"), b, 0o644)
		}
		os.RemoveAll(dir)
		lock.Release()
	}
	t0 := time.Now()
	if _, err := sup.Ensure(ctx); err != nil {
		ps.stop()
		return nil, err
	}
	ps.plan, _ = sup.Plan()
	ps.url, ps.key = sup.Endpoint()
	logf(log, "pilot: server up in %s, decode path %q; %s", time.Since(t0).Round(time.Second), ps.plan.DecodePath, ps.plan.FitLine)
	if why := eval.VoidPath(eval.DecodePathFor(args), ps.plan.DecodePath); why != "" {
		ps.stop()
		return nil, voidError(why)
	}
	return ps, nil
}

func runSmoke(ctx context.Context, mirrors, opencode string, a0, a0k pilot.Arm, srv pilot.Server, serveLog string, capture *pilot.Capture, out string, capD time.Duration, tackRev string, log io.Writer) error {
	start := time.Now()
	logf(log, "pilot smoke: start %s, cap %s per run", start.Format("15:04 MST"), capD)
	var results []pilot.Result
	kenIndex := map[string]float64{}
	prompts := map[string]pilot.PromptStats{}
	for _, arm := range []pilot.Arm{a0, a0k} {
		runDir := filepath.Join(out, arm.Name)
		repo := filepath.Join(runDir, "repo")
		if err := pilot.PrepareRepo(filepath.Join(mirrors, smokeRepo+".git"), smokeBase, repo); err != nil {
			return fmt.Errorf("%s: %w", arm.Name, err)
		}
		if arm.KenMCP != "" { // the untimed warm-up (pilot pre-registration, amendment)
			kl, _ := os.Create(filepath.Join(runDir, "ken-warmup.log"))
			s, err := pilot.WarmKen(ctx, arm.KenMCP, arm.KenModel, repo, kl, 15*time.Minute)
			kl.Close()
			if err != nil {
				return fmt.Errorf("%s: ken warm-up: %w", arm.Name, err)
			}
			kenIndex[arm.Name] = s
			logf(log, "pilot smoke: %s: ken index built in %.1f s (untimed)", arm.Name, s)
		}
		stmt := pilot.StatementTokens(ctx, counter(srv), smokeStatement)
		from := fileSize(serveLog)
		logf(log, "pilot smoke: %s: opencode, cap %s", arm.Name, capD)
		capture.SetDir(filepath.Join(runDir, "requests"))
		r := pilot.RunOpencode(ctx, opencode, srv, arm, repo, smokeStatement, runDir, capD)
		capture.SetDir("")
		if err := pilot.CheckSampling(filepath.Join(runDir, "requests")); err != nil {
			return fmt.Errorf("%s: %w", arm.Name, err)
		}
		prompts[arm.Name] = pilot.Prompts(pilot.Requests(serveLog, from, fileSize(serveLog)), stmt).WithInstructions(ctx, counter(srv), repo)
		logf(log, "pilot smoke: %s: prompt %+v", arm.Name, prompts[arm.Name])
		_ = os.WriteFile(filepath.Join(runDir, "diff.patch"), []byte(pilot.Diff(repo, smokeBase)), 0o644)
		results = append(results, r)
		logf(log, "pilot smoke: %s: exit %d, cap hit %v, %.0f s %s", arm.Name, r.Exit, r.CapHit, r.WallSecs, r.Err)
	}
	b, _ := json.MarshalIndent(map[string]any{"tack": tackRev, "statement": smokeStatement, "repo": smokeRepo, "base": smokeBase,
		"results": results, "ken_index_secs": kenIndex, "prompt": prompts}, "", "  ")
	if err := os.WriteFile(filepath.Join(out, "summary.json"), b, 0o644); err != nil {
		return err
	}
	logf(log, "pilot smoke: done in %s (started %s)", time.Since(start).Round(time.Second), start.Format("15:04"))
	return nil
}

// counter counts a lone user message on the pilot's server, for pilot.StatementTokens.
func counter(srv pilot.Server) func(context.Context, string) (int, error) {
	base := srv.BaseURL
	if srv.Direct != "" {
		base = srv.Direct
	}
	c := goinfer.New("pilot", base, srv.APIKey, nil)
	return func(ctx context.Context, text string) (int, error) {
		return c.CountTokens(ctx, server.Request{Model: srv.Model, MaxTokens: 1,
			Messages: []server.Message{{Role: server.User, Content: []server.Block{server.TextBlock(text)}}}})
	}
}

func fileSize(p string) int64 {
	fi, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return fi.Size()
}

// oracleCheckCmd is `tack eval pilot -oracle-check`: the grader's own check, with no model (pilot.OracleCheck). It holds
// the timing lock, as builds and tests load the box, and passes only if every task's oracle passes at its fix commit.
func oracleCheckCmd(tasks string, from, to int, mirrors, out, rev string, stdout, stderr io.Writer) int {
	if tasks == "" || mirrors == "" || out == "" {
		fmt.Fprintln(stderr, "usage: tack eval pilot -oracle-check -tasks pilot.jsonl [-from N -to M] -mirrors DIR -out DIR")
		return exitUsage
	}
	tackRev, err := eval.Revision(rev)
	if err != nil {
		fmt.Fprintln(stderr, "tack eval pilot:", err)
		fmt.Fprintln(stdout, gate.Verdict(gate.Inconclusive, "oracle check: not started: "+err.Error()))
		return exitError
	}
	ts, err := pilot.LoadTasks(tasks, from, to)
	if err != nil {
		fmt.Fprintln(stderr, "tack eval pilot:", err)
		return exitError
	}
	lockPath, err := eval.TimingLockPath()
	if err != nil {
		fmt.Fprintln(stderr, "tack eval pilot:", err)
		return exitError
	}
	lock, err := eval.AcquireTimingLock(lockPath, "tack eval pilot -oracle-check")
	if err != nil {
		fmt.Fprintln(stderr, "tack eval pilot:", err)
		return exitHeld
	}
	defer lock.Release()
	if err := os.MkdirAll(out, 0o755); err != nil {
		fmt.Fprintln(stderr, "tack eval pilot:", err)
		return exitError
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	passed, err := pilot.OracleCheck(ctx, ts, mirrors, out, tackRev, stderr)
	if err != nil {
		fmt.Fprintln(stderr, "tack eval pilot:", err)
		fmt.Fprintln(stdout, gate.Verdict(gate.Inconclusive, fmt.Sprintf("oracle check: stopped after %d passed: %v", passed, err)))
		return exitError
	}
	if passed != len(ts) {
		fmt.Fprintln(stdout, gate.Verdict(gate.Fail, fmt.Sprintf("oracle check: %d of %d tasks pass at their fix commit", passed, len(ts))))
		return exitError
	}
	fmt.Fprintln(stdout, gate.Verdict(gate.OK, fmt.Sprintf("oracle check: all %d tasks pass at their fix commit", len(ts))))
	return exitOK
}

// goOnPath refuses a job whose PATH has no `go`. The oracle grades every run with `go test`; without it every oracle
// test reports build-fail, and the job would record that as the model's result. nobara puts Go on the PATH only in
// interactive shells, so a job started over ssh or from a queue must set it itself.
func goOnPath() error {
	if _, err := exec.LookPath("go"); err != nil {
		return fmt.Errorf("`go` is not on this job's PATH (%v); the oracle grades every run with go test, and without it "+
			"every test would read build-fail. Start the job with Go on its PATH (on nobara: export PATH=/usr/local/go/bin:$PATH)", err)
	}
	return nil
}

// exitNotRun is a gated job that did not run because its gate refused (e0Gate).
const exitNotRun = 6

// e0Gate holds a pilot job to an E0 arm's smoke (eval.SmokeGate), before anything starts. A refusal writes out/NOT-RUN.txt
// with the reason and returns exitNotRun; a pass writes out/GATE.txt with the score and lets the job go on.
func e0Gate(results, arm, out string, stdout, stderr io.Writer) (int, bool) {
	if out == "" {
		fmt.Fprintln(stderr, "tack eval pilot: a gated job needs -out, where it records whether it ran")
		return exitUsage, false
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		fmt.Fprintln(stderr, "tack eval pilot:", err)
		return exitError, false
	}
	at := time.Now().Format(time.RFC3339)
	score, err := eval.SmokeGate(results, arm)
	if err != nil {
		_ = os.WriteFile(filepath.Join(out, "NOT-RUN.txt"), []byte(fmt.Sprintf("not run (%s): the E0 gate refused: %v\n", at, err)), 0o644)
		fmt.Fprintln(stderr, "tack eval pilot: not run: the E0 gate refused:", err)
		fmt.Fprintln(stdout, gate.Verdict(gate.Inconclusive, "eval pilot: not run: E0 gate: "+err.Error()))
		return exitNotRun, false
	}
	_ = os.WriteFile(filepath.Join(out, "GATE.txt"), []byte(fmt.Sprintf("ran (%s): arm %s in %s: %s\n", at, arm, results, score)), 0o644)
	fmt.Fprintf(stderr, "tack eval pilot: the E0 gate passed: arm %s, %s\n", arm, score)
	return 0, true
}
