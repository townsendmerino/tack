// Command recordrun records cmd/tack's `tack run` cassette against a real model: it starts goinfer-serve with the
// model (a supervised child with a random key), writes a goinfer-remote config pointing at it with a 0600 token file,
// and runs a pinned tack binary with --record over cmd/tack/testdata/ken. Recording through a remote config matters:
// the test replays with one, and a remote server is asked /v1/models first, so the cassette must hold that request.
//
// It loads the machine, so it runs through the night queue:
//
//	go run ./tools/recordrun -tack BIN -goinfer-serve BIN -model ~/models/qwen2.5-7b-instruct-q4_k_m.gguf \
//	    -backend metal -workspace <tack checkout>/cmd/tack/testdata/ken -out ~/tack-runs/cassettes/run-ken-7b.json
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/townsendmerino/tack/internal/models"
)

// question is the one TestRunAnswersAQuestionAboutKenFromACassette asks (cmd/tack/run_test.go:kenQuestion).
const question = "Which file defines applyTokenBudget, and what does it return?"

func main() {
	tack := flag.String("tack", "", "pinned tack binary")
	serve := flag.String("goinfer-serve", "", "goinfer-serve binary")
	model := flag.String("model", "", "checkpoint in ~/models")
	backend := flag.String("backend", "metal", "goinfer backend")
	ctxN := flag.Int("ctx", 16384, "served context")
	workspace := flag.String("workspace", "", "cmd/tack/testdata/ken of the checkout the cassette is for")
	out := flag.String("out", "", "cassette path to write")
	flag.Parse()
	if *tack == "" || *serve == "" || *model == "" || *workspace == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "usage: recordrun -tack BIN -goinfer-serve BIN -model FILE -workspace DIR -out FILE [-backend metal]")
		os.Exit(2)
	}
	if err := run(*tack, *serve, *model, *backend, *ctxN, *workspace, *out); err != nil {
		fmt.Fprintln(os.Stderr, "recordrun:", err)
		os.Exit(1)
	}
}

func run(tack, serve, model, backend string, ctxN int, workspace, out string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	dir, err := os.MkdirTemp("/tmp", "tkrec")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	sup, err := models.NewSupervisor(models.ChildConfig{Binary: serve, ModelPath: model,
		Model: "qwen2.5-7b-instruct-q4_k_m", Ctx: ctxN, Args: []string{"-backend", backend}, Dir: filepath.Join(dir, "serve"),
		IdleUnload: time.Hour, IdleExit: time.Hour})
	if err != nil {
		return err
	}
	defer sup.Stop()
	if _, err := sup.Ensure(ctx); err != nil {
		return err
	}
	url, key := sup.Endpoint()
	tok := filepath.Join(dir, "token")
	if err := os.WriteFile(tok, []byte(key), 0o600); err != nil {
		return err
	}
	cfg := filepath.Join(dir, "config.toml")
	body := fmt.Sprintf("state_dir = %q\n[servers.local]\nkind = \"goinfer-remote\"\nurl = %q\ntoken_file = %q\n",
		filepath.Join(dir, "state"), url, tok)
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, tack, "run", "--config", cfg, "--workspace", workspace, "--record", out, question)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	t0 := time.Now()
	err = cmd.Run()
	fmt.Fprintf(os.Stderr, "recordrun: tack run took %s (exit %v); cassette %s\n", time.Since(t0).Round(time.Second), err, out)
	return err
}
