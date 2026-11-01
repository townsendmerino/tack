package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func workspace(t *testing.T) *Workspace {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"go.mod":                  "module x\n",
		"cmd/tack/main.go":        "package main\n\nfunc main() {}\n",
		"internal/a/a.go":         "package a\n\n// Budget is applied here.\nfunc applyBudget() {}\n",
		"internal/a/a_test.go":    "package a\n",
		"internal/b/doc.go":       "package b\n",
		".env":                    "SECRET=1\n",
		"certs/server.pem":        "-----BEGIN-----\n",
		".git/config":             "[core]\n",
		"node_modules/x/index.js": "applyBudget\n",
	}
	var long strings.Builder
	for i := 1; i <= 250; i++ {
		fmt.Fprintf(&long, "line %d\n", i)
	}
	files["long.txt"] = long.String()
	for rel, body := range files {
		p := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o644)
	}
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("outside\n"), 0o644)
	os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "link.txt"))
	ws, err := NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

func run(t *testing.T, ws *Workspace, name, input string) Result {
	t.Helper()
	tool, ok := NewSet(nil).Get(name)
	if !ok {
		t.Fatalf("no tool %s", name)
	}
	r, err := tool.Run(context.Background(), ws, json.RawMessage(input))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestConfinementAndSecrets(t *testing.T) {
	ws := workspace(t)
	for _, in := range []string{
		`{"path":"../x"}`, `{"path":"/etc/hosts"}`, `{"path":"link.txt"}`, // outside, absolute, symlink out
		`{"path":".env"}`, `{"path":"certs/server.pem"}`, // secrets
	} {
		if r := run(t, ws, "read", in); !r.IsError {
			t.Errorf("read %s was allowed: %q", in, r.Text)
		}
	}
	for _, name := range []string{"glob", "grep"} {
		in := `{"pattern":"**/*"}`
		if name == "grep" {
			in = `{"pattern":"SECRET|BEGIN|core|outside"}`
		}
		if r := run(t, ws, name, in); strings.Contains(r.Text, ".env") || strings.Contains(r.Text, ".pem") ||
			strings.Contains(r.Text, ".git/") || strings.Contains(r.Text, "link.txt") {
			t.Errorf("%s exposed a secret, .git or a symlink out:\n%s", name, r.Text)
		}
	}
}

func TestRead(t *testing.T) {
	ws := workspace(t)
	r := run(t, ws, "read", `{"path":"internal/a/a.go"}`)
	if r.IsError || !strings.Contains(r.Text, "     3\t// Budget is applied here.") {
		t.Fatalf("read: %q", r.Text)
	}
	r = run(t, ws, "read", `{"path":"long.txt"}`)
	if strings.Contains(r.Text, "line 201") || !strings.Contains(r.Text, "has 250 lines; showing 1-200") {
		t.Fatalf("a long file was not windowed:\n%s", r.Text[len(r.Text)-200:])
	}
	r = run(t, ws, "read", `{"path":"long.txt","offset":240,"limit":3}`)
	if r.Text != "   240\tline 240\n   241\tline 241\n   242\tline 242\n" {
		t.Fatalf("range read: %q", r.Text)
	}
	if r := run(t, ws, "read", `{"path":"nope.go"}`); !r.IsError {
		t.Fatal("missing file was not an error")
	}
	if r := run(t, ws, "read", `{"file":"x"}`); !r.IsError {
		t.Fatal("unknown input field was accepted")
	}
}

func TestGlob(t *testing.T) {
	ws := workspace(t)
	cases := map[string]string{
		`*.go`:                  "cmd/tack/main.go\ninternal/a/a.go\ninternal/a/a_test.go\ninternal/b/doc.go\n",
		`internal/*/doc.go`:     "internal/b/doc.go\n",
		`internal/**/*_test.go`: "internal/a/a_test.go\n",
		`cmd/**`:                "cmd/tack/main.go\n",
	}
	for pat, want := range cases {
		if r := run(t, ws, "glob", `{"pattern":"`+pat+`"}`); r.Text != want {
			t.Errorf("glob %s = %q, want %q", pat, r.Text, want)
		}
	}
}

func TestGrep(t *testing.T) {
	ws := workspace(t)
	r := run(t, ws, "grep", `{"pattern":"func applyBudget"}`)
	if r.Text != "internal/a/a.go:4:func applyBudget() {}\n" {
		t.Fatalf("grep = %q (node_modules must be skipped)", r.Text)
	}
	if r := run(t, ws, "grep", `{"pattern":"package","glob":"*_test.go"}`); r.Text != "internal/a/a_test.go:1:package a\n" {
		t.Fatalf("grep with glob = %q", r.Text)
	}
	if r := run(t, ws, "grep", `{"pattern":"package","path":"internal/b"}`); r.Text != "internal/b/doc.go:1:package b\n" {
		t.Fatalf("grep with path = %q", r.Text)
	}
	if r := run(t, ws, "grep", `{"pattern":"line \\d+"}`); !strings.Contains(r.Text, "stopped at 100 matches") {
		t.Fatalf("grep did not cap its matches")
	}
	if r := run(t, ws, "grep", `{"pattern":"("}`); !r.IsError {
		t.Fatal("bad regexp was not an error")
	}
}

type fakeSearcher struct{}

func (fakeSearcher) Search(context.Context, string, int) ([]Hit, error) {
	return []Hit{{File: "internal/a/a.go", StartLine: 3, EndLine: 4, Text: "// Budget is applied here.\nfunc applyBudget() {}"}}, nil
}

func TestSearchOnlyWithASearcher(t *testing.T) {
	if _, ok := NewSet(nil).Get("search"); ok {
		t.Fatal("search offered with no searcher")
	}
	s := NewSet(fakeSearcher{})
	if s.Specs()[0].Name != "search" {
		t.Fatal("search is not first")
	}
	tool, _ := s.Get("search")
	r, _ := tool.Run(context.Background(), nil, json.RawMessage(`{"query":"budget"}`))
	if !strings.HasPrefix(r.Text, "internal/a/a.go:3-4\n") {
		t.Fatalf("search = %q", r.Text)
	}
}

// TestSearchesMarkEmptyResults: glob and grep set Empty when nothing matched, and only then; their text is unchanged.
func TestSearchesMarkEmptyResults(t *testing.T) {
	ws := workspace(t)
	for _, c := range []struct {
		tool, input string
		empty       bool
	}{
		{"glob", `{"pattern":"**/*.nothing"}`, true},
		{"grep", `{"pattern":"zzz_no_such_text_zzz"}`, true},
		{"glob", `{"pattern":"**/*.go"}`, false},
		{"grep", `{"pattern":"package"}`, false},
	} {
		r := run(t, ws, c.tool, c.input)
		if r.Empty != c.empty {
			t.Errorf("%s %s: empty %v (text %.60q); want empty %v", c.tool, c.input, r.Empty, r.Text, c.empty)
		}
	}
}

// TestEditAndWriteReportTheFileState: edit and write set Result.State to ContentState of the file after the call,
// failed calls included, and "" where there is no file; the read-only tools leave it empty.
func TestEditAndWriteReportTheFileState(t *testing.T) {
	ws := workspace(t)
	run := func(t *testing.T, ws *Workspace, name, input string) Result {
		tool, _ := New(Options{Write: true}).Get(name)
		r, err := tool.Run(context.Background(), ws, json.RawMessage(input))
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	state := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(ws.root, rel))
		if err != nil {
			t.Fatal(err)
		}
		return ContentState(string(b))
	}
	if r := run(t, ws, "read", `{"path":"internal/b/doc.go"}`); r.State != "" {
		t.Errorf("read set a state: %q", r.State)
	}
	if r := run(t, ws, "edit", `{"path":"internal/b/doc.go","old_string":"package b","new_string":"package b // x"}`); r.IsError ||
		r.State != state("internal/b/doc.go") || r.State != ContentState("package b // x\n") {
		t.Errorf("edit: %+v", r)
	}
	if r := run(t, ws, "edit", `{"path":"internal/b/doc.go","old_string":"absent","new_string":"y"}`); !r.IsError ||
		r.State != ContentState("package b // x\n") {
		t.Errorf("failed edit: %+v", r)
	}
	if r := run(t, ws, "write", `{"path":"internal/b/new.go","content":"package b\n"}`); r.IsError || r.State != ContentState("package b\n") {
		t.Errorf("write: %+v", r)
	}
	if r := run(t, ws, "edit", `{"path":"internal/b/none.go","old_string":"a","new_string":"b"}`); !r.IsError || r.State != "" {
		t.Errorf("edit of a missing file: %+v", r)
	}
}

// LeanResults: a whole read stops at 80 lines and grep at 30 matches, each with its hint, and the read tool's
// description says 80; without it, 200 and 100. An explicit range is not cut.
func TestLeanResults(t *testing.T) {
	root := t.TempDir()
	var b strings.Builder
	for i := 1; i <= 300; i++ {
		fmt.Fprintf(&b, "match %d\n", i)
	}
	os.WriteFile(filepath.Join(root, "big.txt"), []byte(b.String()), 0o644)
	ws, err := NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	lines := func(s string) int { return strings.Count(s, "\tmatch ") }
	for _, c := range []struct {
		lean               bool
		read, grep         int
		readHint, grepHint string
	}{
		{true, 80, 30, "showing 1-80.", "stopped at 30 matches"},
		{false, 200, 100, "showing 1-200.", "stopped at 100 matches"},
	} {
		set := New(Options{LeanResults: c.lean})
		call := func(name, input string) string {
			tool, _ := set.Get(name)
			r, err := tool.Run(context.Background(), ws, json.RawMessage(input))
			if err != nil {
				t.Fatal(err)
			}
			return r.Text
		}
		if r := call("read", `{"path":"big.txt"}`); lines(r) != c.read || !strings.Contains(r, c.readHint) {
			t.Errorf("lean %v: a whole read gave %d lines, hint %v", c.lean, lines(r), strings.Contains(r, c.readHint))
		}
		if r := call("read", `{"path":"big.txt","offset":1,"limit":150}`); lines(r) != 150 {
			t.Errorf("lean %v: an explicit range of 150 gave %d lines", c.lean, lines(r))
		}
		if r := call("grep", `{"pattern":"match"}`); strings.Count(r, "big.txt:") != c.grep || !strings.Contains(r, c.grepHint) {
			t.Errorf("lean %v: grep gave %d matches, hint %v", c.lean, strings.Count(r, "big.txt:"), strings.Contains(r, c.grepHint))
		}
		read, _ := set.Get("read")
		if d := read.Spec().Description; !strings.Contains(d, fmt.Sprintf("over %d lines", c.read)) {
			t.Errorf("lean %v: read's description %q", c.lean, d)
		}
		if got := (Options{LeanResults: c.lean}).WholeReadLines(); got != c.read {
			t.Errorf("lean %v: WholeReadLines %d", c.lean, got)
		}
	}
}

// A zero-valued readTool or grepTool, as a test or a later caller might build one, uses the defaults: 200 lines and
// 100 matches, not 0.
func TestZeroValuedToolsUseTheDefaults(t *testing.T) {
	root := t.TempDir()
	var b strings.Builder
	for i := 1; i <= 300; i++ {
		fmt.Fprintf(&b, "match %d\n", i)
	}
	os.WriteFile(filepath.Join(root, "big.txt"), []byte(b.String()), 0o644)
	ws, err := NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := readTool{}.Run(context.Background(), ws, json.RawMessage(`{"path":"big.txt"}`))
	if n := strings.Count(r.Text, "\tmatch "); n != readMaxLines || !strings.Contains(readTool{}.Spec().Description, "over 200 lines") {
		t.Errorf("a zero readTool read %d lines; description %q", n, readTool{}.Spec().Description)
	}
	g, _ := grepTool{}.Run(context.Background(), ws, json.RawMessage(`{"pattern":"match"}`))
	if n := strings.Count(g.Text, "big.txt:"); n != grepMaxMatches {
		t.Errorf("a zero grepTool gave %d matches", n)
	}
}

// WithWiden: level 1 shows 3 times a whole read's and a grep's cap, level 2 no cap; an explicit range is never
// widened; Widenable names the calls it applies to.
func TestWithWiden(t *testing.T) {
	root := t.TempDir()
	var b strings.Builder
	for i := 1; i <= 700; i++ {
		fmt.Fprintf(&b, "match %d\n", i)
	}
	os.WriteFile(filepath.Join(root, "big.txt"), []byte(b.String()), 0o644)
	ws, _ := NewWorkspace(root)
	set := New(Options{LeanResults: true})
	call := func(level int, name, input string) string {
		tool, _ := set.Get(name)
		r, err := tool.Run(WithWiden(context.Background(), level), ws, json.RawMessage(input))
		if err != nil {
			t.Fatal(err)
		}
		return r.Text
	}
	for level, want := range map[int][3]int{0: {80, 30, 50}, 1: {240, 90, 50}, 2: {700, 700, 50}} {
		got := [3]int{strings.Count(call(level, "read", `{"path":"big.txt"}`), "\tmatch "),
			strings.Count(call(level, "grep", `{"pattern":"match"}`), "big.txt:"),
			strings.Count(call(level, "read", `{"path":"big.txt","offset":1,"limit":50}`), "\tmatch ")}
		if got != want {
			t.Errorf("level %d: read, grep, range %v, want %v", level, got, want)
		}
	}
	for in, want := range map[string]bool{`{"path":"a"}`: true, `{"path":"a","offset":3}`: false, `{"path":"a","limit":9}`: false} {
		if Widenable("read", []byte(in)) != want {
			t.Errorf("Widenable(read, %s) != %v", in, want)
		}
	}
	if !Widenable("grep", []byte(`{"pattern":"x"}`)) || Widenable("glob", []byte(`{"pattern":"x"}`)) {
		t.Error("Widenable: grep yes, glob no")
	}
}
