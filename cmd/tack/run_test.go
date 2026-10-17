package main

import (
	"bytes"
	"flag"
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
