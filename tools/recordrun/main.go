// Command recordrun records a real model's cassette of `tack run`'s exit question: it starts goinfer-serve with the
// model (a supervised child with a random key), writes a goinfer-remote config pointing at it with a 0600 token file,
// and runs a pinned tack binary with --record over cmd/tack/testdata/ken, up to -attempts times, until an attempt passes
// the exit test. An attempt passes only if the recording run and a strict replay of its cassette, made the way the
// test makes it, both exit 0 and both pass kenrun.CheckModel, the assertions of cmd/tack's
// TestRunAnswersFromARealModelCassette: a recording that test would reject is a failed attempt. Every attempt's
// cassette, answer and reasons are kept, with a summary in <out>.attempts.json, so the commit adopting the cassette (as
// cmd/tack/testdata/run-ken-7b.json) can say how many tries it took. Recording through a remote config matters: the
// test replays with one, and a remote server is asked /v1/models first, so the cassette must hold that request.
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

	"github.com/townsendmerino/tack/internal/kenrun"
	"github.com/townsendmerino/tack/internal/models"
)

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
	// The replay's config is the test's: a remote server at an address that must never be reached.
	replayCfg := filepath.Join(dir, "replay.toml")
	body = fmt.Sprintf("state_dir = %q\n[servers.local]\nkind = \"goinfer-remote\"\nurl = \"http://cassette.invalid\"\n",
		filepath.Join(dir, "replay-state"))
	if err := os.WriteFile(replayCfg, []byte(body), 0o600); err != nil {
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
		t0 := time.Now()
		answer, trace, err := tackRun(ctx, tack, "--config", cfg, "--workspace", workspace, "--record", a.Cassette, kenrun.Question)
		a.Secs, a.Answer = time.Since(t0).Seconds(), answer
		a.Why = judge("recording", answer, trace, err)
		if a.Why == nil { // the test replays the cassette strictly; a recording that does not replay is no cassette
			answer, trace, err := tackRun(ctx, tack, "--config", replayCfg, "--workspace", workspace, "--replay", a.Cassette, kenrun.Question)
			a.Why = judge("replay", answer, trace, err)
		}
		a.Passes = a.Why == nil
		log = append(log, a)
		fmt.Fprintf(os.Stderr, "recordrun: attempt %d/%d took %.0f s, passes the exit test: %v %s\n", i, attempts, a.Secs, a.Passes, strings.Join(a.Why, "; "))
		if a.Passes {
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
	var facts []string
	for _, f := range kenrun.Facts {
		facts = append(facts, f.What)
	}
	b, _ := json.MarshalIndent(map[string]any{"question": kenrun.Question, "answer_must_state": facts,
		"and": "no claim that the function is missing; at least one successful tool call", "attempts": log}, "", "  ")
	if err := os.WriteFile(base+".attempts.json", b, 0o644); err != nil {
		return err
	}
	if !log[len(log)-1].Passes {
		return fmt.Errorf("no attempt of %d passes the exit test; every cassette, answer and reason is kept beside %s", attempts, out)
	}
	return nil
}

// tackRun runs the pinned tack binary's run command, echoing its output, and returns the answer (stdout) and the tool
// trace (stderr).
func tackRun(ctx context.Context, tack string, args ...string) (string, string, error) {
	var stdout, stderr strings.Builder
	cmd := exec.CommandContext(ctx, tack, append([]string{"run"}, args...)...)
	cmd.Stdout, cmd.Stderr = io.MultiWriter(os.Stdout, &stdout), io.MultiWriter(os.Stderr, &stderr)
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// judge is why one run fails the exit test, each reason prefixed with which run it was; nil when it passes.
func judge(which, answer, trace string, err error) []string {
	var why []string
	if err != nil {
		why = append(why, fmt.Sprintf("%s: exit: %v", which, err))
	}
	for _, w := range kenrun.CheckModel(answer, trace) {
		why = append(why, which+": "+w)
	}
	return why
}

// attempt is one recording try.
type attempt struct {
	N        int      `json:"n"`
	Cassette string   `json:"cassette"`
	Secs     float64  `json:"secs"`
	Answer   string   `json:"answer"`
	Passes   bool     `json:"passes_exit_test"`
	Why      []string `json:"why,omitempty"` // each failed assertion, of the recording run or of its replay
}
