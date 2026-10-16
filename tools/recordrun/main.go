// Command recordrun records cmd/tack's `tack run` cassette against a real model: it starts goinfer-serve with the
// model (a supervised child with a random key), writes a goinfer-remote config pointing at it with a 0600 token file,
// and runs a pinned tack binary with --record over cmd/tack/testdata/ken, up to -attempts times, until the answer names
// mcp/token_budget.go. Every attempt's cassette and answer is kept, with a summary in <out>.attempts.json, so the
// commit adopting the cassette can say how many tries it took. Recording through a remote config matters:
// the test replays with one, and a remote server is asked /v1/models first, so the cassette must hold that request.
//
// It loads the machine, so it runs through the night queue:
//
//	go run ./tools/recordrun -tack BIN -goinfer-serve BIN -model ~/models/qwen2.5-7b-instruct-q4_k_m.gguf \
//	    -backend metal -workspace <tack checkout>/cmd/tack/testdata/ken -out ~/tack-runs/cassettes/run-ken-7b.json
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	attempts := flag.Int("attempts", 3, "tries before giving up; every try is kept and counted")
	flag.Parse()
	if *tack == "" || *serve == "" || *model == "" || *workspace == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "usage: recordrun -tack BIN -goinfer-serve BIN -model FILE -workspace DIR -out FILE [-backend metal]")
		os.Exit(2)
	}
	if err := run(*tack, *serve, *model, *backend, *ctxN, *workspace, *out, *attempts); err != nil {
		fmt.Fprintln(os.Stderr, "recordrun:", err)
		os.Exit(1)
	}
}

func run(tack, serve, model, backend string, ctxN int, workspace, out string, attempts int) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(attempts)*35*time.Minute)
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
	// Every attempt is kept, and the count is part of the record: picking the attempt that answered is a selection, and
	// the commit that adopts the cassette says how many it took.
	base := strings.TrimSuffix(out, ".json")
	var log []attempt
	for i := 1; i <= attempts; i++ {
		a := attempt{N: i, Cassette: fmt.Sprintf("%s.attempt%d.json", base, i)}
		var stdout strings.Builder
		cmd := exec.CommandContext(ctx, tack, "run", "--config", cfg, "--workspace", workspace, "--record", a.Cassette, question)
		cmd.Stdout, cmd.Stderr = io.MultiWriter(os.Stdout, &stdout), os.Stderr
		t0 := time.Now()
		err := cmd.Run()
		a.Secs, a.Answer = time.Since(t0).Seconds(), stdout.String()
		if err != nil {
			a.Err = err.Error()
		}
		a.NamesFile = err == nil && strings.Contains(a.Answer, wantFile)
		log = append(log, a)
		fmt.Fprintf(os.Stderr, "recordrun: attempt %d/%d took %.0f s, exit %v, names %s: %v\n", i, attempts, a.Secs, err, wantFile, a.NamesFile)
		if a.NamesFile {
			b, err := os.ReadFile(a.Cassette)
			if err != nil {
				return err
			}
			if err := os.WriteFile(out, b, 0o644); err != nil {
				return err
			}
			break
		}
	}
	b, _ := json.MarshalIndent(map[string]any{"question": question, "want": wantFile, "attempts": log}, "", "  ")
	if err := os.WriteFile(base+".attempts.json", b, 0o644); err != nil {
		return err
	}
	if !log[len(log)-1].NamesFile {
		return fmt.Errorf("no attempt of %d named %s; every cassette and answer is kept beside %s", attempts, wantFile, out)
	}
	return nil
}

// wantFile is what the exit test requires the answer to name.
const wantFile = "mcp/token_budget.go"

// attempt is one recording try.
type attempt struct {
	N         int     `json:"n"`
	Cassette  string  `json:"cassette"`
	Secs      float64 `json:"secs"`
	Answer    string  `json:"answer"`
	NamesFile bool    `json:"names_file"`
	Err       string  `json:"err,omitempty"`
}
