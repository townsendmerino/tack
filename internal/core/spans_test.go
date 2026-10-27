package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/townsendmerino/tack/internal/perms"
	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/sandbox"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
	"github.com/townsendmerino/tack/internal/tools"
)

// aSpans is an index that places func A at lines 3-5 of a.go.
type aSpans struct{}

func (aSpans) Definition(context.Context, string) ([]tools.Def, error)    { return nil, nil }
func (aSpans) Outline(context.Context, string) ([]tools.Def, error)       { return nil, nil }
func (aSpans) Callers(context.Context, string) ([]tools.Site, error)      { return nil, nil }
func (aSpans) Callees(context.Context, string) ([]tools.Site, error)      { return nil, nil }
func (aSpans) TestsFor(context.Context, string) ([]tools.TestSite, error) { return nil, nil }
func (aSpans) Sync(context.Context, []string) (bool, error)               { return false, nil }
func (aSpans) SymbolAt(_ context.Context, path string, line int) ([]tools.Def, error) {
	if path != "a.go" || line < 3 || line > 5 {
		return nil, nil
	}
	return []tools.Def{{File: path, Name: "A", Kind: "func", StartLine: 3, EndLine: 5}}, nil
}

// currentSymbolTurn runs one auto-mode turn over a workspace whose a.go defines A, with CurrentSymbol set to on and
// OpenSpans counting its opens. The model reads a.go and makes that many wrong edits to it, reading it again between them.
// It returns the opens and the last edit's result.
func currentSymbolTurn(t *testing.T, on bool, edits int) (int32, string) {
	t.Helper()
	const wrong = `{"path":"a.go","old_string":"return 1;","new_string":"return 2"}`
	replies := []goinfertest.Reply{goinfertest.Calls("", "r0", "read", `{"path":"a.go"}`)}
	for i := 0; i < edits; i++ {
		if i > 0 {
			replies = append(replies, goinfertest.Calls("", "r", "read", `{"path":"a.go"}`))
		}
		replies = append(replies, goinfertest.Calls("", "e", "edit", wrong))
	}
	f := newFixture(t, replies...)
	ws := gitWorkspace(t, "")
	os.WriteFile(filepath.Join(ws, "a.go"), []byte("package a\n\nfunc A() int {\n\treturn 1\n}\n"), 0o644)
	var opens atomic.Int32
	f.core.opt.Edit = func(root string, _ sandbox.Target) (EditKit, error) {
		return EditKit{Shell: noShell{}, Engine: &perms.Engine{Root: root, Sandboxed: true}}, nil
	}
	f.core.opt.EditTask, f.core.opt.CurrentSymbol = true, on
	f.core.opt.OpenSpans = func(context.Context, string) (tools.Symbols, error) { opens.Add(1); return aSpans{}, nil }
	sid, err := f.sessionIn(t, ws, proto.ModeAuto)
	if err != nil {
		t.Fatal(err)
	}
	if n := opens.Load(); n != 0 {
		t.Fatalf("the index was opened %d times at the session's start", n)
	}
	if err := f.client.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: sid}, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.client.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: sid,
		Content: []proto.ContentBlock{proto.Text("Make A return 2.")}}, nil); err != nil {
		t.Fatal(err)
	}
	last := ""
	for _, ev := range f.waitTurnEnd(t) {
		if v, _ := ev.Decode(); v != nil {
			if fin, ok := v.(proto.ToolFinished); ok && fin.ID == "e" {
				last = fin.Result
			}
		}
	}
	for i, body := range f.script.Requests {
		if strings.Contains(string(body), `"name":"symbol"`) || strings.Contains(string(body), `"name":"search"`) {
			t.Errorf("request %d offers ken's tools", i)
		}
	}
	return opens.Load(), last
}

// TestCurrentSymbolThroughTheCore: Options.CurrentSymbol reaches an auto-mode session's edit tool with OpenSpans'
// index, opened once, at the first note and not before: a turn with one failed edit never opens it. Off, OpenSpans is
// never called and there is no note. Neither way are ken's tools offered.
func TestCurrentSymbolThroughTheCore(t *testing.T) {
	note := tools.CurrentSymbolOpen + "A in a.go, lines 3-5, exactly as the file has it now; copy old_string from it:\n" +
		"func A() int {\n\treturn 1\n}\n]"
	if opens, last := currentSymbolTurn(t, true, 2); opens != 1 || !strings.HasSuffix(last, note) {
		t.Errorf("on, two edits: %d opens, last result %q", opens, last)
	}
	if opens, last := currentSymbolTurn(t, true, 1); opens != 0 || strings.Contains(last, "[tack:") {
		t.Errorf("on, one edit: %d opens, result %q", opens, last)
	}
	if opens, last := currentSymbolTurn(t, false, 2); opens != 0 || strings.Contains(last, "[tack:") {
		t.Errorf("off: %d opens, last result %q", opens, last)
	}
}

// TestLazySpansKeepsAFailedOpen: an index that fails to open is tried once; every later call returns its error.
func TestLazySpansKeepsAFailedOpen(t *testing.T) {
	var opens int
	l := &lazySpans{root: "/r", open: func(context.Context, string) (tools.Symbols, error) {
		opens++
		return nil, os.ErrNotExist
	}}
	for i := 0; i < 3; i++ {
		if _, err := l.SymbolAt(context.Background(), "a.go", 1); err != os.ErrNotExist {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if opens != 1 || l.Close() != nil {
		t.Fatalf("%d opens", opens)
	}
}
