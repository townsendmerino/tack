package core

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/perms"
	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/sandbox"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
	"github.com/townsendmerino/tack/internal/shell"
)

type noShell struct{}

func (noShell) Run(context.Context, string, time.Duration) (shell.Result, error) {
	return shell.Result{Output: "ok\n"}, nil
}

// gitWorkspace is a committed repository with one Go file and, when instructions is set, a CLAUDE.md.
func gitWorkspace(t *testing.T, instructions string) string {
	t.Helper()
	d := filepath.Join(t.TempDir(), "repo")
	os.MkdirAll(d, 0o755)
	os.WriteFile(filepath.Join(d, "a.go"), []byte("package a\n"), 0o644)
	if instructions != "" {
		os.WriteFile(filepath.Join(d, "CLAUDE.md"), []byte(instructions), 0o644)
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"commit", "-q", "-m", "base"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = d
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	return d
}

func (f *fixture) sessionIn(t *testing.T, ws string, mode proto.Mode) (proto.SessionID, error) {
	var r proto.SessionNewResult
	err := f.client.Call(proto.MethodSessionNew, proto.SessionNewParams{Workspace: ws, Model: "main", Mode: mode}, &r)
	return r.Session, err
}

// TestAutoModeNeedsTheSandbox: with no edit kit, a session asking for auto mode is refused.
func TestAutoModeNeedsTheSandbox(t *testing.T) {
	f := newFixture(t)
	if _, err := f.sessionIn(t, gitWorkspace(t, ""), proto.ModeAuto); err == nil || !strings.Contains(err.Error(), "no sandbox") {
		t.Fatalf("auto mode without an edit kit: %v", err)
	}
}

// TestEditTaskThroughTheCore: an auto-mode edit task writes a file with the write tool and finishes through done, every
// request requiring a tool call; the instruction file joins the first message only.
func TestEditTaskThroughTheCore(t *testing.T) {
	f := newFixture(t,
		goinfertest.Calls("", "w1", "write", `{"path":"b.go","content":"package a\n\nfunc B() {}\n"}`),
		goinfertest.Calls("", "d1", "done", `{"reason":"added B"}`))
	ws := gitWorkspace(t, "Run go test before you finish.\n")
	var kitRoot string
	f.core.opt.Edit = func(root string, _ sandbox.Target) (EditKit, error) {
		kitRoot = root
		return EditKit{Shell: noShell{}, Engine: &perms.Engine{Root: root, Sandboxed: true}}, nil
	}
	f.core.opt.EditTask, f.core.opt.WholeInstructions = true, true
	sid, err := f.sessionIn(t, ws, proto.ModeAuto)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.client.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: sid}, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.client.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: sid,
		Content: []proto.ContentBlock{proto.Text("Add func B.")}}, nil); err != nil {
		t.Fatal(err)
	}
	evs := f.waitTurnEnd(t)
	if v, _ := evs[len(evs)-1].Decode(); v.(proto.TurnEnd).StopReason != proto.StopDone {
		t.Fatalf("turn.end %+v", v)
	}
	if b, err := os.ReadFile(filepath.Join(ws, "b.go")); err != nil || !strings.Contains(string(b), "func B()") {
		t.Fatalf("b.go: %q %v", b, err)
	}
	if real, _ := filepath.EvalSymlinks(ws); kitRoot != ws && kitRoot != real {
		t.Errorf("edit kit built for %q, want the workspace %q", kitRoot, ws)
	}
	v, _ := evs[0].Decode()
	first := v.(proto.UserMessage).Content[0].Text
	if !strings.HasPrefix(first, "Add func B.") || !strings.Contains(first, "Project instructions, from CLAUDE.md:\n\nRun go test before you finish.") {
		t.Fatalf("first message %q", first)
	}
	for i, body := range f.script.Requests {
		if !strings.Contains(string(body), `"tool_choice":{"type":"any"}`) || !strings.Contains(string(body), `"name":"done"`) {
			t.Errorf("request %d lacks tool_choice any or the done tool", i)
		}
	}
}

// TestCloseReleasesTheEditKit: Core.Close calls each auto-mode session's EditKit.Close once, so the sandbox's temporary
// directory does not outlive the process.
func TestCloseReleasesTheEditKit(t *testing.T) {
	f := newFixture(t)
	closed := 0
	f.core.opt.Edit = func(root string, _ sandbox.Target) (EditKit, error) {
		return EditKit{Shell: noShell{}, Engine: &perms.Engine{Root: root, Sandboxed: true}, Close: func() error { closed++; return nil }}, nil
	}
	if _, err := f.sessionIn(t, gitWorkspace(t, ""), proto.ModeAuto); err != nil {
		t.Fatal(err)
	}
	if closed != 0 {
		t.Fatalf("the kit was closed while its session was open")
	}
	f.core.Close()
	if closed != 1 {
		t.Fatalf("Core.Close closed the edit kit %d times, want 1", closed)
	}
}
