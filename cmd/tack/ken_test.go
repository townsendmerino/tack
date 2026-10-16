package main

import (
	"bytes"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
)

// TestRunWithKen runs tack run with [ken] configured (bm25, so no model directory is needed in CI): the session offers
// the search tool, the scripted model calls it, and ken's real engine, over a copy of the ken slice with the binary's
// tree-sitter chunker, answers with the definition's chunk, which reaches the model in the next request.
func TestRunWithKen(t *testing.T) {
	ws := filepath.Join(t.TempDir(), "ken")
	if err := os.CopyFS(ws, os.DirFS(filepath.Join("testdata", "ken"))); err != nil {
		t.Fatal(err)
	}
	script := goinfertest.NewScripted("m", 16384,
		goinfertest.Calls("", "toolu_1", "search", `{"query":"applyTokenBudget"}`),
		goinfertest.Says("It is in mcp/token_budget.go."))
	ts := httptest.NewServer(script)
	defer ts.Close()
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	os.WriteFile(cfg, []byte("state_dir = \""+filepath.Join(dir, "state")+"\"\n[servers.local]\nkind = \"goinfer-remote\"\nurl = \""+
		ts.URL+"\"\n[ken]\nmode = \"bm25\"\n"), 0o600)
	var stdout, stderr bytes.Buffer
	if code := dispatch([]string{"run", "--config", cfg, "--workspace", ws, "where is applyTokenBudget?"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("exit %d\n%s", code, stderr.String())
	}
	if len(script.Requests) != 2 {
		t.Fatalf("%d requests", len(script.Requests))
	}
	first, second := string(script.Requests[0]), string(script.Requests[1])
	if !strings.Contains(first, `"name":"search"`) {
		t.Fatal("the search tool was not offered")
	}
	if !strings.Contains(second, `mcp/token_budget.go:`) || !strings.Contains(second, `func applyTokenBudget`) {
		t.Fatalf("ken's hit did not reach the model:\n%s", second)
	}
	if !strings.Contains(stderr.String(), "· search") {
		t.Fatalf("stderr: %s", stderr.String())
	}
}
