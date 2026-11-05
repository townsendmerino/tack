package mine

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Stub is a candidate's stub check (docs/measurements/e1-task-selection-preregistration-2026-10-16.md, amendment 3):
// the fix applied, every function it added or changed reduced to a bare return of zero values, and the oracle run.
type Stub struct {
	Funcs   []string          `json:"funcs"`             // file: Recv.Name of each function stubbed
	Results map[string]string `json:"results,omitempty"` // each oracle test on the stubbed tree
	// Rejected is why the check rejects the candidate, one of the Reject* reasons, or "" when it keeps it.
	Rejected string `json:"rejected,omitempty"`
	Err      string `json:"err,omitempty"` // what stopped the check, for RejectNoRun
}

// The stub check's reasons to reject (amendment 3). A stubbed tree that does not build is not a test telling the fix
// from no fix; the check did not run, so it rejects as RejectNoRun does.
const (
	RejectPassed  = "passed while stubbed"
	RejectNoRun   = "could not run"
	RejectNoBuild = "did not build"
)

// StubCheck runs the stub check for c in work, a PrepareClone clone, against the oracle tests Verify found. A fix
// that changed no function body (only types, variables or constants) has nothing to stub and is not rejected.
func StubCheck(ctx context.Context, r Repo, c Candidate, oracle []string, work string) Stub {
	s := Stub{}
	fail := func(format string, args ...any) Stub {
		s.Err, s.Rejected = fmt.Sprintf(format, args...), RejectNoRun
		return s
	}
	if err := checkout(work, c.Commit); err != nil {
		return fail("checkout commit: %v", err)
	}
	names, err := git(r.Path, "diff", "--name-only", c.Base, c.Commit)
	if err != nil {
		return fail("diff: %v", err)
	}
	for f := range strings.FieldsSeq(names) {
		if !isNonTestGo(f) {
			continue
		}
		p := filepath.Join(work, f)
		src, err := os.ReadFile(p)
		if os.IsNotExist(err) { // deleted by the fix
			continue
		} else if err != nil {
			return fail("read %s: %v", f, err)
		}
		old, _ := git(r.Path, "show", c.Base+":"+f) // absent on the parent: every function is new
		out, funcs, err := stubSource(work, f, old, string(src))
		if err != nil {
			return fail("stub %s: %v", f, err)
		}
		if len(funcs) == 0 {
			continue
		}
		if err := os.WriteFile(p, out, 0o644); err != nil {
			return fail("write %s: %v", f, err)
		}
		s.Funcs = append(s.Funcs, funcs...)
	}
	if len(s.Funcs) == 0 {
		return s
	}
	s.Results, _ = RunTests(ctx, work, oracle, c.Packages)
	s.Rejected = stubVerdict(oracle, s.Results)
	return s
}

// stubVerdict judges the oracle's results on the stubbed tree: any test that did not build rejects as
// RejectNoBuild, all passing as RejectPassed, and otherwise the oracle told the fix from no fix.
func stubVerdict(oracle []string, res map[string]string) string {
	passed := 0
	for _, t := range oracle {
		switch res[t] {
		case "build-fail":
			return RejectNoBuild
		case "pass":
			passed++
		}
	}
	if passed == len(oracle) {
		return RejectPassed
	}
	return ""
}

// stubSource replaces, in src (file name, at the fix), the body of every top-level function or method whose
// declaration differs from old (the parent's version, "" when the file is new) with a bare return of zero values:
// `return` for named results, `return *new(T1), *new(T2)` otherwise. Imports left unused are removed so the package
// still builds. It returns the new source and the functions it stubbed.
func stubSource(dir, name, old, src string) ([]byte, []string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, parser.ParseComments)
	if err != nil {
		return nil, nil, err
	}
	before := map[string]string{}
	if old != "" {
		ofs := token.NewFileSet()
		if of, err := parser.ParseFile(ofs, name, old, parser.ParseComments); err == nil {
			for k, fd := range funcDecls(of) {
				before[k] = old[ofs.Position(fd.Pos()).Offset:ofs.Position(fd.End()).Offset]
			}
		}
	}
	var stubbed []string
	var cut []*ast.BlockStmt // the bodies replaced, to drop the comments inside them
	for k, fd := range funcDecls(f) {
		if fd.Body == nil || before[k] == src[fset.Position(fd.Pos()).Offset:fset.Position(fd.End()).Offset] {
			continue
		}
		cut = append(cut, fd.Body)
		fd.Body = &ast.BlockStmt{Lbrace: fd.Body.Lbrace, Rbrace: fd.Body.Lbrace + 1, List: zeroReturn(fd.Type.Results)}
		stubbed = append(stubbed, name+": "+k)
	}
	if len(stubbed) == 0 {
		return nil, nil, nil
	}
	sort.Strings(stubbed)
	var kept []*ast.CommentGroup
	for _, cg := range f.Comments {
		inside := false
		for _, b := range cut {
			if cg.Pos() > b.Lbrace && cg.End() <= b.Rbrace {
				inside = true
			}
		}
		if !inside {
			kept = append(kept, cg)
		}
	}
	f.Comments = kept
	dropUnusedImports(dir, f)
	var buf bytes.Buffer
	if err := format.Node(&buf, fset, f); err != nil {
		return nil, nil, err
	}
	return buf.Bytes(), stubbed, nil
}

// funcDecls keys a file's top-level functions as Recv.Name (Name for a function; init gets its index).
func funcDecls(f *ast.File) map[string]*ast.FuncDecl {
	m := map[string]*ast.FuncDecl{}
	inits := 0
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}
		k := fd.Name.Name
		if fd.Recv != nil && len(fd.Recv.List) > 0 {
			k = recvName(fd.Recv.List[0].Type) + "." + k
		} else if k == "init" {
			k = fmt.Sprintf("init#%d", inits)
			inits++
		}
		m[k] = fd
	}
	return m
}

func recvName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return recvName(t.X)
	case *ast.IndexExpr:
		return recvName(t.X)
	case *ast.IndexListExpr:
		return recvName(t.X)
	case *ast.Ident:
		return t.Name
	}
	return "?"
}

// zeroReturn is the body's statement list: nothing for no results, a bare return for named ones, else *new(T) per value.
func zeroReturn(results *ast.FieldList) []ast.Stmt {
	if results == nil || len(results.List) == 0 {
		return nil
	}
	if len(results.List[0].Names) > 0 {
		return []ast.Stmt{&ast.ReturnStmt{}}
	}
	var vals []ast.Expr
	for _, fl := range results.List {
		vals = append(vals, &ast.StarExpr{X: &ast.CallExpr{Fun: ast.NewIdent("new"), Args: []ast.Expr{fl.Type}}})
	}
	return []ast.Stmt{&ast.ReturnStmt{Results: vals}}
}

// dropUnusedImports removes the imports nothing in f refers to any more. A package's name comes from go list in dir;
// an import whose name it cannot learn is kept: if that leaves it unused the build fails, which the check records.
func dropUnusedImports(dir string, f *ast.File) {
	used := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		if se, ok := n.(*ast.SelectorExpr); ok {
			if id, ok := se.X.(*ast.Ident); ok {
				used[id.Name] = true
			}
		}
		return true
	})
	var paths []string
	for _, is := range f.Imports {
		if is.Name == nil {
			p, _ := strconv.Unquote(is.Path.Value)
			paths = append(paths, p)
		}
	}
	names := packageNames(dir, paths)
	var decls []ast.Decl
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.IMPORT {
			decls = append(decls, d)
			continue
		}
		var specs []ast.Spec
		for _, sp := range gd.Specs {
			is := sp.(*ast.ImportSpec)
			p, _ := strconv.Unquote(is.Path.Value)
			name := names[p]
			if is.Name != nil {
				name = is.Name.Name
			}
			if name == "" || name == "_" || name == "." || used[name] {
				specs = append(specs, sp)
			}
		}
		if len(specs) > 0 {
			gd.Specs = specs
			decls = append(decls, gd)
		}
	}
	f.Decls = decls
	var imports []*ast.ImportSpec
	for _, d := range decls {
		if gd, ok := d.(*ast.GenDecl); ok && gd.Tok == token.IMPORT {
			for _, sp := range gd.Specs {
				imports = append(imports, sp.(*ast.ImportSpec))
			}
		}
	}
	f.Imports = imports
}

// packageNames asks go list, in the clone's module, for each import path's package name.
func packageNames(dir string, paths []string) map[string]string {
	names := map[string]string{}
	if len(paths) == 0 {
		return names
	}
	cmd := exec.Command("go", append([]string{"list", "-e", "-f", "{{.ImportPath}} {{.Name}}"}, paths...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod", "CGO_ENABLED=0")
	out, _ := cmd.Output()
	for line := range strings.SplitSeq(string(out), "\n") {
		if p, n, ok := strings.Cut(line, " "); ok && n != "" {
			names[p] = n
		}
	}
	return names
}

// Why is the stage-2 reason a rejecting stub check gives.
func (s Stub) Why() string {
	switch s.Rejected {
	case RejectNoRun:
		return "stub check could not run: " + s.Err
	case RejectNoBuild:
		return fmt.Sprintf("stub check: the tree with the fix's %d functions stubbed did not build", len(s.Funcs))
	}
	return fmt.Sprintf("stub check: the named tests pass with the fix's %d functions stubbed", len(s.Funcs))
}
