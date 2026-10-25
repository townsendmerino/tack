package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/townsendmerino/tack/internal/server"
)

// Symbols is the seam for ken's structural index (DESIGN §12, K11–K15): definitions and outlines with their spans,
// call sites, a symbol's callees and tests, the symbol at a line, and a refresh after a file changes. kensearch
// implements it over engine.Workspace. Paths are relative to the workspace root, with forward slashes, as ken
// reports them.
type Symbols interface {
	// Definition returns name's definitions: Name, pkg.Name, Type.Method or pkg.Type.Method (K14).
	Definition(ctx context.Context, name string) ([]Def, error)
	// Outline returns the symbols defined in a file, or in every file under a directory, in file and line order.
	Outline(ctx context.Context, path string) ([]Def, error)
	// Callers returns each call to name, by file and then line; Site.Name is the function the call is in.
	Callers(ctx context.Context, name string) ([]Site, error)
	// Callees returns the calls made inside name's definitions; Site.Name is the callee as written, receiver included.
	Callees(ctx context.Context, name string) ([]Site, error)
	// TestsFor returns the tests that call name, directly or through one other function.
	TestsFor(ctx context.Context, name string) ([]TestSite, error)
	// SymbolAt returns the symbols whose spans hold line in path, innermost first; ErrNotIndexed for a file the
	// index does not hold.
	SymbolAt(ctx context.Context, path string, line int) ([]Def, error)
	// Sync brings the index up to date, before it returns, for each file in paths that changed since it last synced it
	// or that it has never synced, and reports whether it refreshed any. A file that no longer exists is dropped.
	Sync(ctx context.Context, paths []string) (bool, error)
}

// Errors a Symbols implementation returns, wrapped or not.
var (
	ErrSymbolsBuilding = errors.New("the symbol index is still building")
	ErrNoSymbols       = errors.New("this repository has no symbol index")
	ErrNotIndexed      = errors.New("the file is not in the symbol index")
)

// Def is one symbol and its lines, 1-based and inclusive.
type Def struct {
	File string
	// Name is qualified for a method ("Model.Size") and bare otherwise.
	Name      string
	Kind      string // "func", "type" or "method"
	StartLine int
	EndLine   int
	// DocLine is the first line of a Go doc comment directly above StartLine, or 0 when there is none.
	DocLine int
}

// Site is one line in a file: a call to a symbol, or a call a symbol makes.
type Site struct {
	File string
	Line int
	Name string
}

// TestSite is one test function and the directory to run it from.
type TestSite struct {
	File string
	Name string
	Line int
	Dir  string // "." at the root
	Via  string // the function it calls the symbol through, or "" when it calls it directly
}

const (
	symbolMaxDefs    = 20  // definitions listed for an ambiguous name
	symbolMaxSites   = 20  // callers listed
	symbolMaxCallees = 30  // callees listed
	symbolMaxTests   = 20  // tests listed
	symbolMaxOutline = 150 // outline entries listed for a path
)

type symbolTool struct{ sym Symbols }

func (symbolTool) ReadOnly() bool { return true }
func (symbolTool) Spec() server.Tool {
	return server.Tool{Name: "symbol",
		Description: "Show a function, type or method with line numbers, by name: Name, pkg.Name or Type.Method. with adds its callers, callees or tests. A path alone lists the symbols defined there.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"},"path":{"type":"string","description":"file or directory, relative to the repo root"},"with":{"type":"string","enum":["callers","callees","tests"]}}}`)}
}

func (t symbolTool) Run(ctx context.Context, ws *Workspace, input json.RawMessage) (Result, error) {
	var in struct {
		Name string `json:"name"`
		Path string `json:"path"`
		With string `json:"with"`
	}
	if err := decode(input, &in); err != nil {
		return errorf("bad input: %v; symbol takes name, and optionally path and with", err), nil
	}
	switch in.With {
	case "", "callers", "callees", "tests":
	default:
		return errorf("with is callers, callees or tests, not %q", in.With), nil
	}
	if in.Name == "" && in.Path == "" {
		return errorf("symbol needs a name, such as pkg.Func or Type.Method, or a path to list the symbols defined there"), nil
	}
	under := ""
	if in.Path != "" {
		p, err := ws.Resolve(in.Path)
		if err != nil {
			return ws.pathError(in.Path, err), nil
		}
		under = ws.Rel(p)
	}
	if in.Name == "" {
		return t.outline(ctx, ws, in.Path, under), nil
	}
	defs, err := definitions(ctx, ws, t.sym, in.Name, under)
	if err != nil {
		return symbolError(err), nil
	}
	switch {
	case len(defs) == 0 && in.Path != "":
		return Result{Text: fmt.Sprintf("no definition of %s in %s; symbol with only the path lists what is defined there", in.Name, in.Path),
			Empty: true}, nil
	case len(defs) == 0:
		return Result{Text: fmt.Sprintf("no definition of %s; search finds code by meaning, grep finds text", in.Name), Empty: true}, nil
	case len(defs) > 1:
		return Result{Text: ambiguous(in.Name, defs)}, nil
	}
	d := defs[0]
	var b strings.Builder
	if err := show(ws, &b, d); err != nil {
		return ws.pathError(d.File, err), nil
	}
	switch in.With {
	case "callers":
		t.callers(ctx, ws, &b, in.Name)
	case "callees":
		t.callees(ctx, &b, in.Name, d.File)
	case "tests":
		t.tests(ctx, &b, in.Name, d)
	}
	return Result{Text: b.String()}, nil
}

// definitions returns name's definitions under the path under ("" for anywhere), outside the secrets deny-list, with
// spans that match each file as it is now: their files are synced, and the lookup is made again if that changed any.
func definitions(ctx context.Context, ws *Workspace, sym Symbols, name, under string) ([]Def, error) {
	lookup := func() ([]Def, error) {
		all, err := sym.Definition(ctx, name)
		if err != nil {
			return nil, err
		}
		var out []Def
		for _, d := range all {
			if underDir(under, d.File) && !isSecret(filepath.Join(ws.root, d.File)) {
				out = append(out, d)
			}
		}
		return out, nil
	}
	defs, err := lookup()
	if err != nil || len(defs) > symbolMaxDefs {
		return defs, err
	}
	files := make([]string, len(defs))
	for i, d := range defs {
		files[i] = d.File
	}
	if changed, err := sym.Sync(ctx, sortedUnique(files)); err != nil || !changed {
		return defs, err
	}
	return lookup()
}

// underDir reports whether the slash path p is dir or under it; every path is under "" and ".".
func underDir(dir, p string) bool {
	return dir == "" || dir == "." || p == dir || strings.HasPrefix(p, dir+"/")
}

// show writes d's header and its lines, from its doc comment, numbered as read numbers them, and records the file as
// seen: what symbol shows can be edited, as after a read.
func show(ws *Workspace, b *strings.Builder, d Def) error {
	abs, err := ws.Resolve(d.File)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return err
	}
	content := string(data)
	ws.saw(abs, content)
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	start, end := d.StartLine, min(d.EndLine, len(lines))
	if d.DocLine > 0 && d.DocLine < start {
		start = d.DocLine
	}
	fmt.Fprintf(b, "%s:%d-%d %s %s\n", d.File, d.StartLine, d.EndLine, d.Kind, d.Name)
	last := min(end, start-1+readMaxLines)
	ws.showed(abs, content, start, last)
	for i := start; i <= last; i++ {
		fmt.Fprintf(b, "%6d\t%s\n", i, cut(lines[i-1]))
	}
	if last < end {
		fmt.Fprintf(b, "… %s is lines %d-%d; read %s with offset %d for the rest\n", d.Name, d.StartLine, d.EndLine, d.File, last+1)
	}
	return nil
}

func ambiguous(name string, defs []Def) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s has %d definitions:\n", name, len(defs))
	for i, d := range defs {
		if i == symbolMaxDefs {
			fmt.Fprintf(&b, "… %d more\n", len(defs)-i)
			break
		}
		fmt.Fprintf(&b, "%s:%d-%d %s %s\n", d.File, d.StartLine, d.EndLine, d.Kind, d.Name)
	}
	b.WriteString("Name one as pkg.Name or Type.Method, or give its path.\n")
	return b.String()
}

func (t symbolTool) callers(ctx context.Context, ws *Workspace, b *strings.Builder, name string) {
	sites, err := t.sym.Callers(ctx, name)
	if err != nil {
		fmt.Fprintf(b, "callers: %s\n", symbolError(err).Text)
		return
	}
	var keep []Site
	for _, s := range sites {
		if !isSecret(filepath.Join(ws.root, s.File)) {
			keep = append(keep, s)
		}
	}
	if len(keep) == 0 {
		fmt.Fprintf(b, "no calls to %s found; grep finds other uses\n", name)
		return
	}
	fmt.Fprintf(b, "callers (%d):\n", len(keep))
	files := map[string][]string{}
	for i, s := range keep {
		if i == symbolMaxSites {
			fmt.Fprintf(b, "… %d more; grep finds every one\n", len(keep)-i)
			break
		}
		lines, ok := files[s.File]
		if !ok {
			lines = fileLines(ws, s.File)
			files[s.File] = lines
		}
		text := ""
		if s.Line >= 1 && s.Line <= len(lines) {
			text = ": " + cut(strings.TrimSpace(lines[s.Line-1]))
		}
		in := ""
		if s.Name != "" {
			in = " in " + s.Name
		}
		fmt.Fprintf(b, "  %s:%d%s%s\n", s.File, s.Line, in, text)
	}
}

// fileLines is rel's lines, or nil when it cannot be read.
func fileLines(ws *Workspace, rel string) []string {
	abs, err := ws.Resolve(rel)
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil
	}
	return strings.Split(string(data), "\n")
}

// callees lists the calls inside the definition in file.
func (t symbolTool) callees(ctx context.Context, b *strings.Builder, name, file string) {
	sites, err := t.sym.Callees(ctx, name)
	if err != nil {
		fmt.Fprintf(b, "calls: %s\n", symbolError(err).Text)
		return
	}
	var keep []Site
	for _, s := range sites {
		if s.File == file {
			keep = append(keep, s)
		}
	}
	if len(keep) == 0 {
		fmt.Fprintf(b, "%s calls nothing the index records\n", name)
		return
	}
	b.WriteString("calls:\n")
	for i, s := range keep {
		if i == symbolMaxCallees {
			fmt.Fprintf(b, "… %d more\n", len(keep)-i)
			break
		}
		fmt.Fprintf(b, "  %d %s\n", s.Line, s.Name)
	}
}

// tests lists the tests that call d and, for Go, the go test commands that run them: one per package directory. With
// none, it names the package to run instead.
func (t symbolTool) tests(ctx context.Context, b *strings.Builder, name string, d Def) {
	ts, err := t.sym.TestsFor(ctx, name)
	if err != nil {
		fmt.Fprintf(b, "tests: %s\n", symbolError(err).Text)
		return
	}
	if len(ts) == 0 {
		if strings.HasSuffix(d.File, ".go") {
			fmt.Fprintf(b, "no test calls %s; go test %s runs its package's tests\n", name, goPkg(path.Dir(d.File)))
		} else {
			fmt.Fprintf(b, "no test calls %s\n", name)
		}
		return
	}
	fmt.Fprintf(b, "tests (%d):\n", len(ts))
	byDir := map[string][]string{}
	var dirs []string
	for i, s := range ts {
		if i == symbolMaxTests {
			fmt.Fprintf(b, "… %d more\n", len(ts)-i)
			break
		}
		via := ""
		if s.Via != "" {
			via = ", through " + s.Via
		}
		fmt.Fprintf(b, "  %s:%d %s%s\n", s.File, s.Line, s.Name, via)
		if strings.HasSuffix(s.File, "_test.go") && !strings.Contains(s.Name, ".") {
			if _, ok := byDir[s.Dir]; !ok {
				dirs = append(dirs, s.Dir)
			}
			byDir[s.Dir] = append(byDir[s.Dir], s.Name)
		}
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		names := sortedUnique(byDir[dir])
		re := names[0]
		if len(names) > 1 {
			re = "(" + strings.Join(names, "|") + ")"
		}
		fmt.Fprintf(b, "run: go test -run '^%s$' %s\n", re, goPkg(dir))
	}
}

// goPkg is a package directory as go test takes it.
func goPkg(dir string) string {
	if dir == "" || dir == "." {
		return "."
	}
	return "./" + dir
}

// outline lists the symbols defined in the file or directory rel, which the model gave as given.
func (t symbolTool) outline(ctx context.Context, ws *Workspace, given, rel string) Result {
	if st, err := os.Stat(filepath.Join(ws.root, rel)); err == nil && !st.IsDir() {
		if _, err := t.sym.Sync(ctx, []string{rel}); err != nil {
			return symbolError(err)
		}
	}
	defs, err := t.sym.Outline(ctx, rel)
	if err != nil {
		return symbolError(err)
	}
	var keep []Def
	for _, d := range defs {
		if d.StartLine > 0 && !isSecret(filepath.Join(ws.root, d.File)) {
			keep = append(keep, d)
		}
	}
	if len(keep) == 0 {
		return Result{Text: fmt.Sprintf("no symbols indexed in %s; glob lists its files, read shows one", given), Empty: true}
	}
	var b strings.Builder
	file := ""
	for i, d := range keep {
		if i == symbolMaxOutline {
			fmt.Fprintf(&b, "… %d more; give a narrower path\n", len(keep)-i)
			break
		}
		if d.File != file {
			file = d.File
			b.WriteString(file + "\n")
		}
		fmt.Fprintf(&b, "  %d-%d %s %s\n", d.StartLine, d.EndLine, d.Kind, d.Name)
	}
	return Result{Text: b.String()}
}

// symbolError is the result for a Symbols error: what to use instead.
func symbolError(err error) Result {
	switch {
	case errors.Is(err, ErrSymbolsBuilding):
		return errorf("the symbol index is still building; try again shortly, or use grep and read")
	case errors.Is(err, ErrNoSymbols):
		return errorf("this repository has no symbol index; use grep and read")
	case errors.Is(err, ErrNotIndexed):
		return errorf("that file is not in the symbol index; read it instead")
	}
	return errorf("symbol: %v", err)
}
