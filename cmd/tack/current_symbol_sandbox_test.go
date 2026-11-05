//go:build darwin && sandboxtest

package main

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
)

// TestCurrentSymbolWithoutKen: tack run --edit with [run] current_symbol and no [ken], end to end on macOS. tack offers
// neither search nor symbol, opens ken's index in bm25 mode at the first note, and the note carries the function's
// lines as the file has them, with the span ken gives it. The workspace is left as it was: no .ken snapshot.
func TestCurrentSymbolWithoutKen(t *testing.T) {
	ws, _ := filepath.EvalSymlinks(t.TempDir())
	if err := os.CopyFS(ws, os.DirFS(filepath.Join("testdata", "ken"))); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = ws
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return string(out)
	}
	git("init", "-q")
	git("add", "-A")
	git("commit", "-q", "-m", "base")
	const wrong = `{"path":"mcp/token_budget.go","old_string":"\tused, n := 0, 0;","new_string":"\tused, n := 0, 1"}`
	script := goinfertest.NewScripted("m", 32768,
		goinfertest.Calls("", "r1", "read", `{"path":"mcp/token_budget.go"}`),
		goinfertest.Calls("", "e1", "edit", wrong),
		goinfertest.Calls("", "r2", "read", `{"path":"mcp/token_budget.go"}`),
		goinfertest.Calls("", "e2", "edit", wrong),
		goinfertest.Calls("", "d1", "done", `{"reason":"none needed"}`),
		goinfertest.Calls("", "d2", "done", `{"reason":"none needed"}`))
	ts := httptest.NewServer(script)
	defer ts.Close()
	cfg := writeConfig(t, ts.URL)
	f, err := os.OpenFile(cfg, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("[run]\ncurrent_symbol = true\n")
	f.Close()
	var stdout, stderr bytes.Buffer
	code := dispatch([]string{"run", "--config", cfg, "--workspace", ws, "--edit", "--json", "Start n at 1."}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("exit %d\nstderr: %s\nstdout: %s", code, stderr.String(), stdout.String())
	}
	results := map[string]string{}
	for line := range strings.SplitSeq(strings.TrimSpace(stdout.String()), "\n") {
		var ev proto.Event
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		if v, _ := ev.Decode(); v != nil {
			if p, ok := v.(proto.ToolFinished); ok {
				results[p.ID] = p.Result
			}
		}
	}
	if strings.Contains(results["e1"], "[tack:") {
		t.Errorf("the first edit has a note: %q", results["e1"])
	}
	src, _ := os.ReadFile(filepath.Join(ws, "mcp", "token_budget.go"))
	lines := strings.Split(string(src), "\n")
	want := "[tack: the current text of applyTokenBudget in mcp/token_budget.go, lines 70-84, exactly as the file has it " +
		"now; copy old_string from it:\n" + strings.Join(lines[69:84], "\n") + "\n]"
	if !strings.HasSuffix(results["e2"], want) {
		t.Errorf("the second edit: %q", results["e2"])
	}
	for i, body := range script.Requests {
		if strings.Contains(string(body), `"name":"search"`) || strings.Contains(string(body), `"name":"symbol"`) {
			t.Errorf("request %d offers ken's tools", i)
		}
	}
	if !strings.Contains(stderr.String(), "tack: current_symbol: opened ken (bm25)") {
		t.Errorf("stderr: %s", stderr.String())
	}
	if s := git("status", "--porcelain", "--ignored"); s != "" {
		t.Errorf("the workspace changed: %q", s)
	}
}
