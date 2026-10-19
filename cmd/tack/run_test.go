package main

import (
	"bytes"
	"flag"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/townsendmerino/tack/internal/kenrun"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
)

var update = flag.Bool("update", false, "re-record testdata/run-ken.json against the scripted model")

const kenQuestion = kenrun.Question

// kenScript is the model's side of the recorded run: grep for the definition, read it, answer. It is scripted (a
// stand-in for a model, through goinfertest's goinfer-framed server); re-recording against a real model with
// `tack run --record` replaces it without changing the test.
func kenScript() *goinfertest.Scripted {
	return goinfertest.NewScripted("qwen2.5-7b-instruct-q4_k_m", 16384,
		goinfertest.Calls("", "toolu_1", "grep", `{"pattern":"func applyTokenBudget"}`),
		goinfertest.Calls("", "toolu_2", "read", `{"path":"mcp/token_budget.go","offset":62,"limit":22}`),
		goinfertest.Says("`applyTokenBudget` is defined at mcp/token_budget.go:70. It returns the kept prefix of the "+
			"results, how many were dropped, and the approximate token total of the kept set."))
}

func writeConfig(t *testing.T, url string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	body := "state_dir = \"" + filepath.Join(dir, "state") + "\"\n[servers.local]\nkind = \"goinfer-remote\"\nurl = \"" + url + "\"\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestRunAnswersAQuestionAboutKenFromACassette is P0's exit check (DESIGN §16): tack run answers a question about ken
// from a cassette, through the whole --no-daemon path: the CLI, the protocol over a pipe, the core, the loop, and the
// real grep and read tools over a slice of ken. The replay is strict, so the test also fails if anything tack sends
// to the model changes: the system prompt, the tool schemas, or the tool results.
func TestRunAnswersAQuestionAboutKenFromACassette(t *testing.T) {
	cass := filepath.Join("testdata", "run-ken.json")
	ws := filepath.Join("testdata", "ken")
	var stdout, stderr bytes.Buffer
	var code int
	if *update {
		script := kenScript()
		ts := httptest.NewServer(script)
		defer ts.Close()
		code = dispatch([]string{"run", "--config", writeConfig(t, ts.URL), "--workspace", ws, "--record", cass, kenQuestion}, &stdout, &stderr)
		if script.Left() != 0 {
			t.Fatalf("%d scripted replies unused", script.Left())
		}
	} else {
		code = dispatch([]string{"run", "--config", writeConfig(t, "http://cassette.invalid"), "--workspace", ws, "--replay", cass, kenQuestion}, &stdout, &stderr)
	}
	if code != exitOK {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if why := kenrun.Check(stdout.String(), stderr.String()); why != nil {
		t.Fatalf("%s\nstdout:\n%s\nstderr:\n%s", strings.Join(why, "; "), stdout.String(), stderr.String())
	}
}

func TestRunJSONIsCleanEvents(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := dispatch([]string{"run", "--json", "--config", writeConfig(t, "http://cassette.invalid"), "--workspace",
		filepath.Join("testdata", "ken"), "--replay", filepath.Join("testdata", "run-ken.json"), kenQuestion}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if !strings.Contains(lines[len(lines)-1], `"type":"turn.end"`) {
		t.Fatalf("last line %q", lines[len(lines)-1])
	}
	for _, l := range lines {
		if !strings.HasPrefix(l, `{"seq":`) {
			t.Fatalf("stdout carries something other than events: %q", l)
		}
	}
}

func TestRunUsage(t *testing.T) {
	var out, errb bytes.Buffer
	if code := dispatch([]string{"run"}, &out, &errb); code != exitUsage {
		t.Fatalf("no question: exit %d", code)
	}
	if code := dispatch([]string{"frobnicate"}, &out, &errb); code != exitUsage {
		t.Fatalf("unknown command: exit %d", code)
	}
}

// TestRunFitWindowReachesTheLoop: [run] fit_window in config.toml reaches the loop through tack run and the core. With a
// window the scripted model has nearly filled, a long read comes back as the nearly-full note with the setting on, and
// as the plain capped output with it off.
func TestRunFitWindowReachesTheLoop(t *testing.T) {
	ws := t.TempDir()
	var big strings.Builder
	for i := 0; i < 300; i++ {
		big.WriteString("a line of a long file, long enough to count\n")
	}
	if err := os.WriteFile(filepath.Join(ws, "big.txt"), []byte(big.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(fit bool) string {
		t.Helper()
		// The scripted model reports 100 prompt and 20 output tokens; with max_tokens 256 and the 512-token margin, this
		// window leaves 100 tokens, under the 1 KiB minimum at any rate the loop allows.
		script := goinfertest.NewScripted("m", 100+20+256+512+100,
			goinfertest.Calls("", "toolu_1", "read", `{"path":"big.txt","offset":1,"limit":300}`), goinfertest.Says("ok"))
		ts := httptest.NewServer(script)
		defer ts.Close()
		cfg := writeConfig(t, ts.URL)
		f, err := os.OpenFile(cfg, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(f, "[run]\nmax_tokens = 256\nfit_window = %v\n", fit)
		f.Close()
		var stdout, stderr bytes.Buffer
		if code := dispatch([]string{"run", "--json", "--config", cfg, "--workspace", ws, "read big.txt"}, &stdout, &stderr); code != exitOK {
			t.Fatalf("fit_window %v: exit %d: %s", fit, code, stderr.String())
		}
		for _, l := range strings.Split(stdout.String(), "\n") {
			if strings.Contains(l, `"type":"tool.finished"`) {
				return l
			}
		}
		t.Fatalf("fit_window %v: no tool.finished in\n%s", fit, stdout.String())
		return ""
	}
	if on := run(true); !strings.Contains(on, "[window nearly full: this output") {
		t.Errorf("fit_window = true did not reach the loop: %.300s", on)
	}
	if off := run(false); strings.Contains(off, "window nearly full") || !strings.Contains(off, "a line of a long file") {
		t.Errorf("fit_window = false changed the result: %.300s", off)
	}
}
