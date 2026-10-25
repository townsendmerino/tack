package loop

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/perms"
	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
	"github.com/townsendmerino/tack/internal/shell"
	"github.com/townsendmerino/tack/internal/tools"
)

// buildShell records every command and answers each with result.
type buildShell struct {
	ran    []string
	result shell.Result
}

func (b *buildShell) Run(_ context.Context, command string, _ time.Duration) (shell.Result, error) {
	b.ran = append(b.ran, command)
	return b.result, nil
}

// brokenBuild is go's output for a package that does not compile.
var brokenBuild = shell.Result{Exit: 1, Output: "# example.com/ken/mcp\nmcp/token_budget.go:3:30: undefined: x\n"}

// buildHarness is newHarness with edit and write, BuildAfterEdit on, a sandboxed engine in mode and sh as the shell,
// over a workspace that is a Go module.
func buildHarness(t *testing.T, mode proto.Mode, sh *buildShell, replies ...goinfertest.Reply) *harness {
	t.Helper()
	h := newHarness(t, Budget{}, replies...)
	os.WriteFile(filepath.Join(h.ws.Root(), "go.mod"), []byte("module example.com/ken\n"), 0o644)
	cfg := h.sess.cfg
	cfg.Tools = tools.New(tools.Options{Write: true})
	cfg.Mode = mode
	cfg.Perms = &perms.Engine{Root: h.ws.Root(), Path: os.Getenv("PATH"), Sandboxed: true}
	cfg.BuildAfterEdit, cfg.Shell = true, sh
	h.sess = New(cfg)
	return h
}

func editCalls(path, old, new string) []goinfertest.Reply {
	return []goinfertest.Reply{
		goinfertest.Calls("", "r1", "read", `{"path":"`+path+`"}`),
		goinfertest.Calls("", "e1", "edit", fmt.Sprintf(`{"path":%q,"old_string":%q,"new_string":%q}`, path, old, new)),
		goinfertest.Says("done"),
	}
}

// TestBuildAfterEditAppendsTheErrors: a successful edit of a .go file whose package does not build gets the compiler's
// lines in a note, without go's "# package" header; the command is the package's build, run from the module's root.
func TestBuildAfterEditAppendsTheErrors(t *testing.T) {
	sh := &buildShell{result: brokenBuild}
	h := buildHarness(t, proto.ModeAuto, sh, editCalls("mcp/token_budget.go", "func applyTokenBudget() {}", "func applyTokenBudget() { x }")...)
	h.sess.Turn(context.Background(), "change it")
	want := "go -C " + h.ws.Root() + " build -o /dev/null ./mcp"
	if len(sh.ran) != 1 || sh.ran[0] != want {
		t.Fatalf("ran %q, want %q", sh.ran, want)
	}
	f := h.finished("e1")
	if f.Result != "edited mcp/token_budget.go at line 3\n\n[tack: this change does not build (go build ./mcp):\nmcp/token_budget.go:3:30: undefined: x]" {
		t.Fatalf("edit result %q", f.Result)
	}
	if StripNotes(f.Result) != "edited mcp/token_budget.go at line 3" {
		t.Fatalf("StripNotes left %q", StripNotes(f.Result))
	}
	// The read was not followed by a build.
	if r := h.finished("r1"); strings.Contains(r.Result, "[tack:") {
		t.Fatalf("read result %q", r.Result)
	}
}

func TestBuildAfterEditIsSilentWhenItBuilds(t *testing.T) {
	sh := &buildShell{result: shell.Result{Exit: 0}}
	h := buildHarness(t, proto.ModeAuto, sh, editCalls("mcp/token_budget.go", "func applyTokenBudget() {}", "func applyTokenBudget() {}\n")...)
	h.sess.Turn(context.Background(), "change it")
	if len(sh.ran) != 1 || h.finished("e1").Result != "edited mcp/token_budget.go at line 3" {
		t.Fatalf("ran %q, result %q", sh.ran, h.finished("e1").Result)
	}
}

// TestBuildAfterEditOnlyWhereItApplies: no build for a file that is not Go, a failed edit, a session without the setting,
// a mode where the engine would ask for the command, or a file in no module.
func TestBuildAfterEditOnlyWhereItApplies(t *testing.T) {
	run := func(h *harness, sh *buildShell, what string) {
		t.Helper()
		h.sess.Turn(context.Background(), "change it")
		if len(sh.ran) != 0 {
			t.Errorf("%s: ran %q", what, sh.ran)
		}
		if strings.Contains(h.finished("e1").Result, "[tack:") {
			t.Errorf("%s: result %q", what, h.finished("e1").Result)
		}
	}
	sh := &buildShell{result: brokenBuild}
	h := buildHarness(t, proto.ModeAuto, sh, editCalls("notes.txt", "a", "b")...)
	os.WriteFile(filepath.Join(h.ws.Root(), "notes.txt"), []byte("a\n"), 0o644)
	run(h, sh, "a text file")

	sh = &buildShell{result: brokenBuild}
	run(buildHarness(t, proto.ModeAuto, sh, editCalls("mcp/token_budget.go", "not in the file", "x")...), sh, "a failed edit")

	sh = &buildShell{result: brokenBuild}
	h = buildHarness(t, proto.ModeAuto, sh, editCalls("mcp/token_budget.go", "func applyTokenBudget() {}", "func applyTokenBudget() { x }")...)
	cfg := h.sess.cfg
	cfg.BuildAfterEdit = false
	h.sess = New(cfg)
	run(h, sh, "the setting off")

	// In edits mode the edit runs, but the engine would ask for the build (no rule covers it), so tack does not run it.
	sh = &buildShell{result: brokenBuild}
	h = buildHarness(t, proto.ModeEdits, sh, editCalls("mcp/token_budget.go", "func applyTokenBudget() {}", "func applyTokenBudget() { x }")...)
	run(h, sh, "edits mode")
	if h.finished("e1").Outcome != proto.OutcomeOK {
		t.Fatalf("the edit itself did not run in edits mode: %+v", h.finished("e1"))
	}

	sh = &buildShell{result: brokenBuild}
	h = buildHarness(t, proto.ModeAuto, sh, editCalls("mcp/token_budget.go", "func applyTokenBudget() {}", "func applyTokenBudget() { x }")...)
	os.Remove(filepath.Join(h.ws.Root(), "go.mod"))
	run(h, sh, "no module")
}

// TestBuildAfterEditTestFileAndNestedModule: a _test.go file is compiled with its tests (go test -c), and a file in a
// nested module builds from that module's root.
func TestBuildAfterEditTestFileAndNestedModule(t *testing.T) {
	sh := &buildShell{result: shell.Result{Exit: 0}}
	h := buildHarness(t, proto.ModeAuto, sh, goinfertest.Calls("", "w1", "write", `{"path":"sub/pkg/x_test.go","content":"package pkg\n"}`),
		goinfertest.Says("done"))
	os.MkdirAll(filepath.Join(h.ws.Root(), "sub"), 0o755)
	os.WriteFile(filepath.Join(h.ws.Root(), "sub", "go.mod"), []byte("module sub\n"), 0o644)
	h.sess.Turn(context.Background(), "add a test")
	want := "go -C " + filepath.Join(h.ws.Root(), "sub") + " test -c -o /dev/null ./pkg"
	if len(sh.ran) != 1 || sh.ran[0] != want {
		t.Fatalf("ran %q, want %q", sh.ran, want)
	}
}

// TestBuildAfterEditIsNotAVerification: with VerifyDone, the automatic build does not count as the model's build or
// test, so done after an edit is still refused.
func TestBuildAfterEditIsNotAVerification(t *testing.T) {
	sh := &buildShell{result: shell.Result{Exit: 0}}
	h := buildHarness(t, proto.ModeAuto, sh,
		goinfertest.Calls("", "r1", "read", `{"path":"mcp/token_budget.go"}`),
		goinfertest.Calls("", "e1", "edit", `{"path":"mcp/token_budget.go","old_string":"func applyTokenBudget() {}","new_string":"func applyTokenBudget() {}\n"}`),
		goinfertest.Calls("", "d1", "done", `{"reason":"done"}`),
		goinfertest.Calls("", "d2", "done", `{"reason":"done"}`))
	cfg := h.sess.cfg
	cfg.Tools = tools.New(tools.Options{Write: true, Done: true})
	cfg.EditTask, cfg.VerifyDone, cfg.Changed = true, true, func() (bool, error) { return true, nil }
	h.sess = New(cfg)
	h.sess.Turn(context.Background(), "change it")
	if len(sh.ran) != 1 {
		t.Fatalf("ran %q", sh.ran)
	}
	if r := h.finished("d1").Result; !strings.HasPrefix(r, VerifyRefusal) {
		t.Fatalf("done after only the automatic build: %q", r)
	}
}

// TestBuildErrorsCapsTheLines: more than BuildErrorLines lines are cut with a count, long lines are cut, and the
// module's absolute path is taken out.
func TestBuildErrorsCapsTheLines(t *testing.T) {
	var out strings.Builder
	out.WriteString("# m/p\n")
	for i := 1; i <= 12; i++ {
		fmt.Fprintf(&out, "/abs/mod/p/a.go:%d:1: undefined: y%s\n", i, strings.Repeat("y", 250*(i/12)))
	}
	note := buildErrors("go build ./p", out.String(), "/abs/mod")
	lines := strings.Split(note, "\n")
	if lines[3] != "p/a.go:1:1: undefined: y" || !strings.HasSuffix(note, "\n… 2 more lines]") || len(lines) != 3+BuildErrorLines+1 {
		t.Fatalf("note:\n%s", note)
	}
	long := buildErrors("go build ./p", "p/a.go:1:1: "+strings.Repeat("z", 300)+"\n", "/abs/mod")
	if !strings.HasSuffix(long, "…]") || len([]rune(long)) > len(buildOpen)+40+buildLineRunes {
		t.Fatalf("a long line was not cut: %d runes", len([]rune(long)))
	}
}

// TestBuildErrorsReadsRealGoOutput: the note is built from what go itself prints for the command buildNote runs, on a
// module that does not compile; and a module that does is exit 0, which buildNote takes as silence.
func TestBuildErrorsReadsRealGoOutput(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go on PATH")
	}
	mod := t.TempDir()
	os.MkdirAll(filepath.Join(mod, "p"), 0o755)
	os.WriteFile(filepath.Join(mod, "go.mod"), []byte("module m\n\ngo 1.21\n"), 0o644)
	os.WriteFile(filepath.Join(mod, "p", "a.go"), []byte("package p\n\nfunc F() int { return x }\n"), 0o644)
	out, err := exec.Command("go", "-C", mod, "build", "-o", os.DevNull, "./p").CombinedOutput()
	if err == nil {
		t.Fatalf("a package that does not compile built: %s", out)
	}
	if note := buildErrors("go build ./p", string(out), mod); !strings.HasSuffix(note, "):\np/a.go:3:23: undefined: x]") {
		t.Fatalf("note from go's output %q:\n%s", out, note)
	}
	os.WriteFile(filepath.Join(mod, "p", "a.go"), []byte("package p\n\nfunc F() int { return 1 }\n"), 0o644)
	if out, err := exec.Command("go", "-C", mod, "build", "-o", os.DevNull, "./p").CombinedOutput(); err != nil {
		t.Fatalf("a package that compiles failed: %v %s", err, out)
	}
	if entries, _ := os.ReadDir(filepath.Join(mod, "p")); len(entries) != 1 {
		t.Fatalf("the build left files behind: %v", entries)
	}
}
