package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/core"
	"github.com/townsendmerino/tack/internal/eval/pilot"
	"github.com/townsendmerino/tack/internal/kenrun"
	"github.com/townsendmerino/tack/internal/loop"
	"github.com/townsendmerino/tack/internal/sandbox"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
	"github.com/townsendmerino/tack/internal/shell"
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
	for range 300 {
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
		for l := range strings.SplitSeq(stdout.String(), "\n") {
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

// TestRunStopRepeatsExitsNine: [run] stop_repeats reaches the loop through tack run, and a turn the repeat rule ends
// exits with exitRepeatLoop. Off, the same model runs to its own answer and exits 0.
func TestRunStopRepeatsExitsNine(t *testing.T) {
	ws := t.TempDir()
	os.WriteFile(filepath.Join(ws, "a.go"), []byte("package a\n"), 0o644)
	run := func(on bool) int {
		t.Helper()
		var replies []goinfertest.Reply
		for i := 0; i <= 8; i++ {
			replies = append(replies, goinfertest.Calls("", fmt.Sprintf("g%d", i), "grep", `{"pattern":"nothing matches this"}`))
		}
		script := goinfertest.NewScripted("m", 16384, append(replies, goinfertest.Says("gave up"))...)
		ts := httptest.NewServer(script)
		defer ts.Close()
		cfg := writeConfig(t, ts.URL)
		f, _ := os.OpenFile(cfg, os.O_APPEND|os.O_WRONLY, 0)
		fmt.Fprintf(f, "[run]\nstop_repeats = %v\n", on)
		f.Close()
		var stdout, stderr bytes.Buffer
		return dispatch([]string{"run", "--json", "--config", cfg, "--workspace", ws, "find it"}, &stdout, &stderr)
	}
	if code := run(true); code != exitRepeatLoop {
		t.Errorf("stop_repeats = true: exit %d, want %d", code, exitRepeatLoop)
	}
	if code := run(false); code != exitOK {
		t.Errorf("stop_repeats = false: exit %d, want 0", code)
	}
}

// TestReplayRepeatsMatchesTheLiveRule: the same scripted loop through tack run, once with stop_repeats off and once
// on, with result_notes off and then on. Replaying the off run's events (pilot.ReplayRepeats) stops at the request
// where the live rule ended the on run, and counts every later request of the off run as saved; notes, whose repeat
// counts change every call, do not move the stop.
func TestReplayRepeatsMatchesTheLiveRule(t *testing.T) {
	for _, notes := range []bool{false, true} {
		replayMatchesLive(t, notes)
	}
}

func replayMatchesLive(t *testing.T, notes bool) {
	ws := t.TempDir()
	os.WriteFile(filepath.Join(ws, "a.go"), []byte("package a\n"), 0o644)
	run := func(on bool) string {
		t.Helper()
		replies := []goinfertest.Reply{goinfertest.Calls("", "r0", "read", `{"path":"a.go"}`)}
		for i := range 12 { // two calls a response: one repeats unchanged, one varies
			replies = append(replies, goinfertest.Calls("", fmt.Sprintf("g%d", i), "grep", `{"pattern":"nothing matches this"}`,
				fmt.Sprintf("v%d", i), "glob", fmt.Sprintf(`{"pattern":"*%d.go"}`, i)))
		}
		script := goinfertest.NewScripted("m", 16384, append(replies, goinfertest.Says("gave up"))...)
		ts := httptest.NewServer(script)
		defer ts.Close()
		cfg := writeConfig(t, ts.URL)
		f, _ := os.OpenFile(cfg, os.O_APPEND|os.O_WRONLY, 0)
		fmt.Fprintf(f, "[run]\nstop_repeats = %v\nresult_notes = %v\nmax_tool_calls = 100\n", on, notes)
		f.Close()
		var stdout, stderr bytes.Buffer
		dispatch([]string{"run", "--json", "--config", cfg, "--workspace", ws, "find it"}, &stdout, &stderr)
		p := filepath.Join(t.TempDir(), "events.jsonl")
		os.WriteFile(p, stdout.Bytes(), 0o644)
		return p
	}
	off, err := pilot.ReplayRepeats(run(false), nil)
	if err != nil {
		t.Fatal(err)
	}
	onPath := run(true)
	if b, _ := os.ReadFile(onPath); notes != strings.Contains(string(b), loop.GrepEmptyNote) {
		t.Fatalf("result_notes = %v, but the live events carry the empty-grep note: %v", notes, !notes)
	}
	on, err := pilot.ReplayRepeats(onPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !off.Stopped || off.StopRequest != on.Requests || off.StopTool != "grep" || off.Repeats != 8 {
		t.Fatalf("notes %v: replay of the off run %+v; the live rule ended the on run after request %d", notes, off, on.Requests)
	}
	if off.SavedRequests != off.Requests-on.Requests || off.SavedSecs < 0 {
		t.Fatalf("saved %d requests (%v s); the off run sent %d, the on run %d", off.SavedRequests, off.SavedSecs, off.Requests, on.Requests)
	}
}

// TestRunNoInstructionsOverridesWhole: [run] no_instructions keeps the workspace's CLAUDE.md out of the first message
// even under --instructions whole, as calibration §4.8's arm B needs; without it, the file is loaded whole.
func TestRunNoInstructionsOverridesWhole(t *testing.T) {
	ws := t.TempDir()
	os.WriteFile(filepath.Join(ws, "CLAUDE.md"), []byte("Rule: stage explicit paths.\n"), 0o644)
	first := func(none bool) string {
		t.Helper()
		ts := httptest.NewServer(goinfertest.NewScripted("m", 16384, goinfertest.Says("ok")))
		defer ts.Close()
		cfg := writeConfig(t, ts.URL)
		f, _ := os.OpenFile(cfg, os.O_APPEND|os.O_WRONLY, 0)
		fmt.Fprintf(f, "[run]\nno_instructions = %v\n", none)
		f.Close()
		var stdout, stderr bytes.Buffer
		if code := dispatch([]string{"run", "--json", "--instructions", "whole", "--config", cfg, "--workspace", ws, "hi"}, &stdout, &stderr); code != exitOK {
			t.Fatalf("no_instructions %v: exit %d: %s", none, code, stderr.String())
		}
		return strings.SplitN(stdout.String(), "\n", 2)[0]
	}
	if l := first(false); !strings.Contains(l, "Rule: stage explicit paths.") {
		t.Errorf("whole: the first message lacks CLAUDE.md: %s", l)
	}
	if l := first(true); strings.Contains(l, "Rule: stage explicit paths.") || strings.Contains(l, "Project instructions") {
		t.Errorf("no_instructions: the first message carries CLAUDE.md: %s", l)
	}
}

// TestPreflightClosesItsKit: the preflight's edit kit is closed when the preflight ends, pass or fail, so its sandbox
// temporary directory does not outlive it.
func TestPreflightClosesItsKit(t *testing.T) {
	for _, exit := range []int{0, 1} {
		closed := 0
		edit := func(root string, _ sandbox.Target) (core.EditKit, error) {
			return core.EditKit{Shell: fixedShell{exit: exit}, Close: func() error { closed++; return nil }}, nil
		}
		runPreflight(context.Background(), edit, t.TempDir(), "go version", io.Discard)
		if closed != 1 {
			t.Errorf("preflight exiting %d closed its kit %d times, want 1", exit, closed)
		}
	}
}

type fixedShell struct{ exit int }

func (s fixedShell) Run(context.Context, string, time.Duration) (shell.Result, error) {
	return shell.Result{Exit: s.exit, Output: "go version go1\n"}, nil
}

// TestRunInstructionsHead: [run] instructions_head loads the instruction file's budgeted head, not the whole file, under
// --instructions whole, framed as reference notes and placed before the statement, which ends the message under "Task:".
func TestRunInstructionsHead(t *testing.T) {
	ws := t.TempDir()
	os.WriteFile(filepath.Join(ws, "CLAUDE.md"), []byte("# notes\n\n## Rules\n\nStage explicit paths.\n\n## Tests\n\nRun the canary.\n"), 0o644)
	ts := httptest.NewServer(goinfertest.NewScripted("m", 16384, goinfertest.Says("ok")))
	defer ts.Close()
	cfg := writeConfig(t, ts.URL)
	f, _ := os.OpenFile(cfg, os.O_APPEND|os.O_WRONLY, 0)
	fmt.Fprint(f, "[run]\ninstructions_head = true\n")
	f.Close()
	var stdout, stderr bytes.Buffer
	if code := dispatch([]string{"run", "--json", "--instructions", "whole", "--config", cfg, "--workspace", ws, "hi"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	first, _, _ := strings.Cut(stdout.String(), "\n")
	if !strings.Contains(first, "Stage explicit paths.") || strings.Contains(first, "Run the canary.") || !strings.Contains(first, "## Tests") {
		t.Fatalf("the first message is not the head: %s", first)
	}
	var ev struct {
		Payload struct {
			Content []struct{ Text string }
		}
	}
	if err := json.Unmarshal([]byte(first), &ev); err != nil || len(ev.Payload.Content) != 1 {
		t.Fatalf("first event: %v %s", err, first)
	}
	if msg := ev.Payload.Content[0].Text; !strings.HasPrefix(msg, "Project notes from CLAUDE.md, for reference:\n\n") || !strings.HasSuffix(msg, "\n\nTask: hi") {
		t.Fatalf("the notes come first and the task last, labelled:\n%s", msg)
	}
}

// TestRunInjectContext: [run] inject_context puts the code ken finds for the statement into the first message, after the
// instruction head and before the task, which still ends the message; with it off, the message carries none.
func TestRunInjectContext(t *testing.T) {
	ws := t.TempDir()
	os.WriteFile(filepath.Join(ws, "CLAUDE.md"), []byte("# notes\n\nStage explicit paths.\n"), 0o644)
	os.WriteFile(filepath.Join(ws, "go.mod"), []byte("module m\n"), 0o644)
	os.MkdirAll(filepath.Join(ws, "pkg"), 0o755)
	os.WriteFile(filepath.Join(ws, "pkg", "a.go"), []byte("package pkg\n\n// Target is the code to fix.\nfunc Target() int { return 1 }\n"), 0o644)
	statement := "Fix `pkg.Target` so it returns 2."
	firstMessage := func(inject bool) string {
		ts := httptest.NewServer(goinfertest.NewScripted("m", 16384, goinfertest.Says("ok")))
		defer ts.Close()
		cfg := writeConfig(t, ts.URL)
		f, _ := os.OpenFile(cfg, os.O_APPEND|os.O_WRONLY, 0)
		fmt.Fprintf(f, "[run]\ninstructions_head = true\ninject_context = %v\n", inject)
		f.Close()
		var stdout, stderr bytes.Buffer
		if code := dispatch([]string{"run", "--json", "--instructions", "whole", "--config", cfg, "--workspace", ws, statement}, &stdout, &stderr); code != exitOK {
			t.Fatalf("exit %d: %s", code, stderr.String())
		}
		var ev struct {
			Payload struct {
				Content []struct{ Text string }
			}
		}
		first, _, _ := strings.Cut(stdout.String(), "\n")
		if err := json.Unmarshal([]byte(first), &ev); err != nil || len(ev.Payload.Content) != 1 {
			t.Fatalf("first event: %v %s", err, first)
		}
		return ev.Payload.Content[0].Text
	}
	msg := firstMessage(true)
	head, code, task := strings.Index(msg, "Stage explicit paths."), strings.Index(msg, "Code that may be relevant"), strings.Index(msg, "\n\nTask: ")
	if head < 0 || code < head || task < code || !strings.HasSuffix(msg, "\n\nTask: "+statement) ||
		!strings.Contains(msg, "pkg/a.go, lines 3-4:\n```go\n// Target is the code to fix.\nfunc Target() int { return 1 }\n```") {
		t.Fatalf("want the head, then the code, then the task:\n%s", msg)
	}
	if msg := firstMessage(false); strings.Contains(msg, "Code that may be relevant") || !strings.HasSuffix(msg, "\n\nTask: "+statement) {
		t.Fatalf("inject_context off still injects, or moves the task:\n%s", msg)
	}
}

// TestRunRepoMap: [run] repo_map puts the workspace's map into the first message after the instruction head; with
// inject_context too, the order is head, map, injected code, task; with it off, there is no map.
func TestRunRepoMap(t *testing.T) {
	ws := t.TempDir()
	os.WriteFile(filepath.Join(ws, "CLAUDE.md"), []byte("# notes\n\nStage explicit paths.\n"), 0o644)
	os.WriteFile(filepath.Join(ws, "go.mod"), []byte("module m\n"), 0o644)
	os.MkdirAll(filepath.Join(ws, "pkg"), 0o755)
	os.WriteFile(filepath.Join(ws, "pkg", "a.go"), []byte("package pkg\n\n// Target is the code to fix.\nfunc Target() int { return 1 }\n"), 0o644)
	statement := "Fix `pkg.Target` so it returns 2."
	firstMessage := func(settings string) string {
		ts := httptest.NewServer(goinfertest.NewScripted("m", 16384, goinfertest.Says("ok")))
		defer ts.Close()
		cfg := writeConfig(t, ts.URL)
		f, _ := os.OpenFile(cfg, os.O_APPEND|os.O_WRONLY, 0)
		fmt.Fprintf(f, "[run]\ninstructions_head = true\n%s", settings)
		f.Close()
		var stdout, stderr bytes.Buffer
		if code := dispatch([]string{"run", "--json", "--instructions", "whole", "--config", cfg, "--workspace", ws, statement}, &stdout, &stderr); code != exitOK {
			t.Fatalf("exit %d: %s", code, stderr.String())
		}
		var ev struct {
			Payload struct {
				Content []struct{ Text string }
			}
		}
		first, _, _ := strings.Cut(stdout.String(), "\n")
		if err := json.Unmarshal([]byte(first), &ev); err != nil || len(ev.Payload.Content) != 1 {
			t.Fatalf("first event: %v %s", err, first)
		}
		return ev.Payload.Content[0].Text
	}
	msg := firstMessage("repo_map = true\n")
	head, m, task := strings.Index(msg, "Stage explicit paths."), strings.Index(msg, "Repository map ("), strings.Index(msg, "\n\nTask: ")
	if head < 0 || m < head || task < m || !strings.Contains(msg, "\npkg/a.go: Target\n\nTask: ") || !strings.HasSuffix(msg, "Task: "+statement) {
		t.Fatalf("want the head, then the map, then the task:\n%s", msg)
	}
	msg = firstMessage("repo_map = true\ninject_context = true\n")
	m, code := strings.Index(msg, "Repository map ("), strings.Index(msg, "Code that may be relevant")
	if m < 0 || code < m || strings.Index(msg, "\n\nTask: ") < code {
		t.Fatalf("want the map before the injected code, and both before the task:\n%s", msg)
	}
	if msg := firstMessage(""); strings.Contains(msg, "Repository map (") {
		t.Fatalf("repo_map off still maps:\n%s", msg)
	}
}
