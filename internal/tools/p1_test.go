package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/shell"
)

type memBlobs map[string][]byte

func (m memBlobs) Get(h string) ([]byte, error) {
	if b, ok := m[h]; ok {
		return b, nil
	}
	return nil, errors.New("no such blob")
}

type fixedAsker string

func (a fixedAsker) Ask(context.Context, string) (string, error) { return string(a), nil }

// full is the largest tool set, as the daemon will build it.
func full(blobs memBlobs, ask Asker) *Set {
	return New(Options{Searcher: fakeSearcher{}, Write: true, Shell: fakeShell{}, Blobs: blobs, Plan: true, Ask: ask})
}

// fakeShell answers by command: "fail" exits 3, "stop" is stopped by the run-time check, "quiet" prints nothing.
type fakeShell struct{}

func (fakeShell) Run(_ context.Context, cmd string, timeout time.Duration) (shell.Result, error) {
	switch cmd {
	case "fail":
		return shell.Result{Output: "boom\n", Exit: 3}, nil
	case "stop":
		return shell.Result{Exit: 126, Stopped: "/bin/curl was not approved"}, nil
	case "quiet":
		return shell.Result{}, nil
	case "timeout":
		return shell.Result{Output: timeout.String()}, nil
	}
	return shell.Result{Output: "ran " + cmd + "\n"}, nil
}

func call(t *testing.T, s *Set, ws *Workspace, name, input string) Result {
	t.Helper()
	tool, ok := s.Get(name)
	if !ok {
		t.Fatalf("no tool %s", name)
	}
	r, err := tool.Run(context.Background(), ws, json.RawMessage(input))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func read(t *testing.T, s *Set, ws *Workspace, path string) {
	t.Helper()
	if r := call(t, s, ws, "read", `{"path":"`+path+`"}`); r.IsError {
		t.Fatalf("read %s: %s", path, r.Text)
	}
}

func content(t *testing.T, ws *Workspace, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(ws.Root(), rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestEveryPathParameterSaysRepoRoot: a path-taking parameter's description says paths are relative to the repo root
// (docs/design/p1-notes.md, "Tool paths").
func TestEveryPathParameterSaysRepoRoot(t *testing.T) {
	for _, spec := range full(memBlobs{}, nil).Specs() {
		var schema struct {
			Properties map[string]struct {
				Description string `json:"description"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(spec.InputSchema, &schema); err != nil {
			t.Fatalf("%s: %v", spec.Name, err)
		}
		for _, param := range []string{"path", "pattern"} {
			if spec.Name == "grep" && param == "pattern" { // grep's pattern is a regular expression, not a path
				continue
			}
			if p, ok := schema.Properties[param]; ok && !strings.Contains(p.Description, "relative to the repo root") {
				t.Errorf("%s's %s does not say it is relative to the repo root: %q", spec.Name, param, p.Description)
			}
		}
	}
}

// TestPathErrorsGuideAndHideTheHost: a missing relative path, an escape and a secret, to every path-taking tool, get
// guidance and never the workspace's absolute path. The night recording of 2026-10-16 passed grep "ken", the repo's
// own name, and got a raw lstat error naming a host path.
func TestPathErrorsGuideAndHideTheHost(t *testing.T) {
	ws := workspace(t)
	s := full(memBlobs{}, nil)
	cases := []struct{ tool, input, want string }{
		{"grep", `{"pattern":"x","path":"ken"}`, "paths are relative to the repo root, use '.' for all of it"},
		{"read", `{"path":"ken/mcp/token_budget.go"}`, "paths are relative to the repo root, use '.' for all of it"},
		{"read", `{"path":"internal"}`, "is a directory; glob lists its files"},
		{"edit", `{"path":"ken/a.go","old_string":"a","new_string":"b"}`, "paths are relative to the repo root"},
		{"read", `{"path":"../x"}`, "outside the repository; paths are relative to the repo root"},
		{"write", `{"path":"../x.go","content":"x"}`, "outside the repository; paths are relative to the repo root"},
		{"edit", `{"path":"link.txt","old_string":"a","new_string":"b"}`, "outside the repository"},
		{"read", `{"path":".env"}`, "secrets deny-list"},
		{"write", `{"path":"certs/new.pem","content":"x"}`, "secrets deny-list"},
	}
	for _, tc := range cases {
		r := call(t, s, ws, tc.tool, tc.input)
		if !r.IsError || !strings.Contains(r.Text, tc.want) {
			t.Errorf("%s %s: %q, want guidance %q", tc.tool, tc.input, r.Text, tc.want)
		}
		if strings.Contains(r.Text, ws.Root()) {
			t.Errorf("%s %s names the host path: %q", tc.tool, tc.input, r.Text)
		}
	}
	if r := call(t, s, ws, "glob", `{"pattern":"ken/**/*.go"}`); !strings.Contains(r.Text, "relative to the repo root") {
		t.Errorf("glob with no match gave no guidance: %q", r.Text)
	}
}

func TestEdit(t *testing.T) {
	ws := workspace(t)
	s := full(memBlobs{}, nil)
	const f = "internal/a/a.go"
	if r := call(t, s, ws, "edit", `{"path":"`+f+`","old_string":"applyBudget","new_string":"apply"}`); !r.IsError ||
		!strings.Contains(r.Text, "read") {
		t.Fatalf("an unread file was edited: %q", r.Text)
	}
	read(t, s, ws, f)
	if r := call(t, s, ws, "edit", `{"path":"`+f+`","old_string":"nope","new_string":"x"}`); !r.IsError ||
		!strings.Contains(r.Text, "the edit may already have been made") || !strings.Contains(r.Text, "Read the file again") {
		t.Fatalf("missing old_string: the error must say what probably happened and what to do: %q", r.Text)
	}
	if r := call(t, s, ws, "edit", `{"path":"`+f+`","old_string":"func applyBudget() {}","new_string":"func applyBudget() int { return 1 }"}`); r.IsError ||
		r.Text != "edited internal/a/a.go at line 4" {
		t.Fatalf("edit: %q", r.Text)
	}
	if !strings.Contains(content(t, ws, f), "func applyBudget() int { return 1 }") {
		t.Fatal("the file was not changed")
	}
	// The model's own edit counts as seen: a second edit needs no new read.
	if r := call(t, s, ws, "edit", `{"path":"`+f+`","old_string":"return 1","new_string":"return 2"}`); r.IsError {
		t.Fatalf("second edit: %q", r.Text)
	}
	// Ambiguous, then replace_all.
	os.WriteFile(filepath.Join(ws.Root(), "dup.txt"), []byte("a\nb\na\n"), 0o644)
	read(t, s, ws, "dup.txt")
	if r := call(t, s, ws, "edit", `{"path":"dup.txt","old_string":"a","new_string":"c"}`); !r.IsError ||
		!strings.Contains(r.Text, "occurs 2 times") {
		t.Fatalf("ambiguous: %q", r.Text)
	}
	if r := call(t, s, ws, "edit", `{"path":"dup.txt","old_string":"a","new_string":"c","replace_all":true}`); r.IsError ||
		content(t, ws, "dup.txt") != "c\nb\nc\n" {
		t.Fatalf("replace_all: %q, file %q", r.Text, content(t, ws, "dup.txt"))
	}
	if r := call(t, s, ws, "edit", `{"path":".git/config","old_string":"core","new_string":"x"}`); !r.IsError ||
		!strings.Contains(r.Text, ".git") {
		t.Fatalf(".git was edited: %q", r.Text)
	}
}

// TestEditRefusesAnUnseenChange (DESIGN §7.5): a change outside tack to the part being edited makes edit refuse until
// the model reads again; a change elsewhere in the file does not.
func TestEditRefusesAnUnseenChange(t *testing.T) {
	ws := workspace(t)
	s := full(memBlobs{}, nil)
	p := filepath.Join(ws.Root(), "two.txt")
	os.WriteFile(p, []byte("alpha one\nbeta two\n"), 0o644)
	read(t, s, ws, "two.txt")
	os.WriteFile(p, []byte("alpha ONE\nbeta two\n"), 0o644) // someone else changed the alpha line
	if r := call(t, s, ws, "edit", `{"path":"two.txt","old_string":"alpha one","new_string":"x"}`); !r.IsError ||
		!strings.Contains(r.Text, "changed since you last read it") {
		t.Fatalf("an unseen change to the edited part was not refused: %q", r.Text)
	}
	if r := call(t, s, ws, "edit", `{"path":"two.txt","old_string":"alpha ONE","new_string":"x"}`); !r.IsError ||
		!strings.Contains(r.Text, "changed since you last read it") {
		t.Fatalf("editing text the model never saw was not refused: %q", r.Text)
	}
	if r := call(t, s, ws, "edit", `{"path":"two.txt","old_string":"beta two","new_string":"beta 2"}`); r.IsError ||
		content(t, ws, "two.txt") != "alpha ONE\nbeta 2\n" {
		t.Fatalf("a change elsewhere blocked the edit: %q", r.Text)
	}
}

func TestWrite(t *testing.T) {
	ws := workspace(t)
	s := full(memBlobs{}, nil)
	if r := call(t, s, ws, "write", `{"path":"pkg/new/new.go","content":"package new\n"}`); r.IsError ||
		r.Text != "created pkg/new/new.go (1 lines)" || content(t, ws, "pkg/new/new.go") != "package new\n" {
		t.Fatalf("create: %q", r.Text)
	}
	// Overwrite only after a read, and only if unchanged since.
	if r := call(t, s, ws, "write", `{"path":"go.mod","content":"module y\n"}`); !r.IsError || !strings.Contains(r.Text, "read it before") {
		t.Fatalf("an unread file was overwritten: %q", r.Text)
	}
	read(t, s, ws, "go.mod")
	os.WriteFile(filepath.Join(ws.Root(), "go.mod"), []byte("module z\n"), 0o644)
	if r := call(t, s, ws, "write", `{"path":"go.mod","content":"module y\n"}`); !r.IsError || !strings.Contains(r.Text, "changed since") {
		t.Fatalf("a file changed since the read was overwritten: %q", r.Text)
	}
	read(t, s, ws, "go.mod")
	if r := call(t, s, ws, "write", `{"path":"go.mod","content":"module y\n"}`); r.IsError || content(t, ws, "go.mod") != "module y\n" {
		t.Fatalf("overwrite after a read: %q", r.Text)
	}
	// A directory that is a symlink out of the workspace cannot be written through.
	outside := t.TempDir()
	os.Symlink(outside, filepath.Join(ws.Root(), "out"))
	if r := call(t, s, ws, "write", `{"path":"out/x.go","content":"x"}`); !r.IsError || !strings.Contains(r.Text, "outside the repository") {
		t.Fatalf("wrote through a symlink out: %q", r.Text)
	}
	if _, err := os.Stat(filepath.Join(outside, "x.go")); err == nil {
		t.Fatal("a file was created outside the workspace")
	}
	if r := call(t, s, ws, "write", `{"path":".git/hooks/pre-commit","content":"x"}`); !r.IsError {
		t.Fatalf(".git was written: %q", r.Text)
	}
}

func TestReadOutput(t *testing.T) {
	ws := workspace(t)
	var long strings.Builder
	for i := 1; i <= 450; i++ {
		long.WriteString("out\n")
	}
	s := full(memBlobs{"blob:abc": []byte(long.String())}, nil)
	r := call(t, s, ws, "read_output", `{"handle":"blob:abc"}`)
	if r.IsError || !strings.Contains(r.Text, "   200\tout") || !strings.Contains(r.Text, "showing 1-200. Continue with offset 201.") {
		t.Fatalf("first page: %q", r.Text[max(0, len(r.Text)-120):])
	}
	if r := call(t, s, ws, "read_output", `{"handle":"blob:abc","offset":401}`); r.IsError || strings.Contains(r.Text, "Continue") {
		t.Fatalf("last page: %q", r.Text)
	}
	if r := call(t, s, ws, "read_output", `{"handle":"blob:zzz"}`); !r.IsError || !strings.Contains(r.Text, "copy the handle exactly") {
		t.Fatalf("bad handle: %q", r.Text)
	}
}

func TestTodo(t *testing.T) {
	ws := workspace(t)
	s := full(memBlobs{}, nil)
	r := call(t, s, ws, "todo", `{"items":[{"text":"read","status":"done"},{"text":"edit","status":"in_progress"},{"text":"test","status":"pending"}]}`)
	if r.IsError || r.Text != "[x] read\n[>] edit\n[ ] test\n" {
		t.Fatalf("todo: %q", r.Text)
	}
	if r := call(t, s, ws, "todo", `{"items":[{"text":"a","status":"in_progress"},{"text":"b","status":"in_progress"}]}`); !r.IsError ||
		!strings.Contains(r.Text, "keep one in_progress") {
		t.Fatalf("two in progress: %q", r.Text)
	}
	if r := call(t, s, ws, "todo", `{"items":[{"text":"a","status":"started"}]}`); !r.IsError || !strings.Contains(r.Text, "use pending, in_progress or done") {
		t.Fatalf("bad status: %q", r.Text)
	}
}

func TestAskUser(t *testing.T) {
	ws := workspace(t)
	if r := call(t, full(memBlobs{}, nil), ws, "ask_user", `{"question":"which file?"}`); r.IsError ||
		!strings.Contains(r.Text, "no one is attached") {
		t.Fatalf("headless: %q", r.Text)
	}
	if r := call(t, full(memBlobs{}, fixedAsker("a.go")), ws, "ask_user", `{"question":"which file?"}`); r.Text != "a.go" {
		t.Fatalf("answered: %q", r.Text)
	}
}

// TestSetsByOption: write tools only with Write, read_output only with a blob store, todo and ask_user only with Plan.
func TestSetsByOption(t *testing.T) {
	names := func(s *Set) string {
		var n []string
		for _, sp := range s.Specs() {
			n = append(n, sp.Name)
		}
		return strings.Join(n, ",")
	}
	if got := names(NewSet(nil)); got != "read,glob,grep" {
		t.Fatalf("read-only set: %s", got)
	}
	if got := names(full(memBlobs{}, nil)); got != "search,read,glob,grep,edit,write,bash,read_output,todo,ask_user" {
		t.Fatalf("full set: %s", got)
	}
}

func TestBash(t *testing.T) {
	ws := workspace(t)
	s := full(memBlobs{}, nil)
	cases := []struct {
		input, want string
		isErr       bool
	}{
		{`{"command":"ls"}`, "ran ls\n", false},
		{`{"command":"quiet"}`, "(no output)\n", false},
		{`{"command":"fail"}`, "boom\n[exit status 3]\n", true},
		{`{"command":"stop"}`, "(no output)\nerror: stopped: /bin/curl was not approved\n", true},
		{`{"command":"timeout","timeout_ms":5000}`, "5s", false},
		{`{"command":"timeout","timeout_ms":99999999}`, "10m0s", false}, // capped
		{`{"command":"timeout"}`, "2m0s", false},                        // the default
	}
	for _, tc := range cases {
		if r := call(t, s, ws, "bash", tc.input); r.Text != tc.want || r.IsError != tc.isErr {
			t.Errorf("%s: %q (error %v), want %q (error %v)", tc.input, r.Text, r.IsError, tc.want, tc.isErr)
		}
	}
}

// The file tools never write inside .git, in any letter case: macOS's file system takes .GIT/config for .git/config.
func TestFileToolsNeverWriteInGitAnyCase(t *testing.T) {
	ws := workspace(t)
	s := full(memBlobs{}, nil)
	for _, p := range []string{".GIT/config", ".Git/hooks/pre-commit", "sub/.gIT/x"} {
		if r := call(t, s, ws, "write", `{"path":"`+p+`","content":"x"}`); !r.IsError {
			t.Errorf("write %s: %q", p, r.Text)
		}
	}
	if r := call(t, s, ws, "write", `{"path":"git.txt","content":"x"}`); r.IsError {
		t.Errorf("write git.txt, a near-miss: %q", r.Text)
	}
	if r := call(t, s, ws, "write", `{"path":".gitignore","content":"x"}`); r.IsError {
		t.Errorf("write .gitignore, a near-miss: %q", r.Text)
	}
}
