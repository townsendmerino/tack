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

// serverGo has the two shapes §12.22's replay rescued: task 11's error return, and task 6's if block, indented as
// banner.go is.
const serverGo = "package mcp\n\nimport \"fmt\"\n\nfunc repo(s string) (string, error) {\n\tif s == \"\" {\n" +
	"\t\treturn \"\", fmt.Errorf(\"No repo specified\")\n\t}\n\treturn s, nil\n}\n\n" +
	"func note(decline string) string {\n\tcpuNote := \"\"\n\t\tif decline != \"\" {\n\t\t\tcpuNote += \"; declined\"\n\t\t}\n" +
	"\treturn cpuNote\n}\n"

// TestMatchTolerances: all whitespace ignored places a space before a comma; a trailing ; ignored places task 6's
// block, whose indentation also drifts; a kind not asked for places nothing; two places are refused as ambiguous.
func TestMatchTolerances(t *testing.T) {
	m := MatchTolerances(serverGo, `return "" , fmt.Errorf("No repo specified")`, TolerancePunct)
	if m.Name != TolNoSpace || serverGo[m.Start:m.End] != `return "", fmt.Errorf("No repo specified")` {
		t.Errorf("whitespace: %+v %q", m, serverGo[m.Start:m.End])
	}
	semi := "if decline != \"\" {\n\t\t\t\tcpuNote += \"; declined\";\n\t\t\t}"
	m = MatchTolerances(serverGo, semi, []byte{';'})
	if m.Name != TolPunctName(';') || serverGo[m.Start:m.End] != "if decline != \"\" {\n\t\t\tcpuNote += \"; declined\"\n\t\t}" {
		t.Errorf("trailing ;: %+v %q", m, serverGo[m.Start:m.End])
	}
	if m := MatchTolerances(serverGo, semi, []byte{','}); m.Name != "" {
		t.Errorf("a kind not asked for matched: %+v", m)
	}
	if m := MatchTolerances(serverGo+serverGo[len("package mcp\n"):], `return "" , fmt.Errorf("No repo specified")`, nil); m.AmbiguousIn != TolNoSpace || m.Name != "" {
		t.Errorf("two places: %+v", m)
	}
	if !ParsesAsGo(serverGo) || ParsesAsGo(serverGo+"func (") {
		t.Error("ParsesAsGo")
	}
}

// tolerantEdit is a workspace holding mcp/server.go (serverGo) and notes.txt, both read, and the edit tool with
// EditTolerances set to on.
func tolerantEdit(t *testing.T, on bool) (*Workspace, Tool) {
	t.Helper()
	ws := workspace(t)
	os.MkdirAll(filepath.Join(ws.Root(), "mcp"), 0o755)
	os.WriteFile(filepath.Join(ws.Root(), "mcp/server.go"), []byte(serverGo), 0o644)
	os.WriteFile(filepath.Join(ws.Root(), "notes.txt"), []byte("package a\n\nvar x = f(1 , 2)\n"), 0o644) // Go text, not a .go file
	set := New(Options{Write: true, EditTolerances: on})
	r, _ := set.Get("read")
	for _, p := range []string{"mcp/server.go", "notes.txt"} {
		r.Run(context.Background(), ws, json.RawMessage(`{"path":"`+p+`"}`))
	}
	e, _ := set.Get("edit")
	return ws, e
}

func tolEdit(t *testing.T, ws *Workspace, e Tool, path, old, new string) Result {
	t.Helper()
	in, _ := json.Marshal(map[string]string{"path": path, "old_string": old, "new_string": new})
	r, err := e.Run(context.Background(), ws, in)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func fileText(t *testing.T, ws *Workspace, p string) string {
	b, _ := os.ReadFile(filepath.Join(ws.Root(), p))
	return string(b)
}

// TestEditTolerancesApply: with the setting on, task 11's edit applies where the whitespace tolerance places it, and the
// result says so; off, the same edit fails as it always has and the file is unchanged.
func TestEditTolerancesApply(t *testing.T) {
	const old, new = `return "" , fmt.Errorf("No repo specified")`, `return "", fmt.Errorf("no repo specified")`
	ws, e := tolerantEdit(t, true)
	r := tolEdit(t, ws, e, "mcp/server.go", old, new)
	if r.IsError || r.Text != "edited mcp/server.go at line 7 (old_string matched with all whitespace ignored)" {
		t.Fatalf("on: %+v", r)
	}
	if got := fileText(t, ws, "mcp/server.go"); got != strings.Replace(serverGo, `fmt.Errorf("No repo`, `fmt.Errorf("no repo`, 1) {
		t.Fatalf("on: the file is\n%s", got)
	}
	ws, e = tolerantEdit(t, false)
	if r := tolEdit(t, ws, e, "mcp/server.go", old, new); !r.IsError || !strings.Contains(r.Text, "this text isn't in") ||
		fileText(t, ws, "mcp/server.go") != serverGo {
		t.Fatalf("off: %+v", r)
	}
}

// TestEditTolerancesRefuse: a tolerated place is not edited when the edit would leave the file unparseable (the replay's
// `)` case), when the file is not Go, or when the place changed since the model read the file.
func TestEditTolerancesRefuse(t *testing.T) {
	ws, e := tolerantEdit(t, true)
	if r := tolEdit(t, ws, e, "mcp/server.go", `return "" , fmt.Errorf("No repo specified")`, `return "", fmt.Errorf("no repo specified"`); !r.IsError ||
		fileText(t, ws, "mcp/server.go") != serverGo {
		t.Errorf("unparseable result applied: %+v", r)
	}
	if r := tolEdit(t, ws, e, "notes.txt", "f(1  ,  2)", "f(3)"); !r.IsError || fileText(t, ws, "notes.txt") != "package a\n\nvar x = f(1 , 2)\n" {
		t.Errorf("a non-Go file was tolerated: %+v", r)
	}
	ws, e = tolerantEdit(t, true)
	p := filepath.Join(ws.Root(), "mcp/server.go")
	changed := strings.Replace(serverGo, `"No repo specified"`, `"No repo given"`, 1)
	os.WriteFile(p, []byte(changed), 0o644) // changed on disk after the read
	if r := tolEdit(t, ws, e, "mcp/server.go", `return "" , fmt.Errorf("No repo given")`, `return "", nil`); !r.IsError ||
		fileText(t, ws, "mcp/server.go") != changed {
		t.Errorf("a place changed since the read was edited: %+v", r)
	}
}

// TestEditTolerancesLeaveThePrompt: the setting changes no tool's schema or description.
func TestEditTolerancesLeaveThePrompt(t *testing.T) {
	specs := func(o Options) string {
		var b strings.Builder
		for _, sp := range New(o).Specs() {
			fmt.Fprintf(&b, "%s|%s|%s\n", sp.Name, sp.Description, sp.InputSchema)
		}
		return b.String()
	}
	if specs(Options{Write: true}) != specs(Options{Write: true, EditTolerances: true}) {
		t.Fatal("tool specs differ")
	}
}

// TestTolerancesKeepLiterals: whitespace is ignored only between Go tokens. A string literal, a rune literal, a raw string
// and an identifier must match as written, so no tolerance changes the text of a literal or joins two names into one.
func TestTolerancesKeepLiterals(t *testing.T) {
	const src = "package a\n\nvar s = \"foo bar\"\nvar r = ' '\nvar q = `a  b`\nvar n = ab + c\n"
	for _, old := range []string{
		`var s = "foobar"`,   // a space inside a string literal
		`var s = "foo  bar"`, // a doubled space inside a string literal
		`var r = ''`,         // a rune literal's space
		"var q = `a b`",      // a raw string's run of spaces
		`var n = a b + c`,    // one identifier split in two
		`vars = "foo bar"`,   // two tokens joined: the keyword and the name
		"var s = \"foo bar\";",
	} {
		if m := MatchTolerances(src, old, nil); m.Name != "" || m.AmbiguousIn != "" {
			t.Errorf("%q matched %q (%+v)", old, src[m.Start:m.End], m)
		}
	}
	// Whitespace between tokens is still ignored, line breaks included.
	if m := MatchTolerances(src, "var  s=\"foo bar\"\n var r =' '", nil); m.Name != TolNoSpace ||
		src[m.Start:m.End] != "var s = \"foo bar\"\nvar r = ' '" {
		t.Errorf("between tokens: %+v", m)
	}
	// A trailing ; is dropped only where it ends a line as a token, never where a literal holds one: here old's raw
	// string ends a line with ; and the file's does not.
	const raw = "package a\n\nvar q = `x\ny`\n"
	if m := MatchTolerances(raw, "var q = `x;\ny`", TolerancePunct); m.Name != "" {
		t.Errorf("a ; in a literal was dropped: %+v %q", m, raw[m.Start:m.End])
	}
}

// TestTolerancesReplayKeptRescues replays the two distinct edits §12.22's replay kept
// (e1-calibration-c6-tolerances-2026-10-27.md, "The cases"): each old_string, as the model wrote it, against the region it
// rescued, must still be placed by the same tolerance on the whole region.
func TestTolerancesReplayKeptRescues(t *testing.T) {
	for _, c := range []struct{ tol, old, region string }{
		{TolNoSpace,
			"return \"\" , fmt.Errorf(\"No repo specified and no default index. Pass an https:// or http:// git URL or local directory path as `repo`.\")",
			"return \"\", fmt.Errorf(\"No repo specified and no default index. Pass an https:// or http:// git URL or local directory path as `repo`.\")"},
		{TolPunctName(';'),
			"if f.residentDecline != \"\" {\n\t\t\t\tcpuNote += \"; a GPU-resident load caps it with --ctx, but this model declined one (see decode path)\";\n\t\t\t}",
			"if f.residentDecline != \"\" {\n\t\t\tcpuNote += \"; a GPU-resident load caps it with --ctx, but this model declined one (see decode path)\"\n\t\t}"},
	} {
		src := "package a\n\nfunc f() {\n\t" + c.region + "\n}\n"
		m := MatchTolerances(src, c.old, TolerancePunct)
		if m.Name != c.tol || src[m.Start:m.End] != c.region {
			t.Errorf("%s: %+v %q", c.tol, m, src[m.Start:m.End])
		}
	}
}
