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

// bannerGo is a file whose old_string the model gets wrong as Qwen3-8B did on task 6: a `;` Go does not have.
const bannerGo = "package serveapp\n\n// banner is the start line.\nfunc banner(decline string) string {\n\tnote := \"\"\n" +
	"\tif decline != \"\" {\n\t\tnote += \"; declined\"\n\t}\n\treturn note\n}\n"

// symbolEdits is a workspace holding bannerGo, read once, and the edit tool with CurrentSymbol set to an index of it.
func symbolEdits(t *testing.T, content string) (*Workspace, Tool, *fakeSymbols) {
	t.Helper()
	ws := workspace(t)
	os.MkdirAll(filepath.Join(ws.Root(), "internal/serveapp"), 0o755)
	os.WriteFile(filepath.Join(ws.Root(), "internal/serveapp/banner.go"), []byte(content), 0o644)
	sym := newFakeSymbols(t, ws)
	set := New(Options{Write: true, CurrentSymbol: sym})
	r, _ := set.Get("read")
	r.Run(context.Background(), ws, json.RawMessage(`{"path":"internal/serveapp/banner.go"}`))
	e, _ := set.Get("edit")
	return ws, e, sym
}

func runEdit(t *testing.T, ws *Workspace, e Tool, old, new string) Result {
	t.Helper()
	in, _ := json.Marshal(map[string]string{"path": "internal/serveapp/banner.go", "old_string": old, "new_string": new})
	r, err := e.Run(context.Background(), ws, in)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

const wrongOld = "if decline != \"\" {\n\t\t\tnote += \"; declined\";\n\t\t}"

// TestCurrentSymbolNoteOnARepeatedEditOnly: a failed edit whose text is not in the file gets no note the first time
// the session edits that file, and the note on the next: the symbol the edit aimed at, named with its lines, exactly
// as the file has it, with no line numbers. The note is in Note, not in Text, so the repeat rule sees the tool's
// reply.
func TestCurrentSymbolNoteOnARepeatedEditOnly(t *testing.T) {
	ws, e, _ := symbolEdits(t, bannerGo)
	first := runEdit(t, ws, e, wrongOld, "x")
	if !first.IsError || !strings.Contains(first.Text, "this text isn't in") || first.Note != "" {
		t.Fatalf("first edit: %+v", first)
	}
	second := runEdit(t, ws, e, wrongOld, "y")
	want := CurrentSymbolOpen + "banner in internal/serveapp/banner.go, lines 4-10, exactly as the file has it now; copy " +
		"old_string from it:\n" + strings.Join(strings.Split(bannerGo, "\n")[3:10], "\n") + "\n]"
	if !second.IsError || second.Note != want {
		t.Fatalf("second edit's note:\n%q\nwant\n%q", second.Note, want)
	}
	if strings.Contains(second.Text, "[tack:") {
		t.Fatalf("the note is in Text: %q", second.Text)
	}
	// The note's lines are the file's, so an old_string copied from them applies.
	if r := runEdit(t, ws, e, "\t\tnote += \"; declined\"", "\t\tnote += \"; declined (see decode path)\""); r.IsError {
		t.Fatalf("an old_string copied from the note: %q", r.Text)
	}
}

// TestCurrentSymbolNoteAfterAnyEarlierEditCall: an earlier call that applied counts as an earlier edit of the file, and
// so does the edit's other "not in the file" reply, for text the model saw before its own edit changed it.
func TestCurrentSymbolNoteAfterAnyEarlierEditCall(t *testing.T) {
	ws, e, _ := symbolEdits(t, bannerGo)
	if r := runEdit(t, ws, e, "note := \"\"", "note := \"x\""); r.IsError || r.Note != "" {
		t.Fatalf("an edit that applies: %+v", r)
	}
	ws.saw(filepath.Join(ws.Root(), "internal/serveapp/banner.go"), bannerGo) // as if read before that edit
	r := runEdit(t, ws, e, "note := \"\"", "note := \"y\"")
	if !strings.Contains(r.Text, "changed since you last read it") || !strings.Contains(r.Note, "\tnote := \"x\"\n") {
		t.Fatalf("stale text: %+v", r)
	}
}

// TestCurrentSymbolNoteCutsALongSymbol: a symbol longer than currentSymbolLines shows that many lines around the line
// most like old_string, and says which part of the symbol they are.
func TestCurrentSymbolNoteCutsALongSymbol(t *testing.T) {
	var b strings.Builder
	b.WriteString("package serveapp\n\nfunc long() {\n")
	for i := 1; i <= 100; i++ {
		fmt.Fprintf(&b, "\tstep%d()\n", i)
	}
	b.WriteString("}\n")
	ws, e, _ := symbolEdits(t, b.String())
	runEdit(t, ws, e, "step70(;", "x")
	r := runEdit(t, ws, e, "step70(;", "x")
	if !strings.HasPrefix(r.Note, CurrentSymbolOpen+"long in internal/serveapp/banner.go, lines 53-92 of 3-104,") {
		t.Fatalf("note %q", r.Note)
	}
	body := r.Note[strings.Index(r.Note, ":\n")+2 : len(r.Note)-2]
	lines := strings.Split(body, "\n")
	if len(lines) != currentSymbolLines || lines[0] != "\tstep50()" || lines[39] != "\tstep89()" {
		t.Fatalf("%d lines, %q … %q", len(lines), lines[0], lines[len(lines)-1])
	}
}

// TestCurrentSymbolNoNote: there is no note when the setting is off, when no line shares a word with old_string, when
// the best line is at file scope, when the index cannot place the file, and for edit's other failures.
func TestCurrentSymbolNoNote(t *testing.T) {
	twice := func(ws *Workspace, e Tool, old string) Result {
		runEdit(t, ws, e, old, "x")
		return runEdit(t, ws, e, old, "x")
	}
	ws := workspace(t)
	os.MkdirAll(filepath.Join(ws.Root(), "internal/serveapp"), 0o755)
	os.WriteFile(filepath.Join(ws.Root(), "internal/serveapp/banner.go"), []byte(bannerGo), 0o644)
	off := New(Options{Write: true})
	rd, _ := off.Get("read")
	rd.Run(context.Background(), ws, json.RawMessage(`{"path":"internal/serveapp/banner.go"}`))
	e, _ := off.Get("edit")
	if r := twice(ws, e, wrongOld); r.Note != "" {
		t.Errorf("setting off: %q", r.Note)
	}

	ws, e, _ = symbolEdits(t, bannerGo)
	if r := twice(ws, e, "qqq zzz"); r.Note != "" {
		t.Errorf("no shared word: %q", r.Note)
	}
	ws, e, _ = symbolEdits(t, bannerGo)
	if r := twice(ws, e, "package serveapp;"); r.Note != "" {
		t.Errorf("file scope: %q", r.Note)
	}
	ws, _, sym := symbolEdits(t, bannerGo)
	e, _ = New(Options{Write: true, CurrentSymbol: notIndexed{sym}}).Get("edit")
	if r := twice(ws, e, wrongOld); r.Note != "" {
		t.Errorf("not indexed: %q", r.Note)
	}
	ws, e, _ = symbolEdits(t, bannerGo+"func two() {\n\tnote := \"\"\n}\n")
	if r := twice(ws, e, "note := \"\""); r.Note != "" || !strings.Contains(r.Text, "occurs 2 times") {
		t.Errorf("text found twice: %+v", r)
	}
}

// TestCurrentSymbolLeavesThePrompt: the setting adds no tool and leaves edit's schema and description as they are, so
// the fixed prompt is byte for byte the bundle's.
func TestCurrentSymbolLeavesThePrompt(t *testing.T) {
	ws := workspace(t)
	specs := func(o Options) string {
		var b strings.Builder
		for _, sp := range New(o).Specs() {
			fmt.Fprintf(&b, "%s|%s|%s\n", sp.Name, sp.Description, sp.InputSchema)
		}
		return b.String()
	}
	if a, b := specs(Options{Write: true}), specs(Options{Write: true, CurrentSymbol: newFakeSymbols(t, ws)}); a != b {
		t.Fatalf("tool specs differ:\n%s\n---\n%s", a, b)
	}
}

// notIndexed is an index that holds no file.
type notIndexed struct{ *fakeSymbols }

func (notIndexed) SymbolAt(context.Context, string, int) ([]Def, error) { return nil, ErrNotIndexed }
