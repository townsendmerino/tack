package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/eval/pilot"
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

// TestEditMessagesThroughTheCore: Options.EditMessages reaches the edit tool of an auto-mode session; off, the same
// call gets the old reply.
func TestEditMessagesThroughTheCore(t *testing.T) {
	for _, on := range []bool{true, false} {
		f := newFixture(t,
			goinfertest.Calls("", "e1", "edit", `{"path":"a.go","old_string":"package a","new_string":"package a"}`),
			goinfertest.Calls("", "d1", "done", `{"reason":"x"}`), goinfertest.Calls("", "d2", "done", `{"reason":"x"}`))
		f.core.opt.Edit = func(root string, _ sandbox.Target) (EditKit, error) {
			return EditKit{Shell: noShell{}, Engine: &perms.Engine{Root: root, Sandboxed: true}}, nil
		}
		f.core.opt.EditTask, f.core.opt.EditMessages = true, on
		sid, err := f.sessionIn(t, gitWorkspace(t, ""), proto.ModeAuto)
		if err != nil {
			t.Fatal(err)
		}
		f.client.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: sid}, nil)
		f.client.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: sid, Content: []proto.ContentBlock{proto.Text("x")}}, nil)
		var result string
		for _, ev := range f.waitTurnEnd(t) {
			if v, _ := ev.Decode(); ev.Type == proto.EvToolFinished && result == "" {
				result = v.(proto.ToolFinished).Result
			}
		}
		if want := map[bool]string{true: "this edit changes nothing", false: "old_string and new_string are the same"}[on]; !strings.Contains(result, want) {
			t.Errorf("EditMessages %v: %q", on, result)
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

// editTurn runs one auto-mode edit task in ws against script, with set applied to the core's options first, and
// returns its events, also written as an events log for the replays.
func editTurn(t *testing.T, ws string, set func(*Options), script ...goinfertest.Reply) ([]proto.Event, string) {
	t.Helper()
	f := newFixture(t, script...)
	f.core.opt.Edit = func(root string, _ sandbox.Target) (EditKit, error) {
		return EditKit{Shell: noShell{}, Engine: &perms.Engine{Root: root, Sandboxed: true}}, nil
	}
	f.core.opt.EditTask = true
	f.core.opt.Budget.MaxRequests, f.core.opt.Budget.MaxToolCalls = 40, 40
	set(&f.core.opt)
	sid, err := f.sessionIn(t, ws, proto.ModeAuto)
	if err != nil {
		t.Fatal(err)
	}
	f.client.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: sid}, nil)
	f.client.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: sid, Content: []proto.ContentBlock{proto.Text("x")}}, nil)
	evs := f.waitTurnEnd(t)
	var log bytes.Buffer
	for _, ev := range evs {
		b, _ := json.Marshal(ev)
		log.Write(append(b, '\n'))
	}
	p := filepath.Join(t.TempDir(), "events.jsonl")
	os.WriteFile(p, log.Bytes(), 0o644)
	return evs, p
}

// toolResults is each tool call's result in evs, by call id.
func toolResults(evs []proto.Event) map[string]string {
	r := map[string]string{}
	for _, ev := range evs {
		if v, _ := ev.Decode(); ev.Type == proto.EvToolFinished {
			r[v.(proto.ToolFinished).ID] = v.(proto.ToolFinished).Result
		}
	}
	return r
}

// TestRepeatRuleSeesTheFileChange: under stop_repeats, an edit that changes its file every time is never an unchanged
// repeat, though its reply is the same each time (calibration §12.13), and an identical failing edit of a file that
// does not change still ends the turn. The replay of the run made with the rule off (pilot.ReplayRepeats, given the
// base tree) stops where the live rule did; replayed without the file states, it stops at the growing edit's ninth call.
func TestRepeatRuleSeesTheFileChange(t *testing.T) {
	grow := `{"path":"a.go","old_string":"package a","new_string":"package a\n// grown"}`
	fail := `{"path":"b.go","old_string":"package zzz","new_string":"package b"}`
	script := []goinfertest.Reply{goinfertest.Calls("", "r0", "read", `{"path":"a.go"}`, "r1", "read", `{"path":"b.go"}`)}
	for i := 0; i < 3; i++ {
		script = append(script, goinfertest.Calls("", fmt.Sprintf("g%d", i), "edit", grow))
	}
	for i := 3; i < 15; i++ { // from the fifth request, the growing edit and the failing one
		script = append(script, goinfertest.Calls("", fmt.Sprintf("g%d", i), "edit", grow, fmt.Sprintf("f%d", i), "edit", fail))
	}
	script = append(script, goinfertest.Calls("", "d1", "done", `{"reason":"x"}`))
	run := func(on bool) ([]proto.Event, string, string) {
		ws := gitWorkspace(t, "")
		os.WriteFile(filepath.Join(ws, "b.go"), []byte("package b\n"), 0o644)
		evs, p := editTurn(t, ws, func(o *Options) { o.StopRepeats = on }, script...)
		a, _ := os.ReadFile(filepath.Join(ws, "a.go"))
		return evs, p, string(a)
	}
	evs, onPath, a := run(true)
	if v, _ := evs[len(evs)-1].Decode(); v.(proto.TurnEnd).StopReason != proto.StopRepeatLoop {
		t.Fatalf("the failing edit's repeats did not end the turn: %+v", v)
	}
	// The ninth failing edit, in the thirteenth response, ends the turn: twelve growing edits have applied by then,
	// where the rule on replies alone would have ended it at the ninth.
	if n := strings.Count(a, "// grown"); n != 12 {
		t.Fatalf("a.go grew %d times before the stop, want 12", n)
	}
	on, err := pilot.ReplayRepeats(onPath, map[string]string{"a.go": "package a\n", "b.go": "package b\n"})
	if err != nil {
		t.Fatal(err)
	}
	_, offPath, _ := run(false)
	base := map[string]string{"a.go": "package a\n", "b.go": "package b\n"}
	off, err := pilot.ReplayRepeats(offPath, base)
	if err != nil {
		t.Fatal(err)
	}
	if !off.Stopped || off.StopRequest != on.Requests || off.StopRequest != 13 || off.Repeats != 8 || off.Lost != 0 {
		t.Fatalf("amended replay of the off run %+v; the live rule ended the on run after request %d", off, on.Requests)
	}
	was, err := pilot.ReplayRepeats(offPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !was.Stopped || was.StopRequest != 10 {
		t.Fatalf("the rule on replies alone should stop at the ninth growing edit (request 10): %+v", was)
	}
}

// TestDuplicateEditGuard: with Options.RefuseDuplicateEdits, an edit identical to one already applied to the same file
// is refused with the line it was applied at, both when it would apply again (its new_string holds its old_string) and
// when its text is gone; off, the first applies twice and the second is not found. pilot.ReplayDuplicateEdits counts
// both in either run, and counts the one the off run applied again.
func TestDuplicateEditGuard(t *testing.T) {
	grow := `{"path":"a.go","old_string":"package a","new_string":"package a\n// grown"}`
	swap := `{"path":"b.go","old_string":"package b","new_string":"package c"}`
	for _, on := range []bool{true, false} {
		ws := gitWorkspace(t, "")
		os.WriteFile(filepath.Join(ws, "b.go"), []byte("package b\n"), 0o644)
		evs, p := editTurn(t, ws, func(o *Options) { o.RefuseDuplicateEdits = on },
			goinfertest.Calls("", "r0", "read", `{"path":"a.go"}`, "r1", "read", `{"path":"b.go"}`),
			goinfertest.Calls("", "g1", "edit", grow, "s1", "edit", swap),
			goinfertest.Calls("", "g2", "edit", grow, "s2", "edit", swap),
			goinfertest.Calls("", "d1", "done", `{"reason":"x"}`))
		res := toolResults(evs)
		a, _ := os.ReadFile(filepath.Join(ws, "a.go"))
		if on {
			if !strings.Contains(res["g2"], `this exact edit was already applied to "a.go" at line 1`) ||
				!strings.Contains(res["s2"], `this exact edit was already applied to "b.go" at line 1`) ||
				strings.Count(string(a), "// grown") != 1 {
				t.Fatalf("guard on: %q, %q; a.go %q", res["g2"], res["s2"], a)
			}
		} else if res["g2"] != "edited a.go at line 1" || !strings.Contains(res["s2"], "this text isn't in") ||
			strings.Count(string(a), "// grown") != 2 {
			t.Fatalf("guard off: %q, %q; a.go %q", res["g2"], res["s2"], a)
		}
		d, err := pilot.ReplayDuplicateEdits(p)
		if want := map[bool]int{true: 0, false: 1}[on]; err != nil || d.Calls != 2 || d.Applied != want || d.First != 3 {
			t.Fatalf("guard %v: replay counts %+v, %v; want 2 calls, %d applied again, first in request 3", on, d, err, want)
		}
	}
}
