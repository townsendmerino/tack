package tools

import (
	"context"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
)

// fakeSymbols indexes a workspace's .go files the way ken does: once at the start, and again for a file only when
// Sync refreshes it, so a file changed on disk keeps its old spans until then. Definition, Outline and SymbolAt come
// from that index; Callers, Callees and TestsFor are fixed answers.
type fakeSymbols struct {
	root string

	mu      sync.Mutex
	index   map[string]string // rel → content as indexed
	syncs   [][]string        // every Sync's paths
	callers []Site
	callees []Site
	tests   []TestSite
	err     error // returned by every lookup when set
}

func newFakeSymbols(t *testing.T, ws *Workspace) *fakeSymbols {
	t.Helper()
	f := &fakeSymbols{root: ws.Root(), index: map[string]string{}}
	filepath.WalkDir(f.root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".go") {
			b, _ := os.ReadFile(p)
			f.index[ws.Rel(p)] = string(b)
		}
		return nil
	})
	return f
}

var declRe = regexp.MustCompile(`^(?:func (?:\(\w+ \*?(\w+)\) )?(\w+)\(|type (\w+) )`)

// defs is the fake index's definitions, in file and line order: a declaration at column 0 through the next line that
// is "}" (or the same line when it closes there), and the // lines directly above it.
func (f *fakeSymbols) defs() []Def {
	var files []string
	for rel := range f.index {
		files = append(files, rel)
	}
	sort.Strings(files)
	var out []Def
	for _, rel := range files {
		lines := strings.Split(f.index[rel], "\n")
		for i, l := range lines {
			m := declRe.FindStringSubmatch(l)
			if m == nil {
				continue
			}
			d := Def{File: rel, StartLine: i + 1, EndLine: i + 1}
			switch {
			case m[3] != "":
				d.Name, d.Kind = m[3], "type"
			case m[1] != "":
				d.Name, d.Kind = m[1]+"."+m[2], "method"
			default:
				d.Name, d.Kind = m[2], "func"
			}
			if !strings.HasSuffix(l, "}") {
				for j := i + 1; j < len(lines); j++ {
					if lines[j] == "}" {
						d.EndLine = j + 1
						break
					}
				}
			}
			if i > 0 && strings.HasPrefix(lines[i-1], "//") {
				d.DocLine = i
				for d.DocLine > 1 && strings.HasPrefix(lines[d.DocLine-2], "//") {
					d.DocLine--
				}
			}
			out = append(out, d)
		}
	}
	return out
}

func (f *fakeSymbols) Definition(_ context.Context, name string) ([]Def, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	var out []Def
	for _, d := range f.defs() {
		pkg := path.Base(path.Dir(d.File))
		if d.Name == name || pkg+"."+d.Name == name || strings.HasSuffix(d.Name, "."+name) {
			out = append(out, d)
		}
	}
	return out, nil
}

func (f *fakeSymbols) Outline(_ context.Context, p string) ([]Def, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	var out []Def
	for _, d := range f.defs() {
		if underDir(p, d.File) {
			out = append(out, d)
		}
	}
	return out, nil
}

func (f *fakeSymbols) Callers(context.Context, string) ([]Site, error)      { return f.callers, f.err }
func (f *fakeSymbols) Callees(context.Context, string) ([]Site, error)      { return f.callees, f.err }
func (f *fakeSymbols) TestsFor(context.Context, string) ([]TestSite, error) { return f.tests, f.err }

func (f *fakeSymbols) SymbolAt(_ context.Context, p string, line int) ([]Def, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.index[p]; !ok {
		return nil, ErrNotIndexed
	}
	var out []Def
	for _, d := range f.defs() {
		if d.File == p && d.StartLine <= line && line <= d.EndLine {
			out = append(out, d)
		}
	}
	return out, nil
}

func (f *fakeSymbols) Sync(_ context.Context, paths []string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.syncs = append(f.syncs, append([]string(nil), paths...))
	changed := false
	for _, p := range paths {
		b, err := os.ReadFile(filepath.Join(f.root, p))
		switch {
		case err != nil:
			if _, ok := f.index[p]; ok {
				delete(f.index, p)
				changed = true
			}
		case f.index[p] != string(b):
			f.index[p] = string(b)
			changed = true
		}
	}
	return changed, nil
}

func (f *fakeSymbols) synced(p string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.syncs {
		for _, q := range s {
			if q == p {
				return true
			}
		}
	}
	return false
}

const pullTypes = `package pull

// Model is a pulled model.
// It has a size.
type Model struct{ n int }

// Select picks one.
func Select(m Model) int {
	return m.n
}
`

const pullMethods = `package pull

func (m Model) Size() int {
	return m.n * 2
}

func (m Model) Close() error {
	return nil
}
`

// symbolWorkspace is a workspace with package pull, whose type Model has its methods in another file, a secret Go
// file, and a fake symbol index over it.
func symbolWorkspace(t *testing.T) (*Workspace, *fakeSymbols, *Set) {
	t.Helper()
	root := t.TempDir()
	for rel, body := range map[string]string{
		"go.mod":               "module x\n",
		"pull/types.go":        pullTypes,
		"pull/methods.go":      pullMethods,
		"pull/other/other.go":  "package other\n\nfunc Close() {\n}\n",
		"certs/server.pem":     "-----BEGIN-----\n",
		"secrets/.env.go":      "package secrets\n\nfunc Leak() {\n}\n",
		"pull/types_test.go":   "package pull\n",
		"pull/methods_test.go": "package pull\n",
	} {
		p := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o644)
	}
	ws, err := NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	sym := newFakeSymbols(t, ws)
	return ws, sym, New(Options{Symbols: sym, Write: true})
}

func TestSymbolShowsADefinitionWithItsDocComment(t *testing.T) {
	ws, _, s := symbolWorkspace(t)
	r := call(t, s, ws, "symbol", `{"name":"pull.Select"}`)
	want := "pull/types.go:8-10 func Select\n" +
		"     7\t// Select picks one.\n" +
		"     8\tfunc Select(m Model) int {\n" +
		"     9\t\treturn m.n\n" +
		"    10\t}\n"
	if r.IsError || r.Text != want {
		t.Fatalf("symbol pull.Select =\n%s\nwant\n%s", r.Text, want)
	}
	// What symbol shows counts as read: edit takes it without a read first.
	if r := call(t, s, ws, "edit", `{"path":"pull/types.go","old_string":"return m.n","new_string":"return m.n + 1"}`); r.IsError {
		t.Fatalf("edit after symbol: %s", r.Text)
	}
}

func TestSymbolMethodAndAmbiguousNames(t *testing.T) {
	ws, _, s := symbolWorkspace(t)
	if r := call(t, s, ws, "symbol", `{"name":"Model.Size"}`); !strings.HasPrefix(r.Text, "pull/methods.go:3-5 method Model.Size\n") {
		t.Fatalf("Model.Size = %q", r.Text)
	}
	r := call(t, s, ws, "symbol", `{"name":"Close"}`)
	for _, want := range []string{"Close has 2 definitions:", "pull/methods.go:7-9 method Model.Close",
		"pull/other/other.go:3-4 func Close", "Name one as pkg.Name or Type.Method, or give its path."} {
		if !strings.Contains(r.Text, want) {
			t.Fatalf("ambiguous Close: no %q in\n%s", want, r.Text)
		}
	}
	// A path narrows a name to the definitions under it.
	if r := call(t, s, ws, "symbol", `{"name":"Close","path":"pull/other"}`); !strings.HasPrefix(r.Text, "pull/other/other.go:3-4 func Close\n") {
		t.Fatalf("Close under pull/other = %q", r.Text)
	}
}

func TestSymbolNotFoundAndErrors(t *testing.T) {
	ws, sym, s := symbolWorkspace(t)
	if r := call(t, s, ws, "symbol", `{"name":"Nope"}`); r.IsError || !r.Empty || !strings.Contains(r.Text, "no definition of Nope") {
		t.Fatalf("missing name: %+v", r)
	}
	if r := call(t, s, ws, "symbol", `{}`); !r.IsError {
		t.Fatal("no name and no path was not an error")
	}
	if r := call(t, s, ws, "symbol", `{"name":"Select","with":"everything"}`); !r.IsError {
		t.Fatal("a bad with was not an error")
	}
	if r := call(t, s, ws, "symbol", `{"name":"Leak"}`); !r.Empty {
		t.Fatalf("a definition in a secrets path was shown: %q", r.Text)
	}
	if r := call(t, s, ws, "symbol", `{"name":"Select","path":"../x"}`); !r.IsError || strings.Contains(r.Text, ws.Root()) {
		t.Fatalf("a path outside the workspace: %q", r.Text)
	}
	sym.err = ErrSymbolsBuilding
	if r := call(t, s, ws, "symbol", `{"name":"Select"}`); !r.IsError || !strings.Contains(r.Text, "still building") {
		t.Fatalf("building: %q", r.Text)
	}
	sym.err = ErrNoSymbols
	if r := call(t, s, ws, "symbol", `{"path":"pull"}`); !r.IsError || !strings.Contains(r.Text, "use grep and read") {
		t.Fatalf("no index: %q", r.Text)
	}
}

func TestSymbolPathListsWhatIsDefinedThere(t *testing.T) {
	ws, _, s := symbolWorkspace(t)
	r := call(t, s, ws, "symbol", `{"path":"pull/types.go"}`)
	want := "pull/types.go\n  5-5 type Model\n  8-10 func Select\n"
	if r.Text != want {
		t.Fatalf("outline =\n%s\nwant\n%s", r.Text, want)
	}
	r = call(t, s, ws, "symbol", `{"path":"."}`)
	if !strings.Contains(r.Text, "pull/methods.go\n  3-5 method Model.Size\n") || strings.Contains(r.Text, "Leak") {
		t.Fatalf("outline of the root =\n%s", r.Text)
	}
	if r := call(t, s, ws, "symbol", `{"path":"secrets/.env.go"}`); !r.IsError {
		t.Fatalf("a secrets path was listed: %q", r.Text)
	}
}

func TestSymbolWithTestsGivesTheCommandsToRunThem(t *testing.T) {
	ws, sym, s := symbolWorkspace(t)
	sym.tests = []TestSite{
		{File: "pull/types_test.go", Name: "TestSelect", Line: 3, Dir: "pull"},
		{File: "pull/methods_test.go", Name: "TestSize", Line: 9, Dir: "pull", Via: "Model.Size"},
		{File: "main_test.go", Name: "TestMain", Line: 1, Dir: "."},
	}
	r := call(t, s, ws, "symbol", `{"name":"Select","with":"tests"}`)
	for _, want := range []string{"tests (3):\n", "  pull/types_test.go:3 TestSelect\n",
		"  pull/methods_test.go:9 TestSize, through Model.Size\n",
		"run: go test -run '^TestMain$' .\n", "run: go test -run '^(TestSelect|TestSize)$' ./pull\n"} {
		if !strings.Contains(r.Text, want) {
			t.Fatalf("no %q in\n%s", want, r.Text)
		}
	}
	sym.tests = nil
	r = call(t, s, ws, "symbol", `{"name":"Select","with":"tests"}`)
	if !strings.Contains(r.Text, "no test calls Select; go test ./pull runs its package's tests\n") {
		t.Fatalf("no tests:\n%s", r.Text)
	}
}

func TestSymbolWithCallersAndCallees(t *testing.T) {
	ws, sym, s := symbolWorkspace(t)
	sym.callers = []Site{{File: "pull/methods.go", Line: 4, Name: "Model.Size"}, {File: "secrets/.env.go", Line: 3, Name: "Leak"}}
	r := call(t, s, ws, "symbol", `{"name":"Select","with":"callers"}`)
	if !strings.Contains(r.Text, "callers (1):\n  pull/methods.go:4 in Model.Size: return m.n * 2\n") || strings.Contains(r.Text, "Leak") {
		t.Fatalf("callers:\n%s", r.Text)
	}
	sym.callees = []Site{{File: "pull/types.go", Line: 9, Name: "m.n"}, {File: "pull/other/other.go", Line: 3, Name: "elsewhere"}}
	r = call(t, s, ws, "symbol", `{"name":"Select","with":"callees"}`)
	if !strings.Contains(r.Text, "calls:\n  9 m.n\n") || strings.Contains(r.Text, "elsewhere") {
		t.Fatalf("callees:\n%s", r.Text)
	}
}

// TestSymbolSyncsAFileChangedOutsideTheTools: a file changed on disk since the index read it (as bash or an editor
// would) is synced before its spans are used, so symbol shows the definition where it is now.
func TestSymbolSyncsAFileChangedOutsideTheTools(t *testing.T) {
	ws, sym, s := symbolWorkspace(t)
	p := filepath.Join(ws.Root(), "pull/types.go")
	os.WriteFile(p, []byte(strings.Replace(pullTypes, "package pull\n", "package pull\n\n// added\n// lines\n", 1)), 0o644)
	r := call(t, s, ws, "symbol", `{"name":"Select"}`)
	if !strings.HasPrefix(r.Text, "pull/types.go:11-13 func Select\n") || !strings.Contains(r.Text, "    11\tfunc Select(m Model) int {\n") {
		t.Fatalf("after a change on disk, symbol Select =\n%s", r.Text)
	}
	if !sym.synced("pull/types.go") {
		t.Fatal("the definition's file was not synced")
	}
}

func TestEditBySymbolReplacesTheDefinition(t *testing.T) {
	ws, sym, s := symbolWorkspace(t)
	call(t, s, ws, "symbol", `{"name":"Model.Size"}`)
	r := call(t, s, ws, "edit", `{"path":"pull/methods.go","symbol":"Model.Size","new_string":"func (m Model) Size() int {\n\tif m.n < 0 {\n\t\treturn 0\n\t}\n\treturn m.n * 2\n}"}`)
	if r.IsError || r.Text != "edited pull/methods.go: replaced Model.Size, lines 3-5, now lines 3-8" {
		t.Fatalf("edit by symbol: %q", r.Text)
	}
	want := strings.Replace(pullMethods, "func (m Model) Size() int {\n", "func (m Model) Size() int {\n\tif m.n < 0 {\n\t\treturn 0\n\t}\n", 1)
	if got := content(t, ws, "pull/methods.go"); got != want {
		t.Fatalf("file =\n%s\nwant\n%s", got, want)
	}
	if r.State != ContentState(want) {
		t.Fatal("the result's State is not the file's content after the edit")
	}
	// The index follows the edit: Close moved down three lines.
	if r := call(t, s, ws, "symbol", `{"name":"Model.Close"}`); !strings.HasPrefix(r.Text, "pull/methods.go:10-12 method Model.Close\n") {
		t.Fatalf("after the edit, Model.Close = %q", r.Text)
	}
	if len(sym.syncs) == 0 {
		t.Fatal("no sync after the edit")
	}
}

func TestEditBySymbolKeepsOrReplacesTheDocComment(t *testing.T) {
	ws, _, s := symbolWorkspace(t)
	call(t, s, ws, "symbol", `{"name":"Select"}`)
	// Without a leading comment, the declaration is replaced and its comment kept.
	call(t, s, ws, "edit", `{"path":"pull/types.go","symbol":"Select","new_string":"func Select(m Model) int {\n\treturn 0\n}\n"}`)
	if got := content(t, ws, "pull/types.go"); !strings.Contains(got, "// Select picks one.\nfunc Select(m Model) int {\n\treturn 0\n}\n") {
		t.Fatalf("comment not kept:\n%s", got)
	}
	// With one, the comment is replaced too: no duplicate.
	call(t, s, ws, "edit", `{"path":"pull/types.go","symbol":"Select","new_string":"// Select picks none.\nfunc Select(m Model) int {\n\treturn -1\n}\n"}`)
	got := content(t, ws, "pull/types.go")
	if strings.Contains(got, "picks one") || !strings.Contains(got, "\n// Select picks none.\nfunc Select(m Model) int {\n\treturn -1\n}\n") {
		t.Fatalf("comment not replaced:\n%s", got)
	}
	// A multi-line comment is replaced whole.
	call(t, s, ws, "edit", `{"path":"pull/types.go","symbol":"Model","new_string":"// Model is one model.\ntype Model struct{ n, m int }"}`)
	if got := content(t, ws, "pull/types.go"); !strings.HasPrefix(got, "package pull\n\n// Model is one model.\ntype Model struct{ n, m int }\n\n") {
		t.Fatalf("multi-line comment:\n%s", got)
	}
}

func TestEditBySymbolRefusals(t *testing.T) {
	ws, _, s := symbolWorkspace(t)
	edit := func(input string) Result { return call(t, s, ws, "edit", input) }
	if r := edit(`{"path":"pull/methods.go","symbol":"Model.Size","new_string":"x"}`); !r.IsError || !strings.Contains(r.Text, "before editing it") {
		t.Fatalf("unseen file: %q", r.Text)
	}
	read(t, s, ws, "pull/methods.go")
	if r := edit(`{"path":"pull/methods.go","symbol":"Model.Size","old_string":"a","new_string":"b"}`); !r.IsError || !strings.Contains(r.Text, "not both") {
		t.Fatalf("both: %q", r.Text)
	}
	if r := edit(`{"path":"pull/methods.go","symbol":"Select","new_string":"x"}`); !r.IsError || !strings.Contains(r.Text, "no definition of Select in") {
		t.Fatalf("symbol in another file: %q", r.Text)
	}
	os.WriteFile(filepath.Join(ws.Root(), "pull/methods.go"), []byte(pullMethods+"\nfunc (o Other) Size() int {\n\treturn 1\n}\n"), 0o644)
	read(t, s, ws, "pull/methods.go")
	if r := edit(`{"path":"pull/methods.go","symbol":"Size","new_string":"x"}`); !r.IsError || !strings.Contains(r.Text, "Model.Size at line 3, Other.Size at line 11") {
		t.Fatalf("ambiguous in the file: %q", r.Text)
	}
	if r := edit(`{"path":"pull/methods.go","symbol":"Model.Size","new_string":"func (m Model) Size() int {\n\treturn m.n * 2\n}"}`); !r.IsError || !strings.Contains(r.Text, "nothing to change") {
		t.Fatalf("unchanged: %q", r.Text)
	}
	// The symbol changed on disk since the model saw it: refused, and the file is left alone.
	changed := strings.Replace(content(t, ws, "pull/methods.go"), "return m.n * 2", "return m.n * 3", 1)
	os.WriteFile(filepath.Join(ws.Root(), "pull/methods.go"), []byte(changed), 0o644)
	if r := edit(`{"path":"pull/methods.go","symbol":"Model.Size","new_string":"func (m Model) Size() int { return 0 }"}`); !r.IsError || !strings.Contains(r.Text, "changed since you last read it, in Model.Size") {
		t.Fatalf("changed symbol: %q", r.Text)
	}
	if content(t, ws, "pull/methods.go") != changed {
		t.Fatal("a refused edit changed the file")
	}
	// A change elsewhere in the file is no reason to refuse; the spans are synced first.
	ws2, _, s2 := symbolWorkspace(t)
	call(t, s2, ws2, "symbol", `{"name":"Model.Close"}`)
	os.WriteFile(filepath.Join(ws2.Root(), "pull/methods.go"), []byte(strings.Replace(pullMethods, "package pull\n", "package pull\n\n// moved\n", 1)), 0o644)
	r := call(t, s2, ws2, "edit", `{"path":"pull/methods.go","symbol":"Model.Close","new_string":"func (m Model) Close() error { return nil }"}`)
	if r.IsError || !strings.HasSuffix(content(t, ws2, "pull/methods.go"), "// moved\n\nfunc (m Model) Size() int {\n\treturn m.n * 2\n}\n\nfunc (m Model) Close() error { return nil }\n") {
		t.Fatalf("change elsewhere: %q\n%s", r.Text, content(t, ws2, "pull/methods.go"))
	}
}

// TestEditBySymbolRefusesWhatSymbolCannotShowWhole: a definition longer than symbol shows, or with a line it cuts, is
// refused, since the model would write its replacement from the cut text; the file is left alone.
func TestEditBySymbolRefusesWhatSymbolCannotShowWhole(t *testing.T) {
	ws, _, _ := symbolWorkspace(t)
	var long strings.Builder
	long.WriteString("package pull\n\nfunc Long() {\n")
	for range readMaxLines {
		long.WriteString("\tx()\n")
	}
	long.WriteString("}\n\nfunc Wide() {\n\ty := \"" + strings.Repeat("w", maxLineRunes) + "\"\n}\n")
	os.WriteFile(filepath.Join(ws.Root(), "pull/long.go"), []byte(long.String()), 0o644)
	s := New(Options{Symbols: newFakeSymbols(t, ws), Write: true})
	read(t, s, ws, "pull/long.go")
	for name, want := range map[string]string{"Long": "202 lines, more than symbol shows", "Wide": "a line longer than symbol shows whole"} {
		r := call(t, s, ws, "edit", `{"path":"pull/long.go","symbol":"`+name+`","new_string":"func `+name+`() {}"}`)
		if !r.IsError || !strings.Contains(r.Text, want) {
			t.Fatalf("%s: %q", name, r.Text)
		}
	}
	if content(t, ws, "pull/long.go") != long.String() {
		t.Fatal("a refused edit changed the file")
	}
}

func TestEditBySymbolRefusesADuplicate(t *testing.T) {
	ws, _, _ := symbolWorkspace(t)
	sym := newFakeSymbols(t, ws)
	s := New(Options{Symbols: sym, Write: true, RefuseDuplicateEdits: true})
	call(t, s, ws, "symbol", `{"name":"Model.Size"}`)
	in := `{"path":"pull/methods.go","symbol":"Model.Size","new_string":"func (m Model) Size() int {\n\treturn 7\n}"}`
	back := `{"path":"pull/methods.go","symbol":"Model.Size","new_string":"func (m Model) Size() int {\n\treturn m.n * 2\n}"}`
	for i, input := range []string{in, back} {
		if r := call(t, s, ws, "edit", input); r.IsError {
			t.Fatalf("edit %d: %q", i, r.Text)
		}
	}
	if r := call(t, s, ws, "edit", in); !r.IsError || !strings.Contains(r.Text, "already applied") {
		t.Fatalf("a repeated edit by symbol: %q", r.Text)
	}
}

func TestPlainEditNamesTheSymbolAndSyncs(t *testing.T) {
	ws, sym, s := symbolWorkspace(t)
	read(t, s, ws, "pull/methods.go")
	r := call(t, s, ws, "edit", `{"path":"pull/methods.go","old_string":"return nil","new_string":"return m.err"}`)
	if r.Text != "edited pull/methods.go at line 8, in Model.Close" {
		t.Fatalf("edit = %q", r.Text)
	}
	if r := call(t, s, ws, "edit", `{"path":"pull/methods.go","old_string":"package pull","new_string":"package pull // x"}`); r.Text != "edited pull/methods.go at line 1" {
		t.Fatalf("edit at file scope = %q", r.Text)
	}
	if r := call(t, s, ws, "write", `{"path":"pull/new.go","content":"package pull\n\nfunc New() {\n}\n"}`); r.IsError {
		t.Fatal(r.Text)
	}
	if !sym.synced("pull/new.go") {
		t.Fatal("write did not sync the index")
	}
	if r := call(t, s, ws, "symbol", `{"name":"New"}`); !strings.HasPrefix(r.Text, "pull/new.go:3-4 func New\n") {
		t.Fatalf("a written file's symbol: %q", r.Text)
	}
}

// TestNoSymbolsNoChange: without Symbols there is no symbol tool, edit's schema has no symbol field and still requires
// old_string, a symbol field is bad input, and read's hint is unchanged: a session without ken sends what it sent
// before.
func TestNoSymbolsNoChange(t *testing.T) {
	ws, _, _ := symbolWorkspace(t)
	s := New(Options{Write: true})
	if _, ok := s.Get("symbol"); ok {
		t.Fatal("symbol offered without Symbols")
	}
	e, _ := s.Get("edit")
	spec := e.Spec()
	if strings.Contains(string(spec.InputSchema), "symbol") || !strings.Contains(string(spec.InputSchema), `"required":["path","old_string","new_string"]`) ||
		strings.Contains(spec.Description, "symbol") {
		t.Fatalf("edit without Symbols: %s %s", spec.Description, spec.InputSchema)
	}
	read(t, s, ws, "pull/methods.go")
	if r := call(t, s, ws, "edit", `{"path":"pull/methods.go","symbol":"Model.Size","new_string":"x"}`); !r.IsError || !strings.Contains(r.Text, "bad input") {
		t.Fatalf("symbol without Symbols: %q", r.Text)
	}
	with := New(Options{Symbols: newFakeSymbols(t, ws), Write: true})
	if e, _ := with.Get("edit"); !strings.Contains(string(e.Spec().InputSchema), `"symbol"`) {
		t.Fatal("edit with Symbols has no symbol field")
	}
	if with.Specs()[0].Name != "symbol" {
		t.Fatalf("symbol is not first without search: %s", with.Specs()[0].Name)
	}
	var schema map[string]any
	if err := json.Unmarshal(with.Specs()[0].InputSchema, &schema); err != nil {
		t.Fatalf("symbol's schema: %v", err)
	}
}

func TestReadHintsAtSymbolOnALongFile(t *testing.T) {
	ws := workspace(t)
	plain := call(t, New(Options{}), ws, "read", `{"path":"long.txt"}`)
	if strings.Contains(plain.Text, "symbol") {
		t.Fatal("read without Symbols mentions symbol")
	}
	r := call(t, New(Options{Symbols: newFakeSymbols(t, ws)}), ws, "read", `{"path":"long.txt"}`)
	if r.Text != plain.Text+"symbol with path \"long.txt\" lists its functions and types with their lines.\n" {
		t.Fatalf("read with Symbols ends %q", r.Text[len(r.Text)-120:])
	}
}

func TestLineRange(t *testing.T) {
	c := "a\nbb\nccc\n"
	for _, tc := range []struct {
		start, end int
		want       string
		ok         bool
	}{{1, 1, "a\n", true}, {2, 3, "bb\nccc\n", true}, {3, 3, "ccc\n", true}, {3, 4, "", false}, {0, 1, "", false}, {2, 1, "", false}} {
		a, b, ok := lineRange(c, tc.start, tc.end)
		if ok != tc.ok || ok && c[a:b] != tc.want {
			t.Errorf("lineRange(%d, %d) = %q, %v", tc.start, tc.end, c[a:b], ok)
		}
	}
	if a, b, ok := lineRange("x\ny", 2, 2); !ok || "x\ny"[a:b] != "y" {
		t.Error("a last line without a newline")
	}
}
