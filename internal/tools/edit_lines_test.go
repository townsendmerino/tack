package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// linesFile is ten numbered lines.
const linesFile = "one\ntwo\nthree\nfour\nfive\nsix\nseven\neight\nnine\nten\n"

func linesWorkspace(t *testing.T, o Options) (*Workspace, *Set) {
	t.Helper()
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "f.txt"), []byte(linesFile), 0o644)
	ws, err := NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	o.Write, o.LineEdits = true, true
	return ws, New(o)
}

func lineEdit(start, end int, newText string) string {
	return fmt.Sprintf(`{"path":"f.txt","start_line":%d,"end_line":%d,"new_string":%q}`, start, end, newText)
}

func TestEditByLinesReplacesTheLines(t *testing.T) {
	ws, s := linesWorkspace(t, Options{})
	read(t, s, ws, "f.txt")
	r := call(t, s, ws, "edit", lineEdit(3, 4, "THREE\nTHREE-B\nFOUR"))
	if r.IsError || r.Text != "edited f.txt: replaced lines 3-4, now lines 3-5" {
		t.Fatalf("edit by lines: %q", r.Text)
	}
	want := strings.Replace(linesFile, "three\nfour\n", "THREE\nTHREE-B\nFOUR\n", 1)
	if got := content(t, ws, "f.txt"); got != want {
		t.Fatalf("file =\n%s", got)
	}
	if r.State != ContentState(want) {
		t.Fatal("State is not the file after the edit")
	}
}

// TestEditByLinesRefusesAShiftedRange: §4.9's guard. After the model's own edit moves the lines below it, a range with
// the old numbers is refused, and the file is left alone; reading again makes it good.
func TestEditByLinesRefusesAShiftedRange(t *testing.T) {
	ws, s := linesWorkspace(t, Options{})
	read(t, s, ws, "f.txt")
	call(t, s, ws, "edit", lineEdit(2, 2, "two\ntwo-b\ntwo-c")) // lines below move down 2
	before := content(t, ws, "f.txt")
	r := call(t, s, ws, "edit", lineEdit(8, 8, "EIGHT")) // "eight" as read; now "six"
	if !r.IsError || !strings.Contains(r.Text, "lines 8-8 of \"f.txt\" are not what you read there") {
		t.Fatalf("a range shifted by the model's own edit: %q", r.Text)
	}
	if content(t, ws, "f.txt") != before {
		t.Fatal("a refused edit changed the file")
	}
	read(t, s, ws, "f.txt")
	if r := call(t, s, ws, "edit", lineEdit(10, 10, "EIGHT")); r.IsError || !strings.Contains(content(t, ws, "f.txt"), "\nEIGHT\nnine\n") {
		t.Fatalf("after reading again: %q", r.Text)
	}

}

// TestEditByLinesRefusesARangeShiftedOutside: a change made outside the tools that moves the lines is refused the same way.
func TestEditByLinesRefusesARangeShiftedOutside(t *testing.T) {
	ws, s := linesWorkspace(t, Options{})
	read(t, s, ws, "f.txt")
	os.WriteFile(filepath.Join(ws.Root(), "f.txt"), []byte("zero\n"+linesFile), 0o644)
	if r := call(t, s, ws, "edit", lineEdit(5, 5, "FIVE")); !r.IsError || !strings.Contains(r.Text, "not what you read there") {
		t.Fatalf("a range shifted by an outside change: %q", r.Text)
	}
}

// TestEditByLinesNeedsTheLinesShown: a range must lie in what read showed; ranges shown of the same content add up.
func TestEditByLinesNeedsTheLinesShown(t *testing.T) {
	ws, s := linesWorkspace(t, Options{})
	if r := call(t, s, ws, "edit", lineEdit(1, 1, "x")); !r.IsError || !strings.Contains(r.Text, "before editing it by line") {
		t.Fatalf("unread: %q", r.Text)
	}
	call(t, s, ws, "read", `{"path":"f.txt","offset":1,"limit":3}`)
	if r := call(t, s, ws, "edit", lineEdit(3, 5, "x")); !r.IsError || !strings.Contains(r.Text, "were not all shown to you") {
		t.Fatalf("partly shown: %q", r.Text)
	}
	call(t, s, ws, "read", `{"path":"f.txt","offset":4,"limit":2}`)
	if r := call(t, s, ws, "edit", lineEdit(3, 5, "x")); r.IsError {
		t.Fatalf("shown in two reads: %q", r.Text)
	}
}

func TestEditByLinesRefusals(t *testing.T) {
	ws, s := linesWorkspace(t, Options{})
	read(t, s, ws, "f.txt")
	for input, want := range map[string]string{
		lineEdit(0, 1, "x"):   "from 1",
		lineEdit(3, 2, "x"):   "start_line <= end_line",
		lineEdit(10, 11, "x"): "were not all shown",
		lineEdit(2, 2, "two"): "nothing to change",
		`{"path":"f.txt","start_line":1,"end_line":1,"old_string":"one","new_string":"x"}`: "give one of old_string, symbol, or start_line and end_line",
	} {
		if r := call(t, s, ws, "edit", input); !r.IsError || !strings.Contains(r.Text, want) {
			t.Errorf("%s: %q, want %q", input, r.Text, want)
		}
	}
	if content(t, ws, "f.txt") != linesFile {
		t.Fatal("a refused edit changed the file")
	}
	wide := "a\n" + strings.Repeat("w", maxLineRunes+1) + "\nc\n"
	os.WriteFile(filepath.Join(ws.Root(), "f.txt"), []byte(wide), 0o644)
	read(t, s, ws, "f.txt")
	if r := call(t, s, ws, "edit", lineEdit(2, 2, "x")); !r.IsError || !strings.Contains(r.Text, "longer than read shows whole") {
		t.Fatalf("a cut line: %q", r.Text)
	}
}

func TestEditByLinesDeletes(t *testing.T) {
	ws, s := linesWorkspace(t, Options{})
	read(t, s, ws, "f.txt")
	if r := call(t, s, ws, "edit", lineEdit(2, 3, "")); r.Text != "edited f.txt: deleted lines 2-3" {
		t.Fatalf("delete: %q", r.Text)
	}
	if got := content(t, ws, "f.txt"); got != strings.Replace(linesFile, "two\nthree\n", "", 1) {
		t.Fatalf("file =\n%s", got)
	}
}

func TestEditByLinesRefusesADuplicate(t *testing.T) {
	ws, s := linesWorkspace(t, Options{RefuseDuplicateEdits: true})
	read(t, s, ws, "f.txt")
	call(t, s, ws, "edit", lineEdit(1, 1, "ONE"))
	read(t, s, ws, "f.txt")
	call(t, s, ws, "edit", lineEdit(1, 1, "one"))
	read(t, s, ws, "f.txt")
	if r := call(t, s, ws, "edit", lineEdit(1, 1, "ONE")); !r.IsError || !strings.Contains(r.Text, "already applied") {
		t.Fatalf("a repeated line edit: %q", r.Text)
	}
}

// TestEditByLinesAfterSymbol: symbol shows lines with their numbers, so they can be edited by line.
func TestEditByLinesAfterSymbol(t *testing.T) {
	ws, sym, _ := symbolWorkspace(t)
	s := New(Options{Symbols: sym, Write: true, LineEdits: true})
	call(t, s, ws, "symbol", `{"name":"Model.Size"}`)
	r := call(t, s, ws, "edit", `{"path":"pull/methods.go","start_line":4,"end_line":4,"new_string":"\treturn m.n * 4"}`)
	if r.IsError || !strings.HasPrefix(r.Text, "edited pull/methods.go: replaced lines 4-4, now lines 4-4, in Model.Size") {
		t.Fatalf("line edit after symbol: %q", r.Text)
	}
}

// TestEditSchemaForms: the schema without a form is as it was, byte for byte; the line form adds its two properties and
// a sentence, and drops old_string from required; without it, start_line is bad input.
func TestEditSchemaForms(t *testing.T) {
	const plainSchema = `{"type":"object","properties":{"path":{"type":"string","description":"relative to the repo root"},"old_string":{"type":"string"},"new_string":{"type":"string"},"replace_all":{"type":"boolean"}},"required":["path","old_string","new_string"]}`
	const symbolSchema = `{"type":"object","properties":{"path":{"type":"string","description":"relative to the repo root"},"old_string":{"type":"string"},"new_string":{"type":"string"},"replace_all":{"type":"boolean"},"symbol":{"type":"string","description":"a function, type or method in path"}},"required":["path","new_string"]}`
	if got := string(editTool{}.Spec().InputSchema); got != plainSchema {
		t.Fatalf("plain schema changed:\n%s", got)
	}
	if got := string(editTool{sym: &fakeSymbols{}}.Spec().InputSchema); got != symbolSchema {
		t.Fatalf("symbol schema changed:\n%s", got)
	}
	lines := editTool{lines: true}.Spec()
	if !strings.Contains(string(lines.InputSchema), `"start_line":{"type":"integer"},"end_line":{"type":"integer"}},"required":["path","new_string"]`) ||
		!strings.HasSuffix(lines.Description, " Or give start_line and end_line, as read numbers them, instead of old_string: new_string replaces those lines.") {
		t.Fatalf("line form: %s %s", lines.Description, lines.InputSchema)
	}
	ws, _ := linesWorkspace(t, Options{})
	plain := New(Options{Write: true})
	read(t, plain, ws, "f.txt")
	if r := call(t, plain, ws, "edit", lineEdit(1, 1, "x")); !r.IsError || !strings.Contains(r.Text, `unknown field "start_line"`) {
		t.Fatalf("start_line without the line form: %q", r.Text)
	}
}
