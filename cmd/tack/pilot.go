package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/townsendmerino/tack/internal/eval"
	"github.com/townsendmerino/tack/internal/eval/pilot"
	"github.com/townsendmerino/tack/internal/models"
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
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if (!*smoke && *tasks == "") || *mirrors == "" || *opencode == "" || *kenMCP == "" || *kenModel == "" || *serve == "" ||
		*modelPath == "" || *out == "" {
		fmt.Fprintln(stderr, "usage: tack eval pilot (-smoke | -tasks pilot.jsonl -from N -to M) -mirrors DIR -opencode BIN "+
			"-ken-mcp BIN -ken-model DIR -goinfer-serve BIN -model-path FILE -out DIR [-cap 10m]")
		return exitUsage
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a0, a0k := pilot.Arm{Name: "A0"}, pilot.Arm{Name: "A0k", KenMCP: *kenMCP, KenModel: *kenModel}
	var err error
	if *smoke {
		err = withServer(ctx, *serve, *modelPath, *ctxN, *out, stderr, func(srv pilot.Server, _ string) error {
			return runSmoke(ctx, *mirrors, *opencode, a0, a0k, srv, *out, *capFlag, stderr)
		})
	} else {
		var ts []pilot.Task
		if ts, err = pilot.LoadTasks(*tasks, *from, *to); err == nil {
			err = withServer(ctx, *serve, *modelPath, *ctxN, *out, stderr, func(srv pilot.Server, serveLog string) error {
				return pilot.RunPilot(ctx, pilot.Options{Tasks: ts, Mirrors: *mirrors, Opencode: *opencode, A0: a0, A0k: a0k,
					Server: srv, ServeLog: serveLog, Out: *out, Cap: *capFlag, KenWarmup: 15 * time.Minute, Log: stderr})
			})
		}
	}
	if err != nil {
		fmt.Fprintln(stderr, "tack eval pilot:", err)
		if errors.Is(err, eval.ErrHeld) {
			return exitHeld
		}
		return exitError
	}
	return exitOK
}

// withServer takes (or, under night.py, inherits) the timing lock, starts one supervised goinfer-serve with
// -log-requests, runs fn against it, and saves the server's log into out.
func withServer(ctx context.Context, serve, modelPath string, ctxN int, out string, log io.Writer, fn func(pilot.Server, string) error) error {
	lockPath, err := eval.TimingLockPath()
	if err != nil {
		return err
	}
	lock, err := eval.AcquireTimingLock(lockPath, "tack eval pilot")
	if err != nil {
		return fmt.Errorf("%w: %v", eval.ErrHeld, err)
	}
	defer lock.Release()
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	dir, err := os.MkdirTemp("/tmp", "tkpilot")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	sup, err := models.NewSupervisor(models.ChildConfig{Binary: serve, ModelPath: modelPath, Model: "qwen", Ctx: ctxN,
		Args: []string{"-backend", "cuda", "-kv-sessions", "1", "-log-requests"}, Dir: dir, IdleUnload: 24 * time.Hour, IdleExit: 24 * time.Hour})
	if err != nil {
		return err
	}
	serveLog := filepath.Join(dir, "goinfer-serve.log")
	defer func() {
		_ = sup.Stop()
		if b, err := os.ReadFile(serveLog); err == nil {
			_ = os.WriteFile(filepath.Join(out, "goinfer-serve.log"), b, 0o644)
		}
	}()
	t0 := time.Now()
	if _, err := sup.Ensure(ctx); err != nil {
		return err
	}
	plan, _ := sup.Plan()
	url, key := sup.Endpoint()
	logf(log, "pilot: server up in %s; %s", time.Since(t0).Round(time.Second), plan.FitLine)
	return fn(pilot.Server{BaseURL: url, APIKey: key, Model: "qwen", Context: ctxN}, serveLog)
}

func runSmoke(ctx context.Context, mirrors, opencode string, a0, a0k pilot.Arm, srv pilot.Server, out string, capD time.Duration, log io.Writer) error {
	start := time.Now()
	logf(log, "pilot smoke: start %s, cap %s per run", start.Format("15:04 MST"), capD)
	var results []pilot.Result
	kenIndex := map[string]float64{}
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
		logf(log, "pilot smoke: %s: opencode, cap %s", arm.Name, capD)
		r := pilot.RunOpencode(ctx, opencode, srv, arm, repo, smokeStatement, runDir, capD)
		_ = os.WriteFile(filepath.Join(runDir, "diff.patch"), []byte(pilot.Diff(repo, smokeBase)), 0o644)
		results = append(results, r)
		logf(log, "pilot smoke: %s: exit %d, cap hit %v, %.0f s %s", arm.Name, r.Exit, r.CapHit, r.WallSecs, r.Err)
	}
	b, _ := json.MarshalIndent(map[string]any{"statement": smokeStatement, "repo": smokeRepo, "base": smokeBase,
		"results": results, "ken_index_secs": kenIndex}, "", "  ")
	if err := os.WriteFile(filepath.Join(out, "summary.json"), b, 0o644); err != nil {
		return err
	}
	logf(log, "pilot smoke: done in %s (started %s)", time.Since(start).Round(time.Second), start.Format("15:04"))
	return nil
}
