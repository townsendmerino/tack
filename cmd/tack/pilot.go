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

// pilotCmd is `tack eval pilot`. With -smoke it runs the smoke task once per arm: a night job that learns opencode's
// event stream and checks the server, opencode and ken-mcp configuration before the pilot's timed runs.
func pilotCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("eval pilot", flag.ContinueOnError)
	fs.SetOutput(stderr)
	smoke := fs.Bool("smoke", false, "run the smoke task once per arm")
	mirrors := fs.String("mirrors", "", "directory of bare mirrors, <repo>.git")
	opencode := fs.String("opencode", "", "the opencode binary")
	kenMCP := fs.String("ken-mcp", "", "the ken-mcp binary (A0k)")
	kenModel := fs.String("ken-model", "", "ken's embedding model directory")
	serve := fs.String("goinfer-serve", "", "the goinfer-serve binary")
	modelPath := fs.String("model-path", "", "the checkpoint, from ~/models")
	ctxN := fs.Int("ctx", 16384, "the served context")
	out := fs.String("out", "", "results directory (durable)")
	capFlag := fs.Duration("cap", 2*time.Minute, "wall-time cap per run")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if !*smoke || *mirrors == "" || *opencode == "" || *kenMCP == "" || *kenModel == "" || *serve == "" || *modelPath == "" || *out == "" {
		fmt.Fprintln(stderr, "usage: tack eval pilot -smoke -mirrors DIR -opencode BIN -ken-mcp BIN -ken-model DIR -goinfer-serve BIN -model-path FILE -out DIR [-cap 2m]")
		return exitUsage
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runSmoke(ctx, *mirrors, *opencode, *kenMCP, *kenModel, *serve, *modelPath, *ctxN, *out, *capFlag, stderr); err != nil {
		fmt.Fprintln(stderr, "tack eval pilot:", err)
		if errors.Is(err, eval.ErrHeld) {
			return exitHeld
		}
		return exitError
	}
	return exitOK
}

func runSmoke(ctx context.Context, mirrors, opencode, kenMCP, kenModel, serve, modelPath string, ctxN int, out string, capD time.Duration, log io.Writer) error {
	lockPath, err := eval.TimingLockPath()
	if err != nil {
		return err
	}
	lock, err := eval.AcquireTimingLock(lockPath, "tack eval pilot -smoke")
	if err != nil {
		return fmt.Errorf("%w: %v", eval.ErrHeld, err)
	}
	defer lock.Release()
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	start := time.Now()
	logf(log, "pilot smoke: start %s, cap %s per run", start.Format("15:04 MST"), capD)
	dir, err := os.MkdirTemp("/tmp", "tkpilot")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	sup, err := models.NewSupervisor(models.ChildConfig{Binary: serve, ModelPath: modelPath, Model: "qwen", Ctx: ctxN,
		Args: []string{"-backend", "cuda", "-log-requests"}, Dir: dir, IdleUnload: 24 * time.Hour, IdleExit: 24 * time.Hour})
	if err != nil {
		return err
	}
	defer func() {
		_ = sup.Stop()
		if b, err := os.ReadFile(filepath.Join(dir, "goinfer-serve.log")); err == nil {
			_ = os.WriteFile(filepath.Join(out, "goinfer-serve.log"), b, 0o644)
		}
	}()
	if _, err := sup.Ensure(ctx); err != nil {
		return err
	}
	plan, _ := sup.Plan()
	url, key := sup.Endpoint()
	srv := pilot.Server{BaseURL: url, APIKey: key, Model: "qwen", Context: ctxN}
	logf(log, "pilot smoke: server up in %s; %s", time.Since(start).Round(time.Second), plan.FitLine)
	var results []pilot.Result
	for _, arm := range []pilot.Arm{{Name: "A0"}, {Name: "A0k", KenMCP: kenMCP, KenModel: kenModel}} {
		runDir := filepath.Join(out, arm.Name)
		repo := filepath.Join(runDir, "repo")
		if err := pilot.PrepareRepo(filepath.Join(mirrors, smokeRepo+".git"), smokeBase, repo); err != nil {
			return fmt.Errorf("%s: %w", arm.Name, err)
		}
		logf(log, "pilot smoke: %s: opencode, cap %s", arm.Name, capD)
		r := pilot.RunOpencode(ctx, opencode, srv, arm, repo, smokeStatement, runDir, capD)
		_ = os.WriteFile(filepath.Join(runDir, "diff.patch"), []byte(pilot.Diff(repo)), 0o644)
		results = append(results, r)
		logf(log, "pilot smoke: %s: exit %d, cap hit %v, %.0f s %s", arm.Name, r.Exit, r.CapHit, r.WallSecs, r.Err)
	}
	b, _ := json.MarshalIndent(map[string]any{"statement": smokeStatement, "repo": smokeRepo, "base": smokeBase,
		"fit_line": plan.FitLine, "results": results}, "", "  ")
	if err := os.WriteFile(filepath.Join(out, "summary.json"), b, 0o644); err != nil {
		return err
	}
	logf(log, "pilot smoke: done in %s (started %s)", time.Since(start).Round(time.Second), start.Format("15:04"))
	return nil
}
