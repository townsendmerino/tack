package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/townsendmerino/tack/tools/gate"
)

// testRules is a small module's table and rules in the shape of tackRules: proto sealed, tui a client that must not
// reach store, only x/ken/engine of ken, nothing of x/goinfer.
func testRules() *importRules {
	return &importRules{
		Module: "m",
		Layers: map[string]allow{
			"app":      {Imports: []string{"search", "store", "tui"}},
			"fixtures": {},
			"proto":    {},
			"search":   {Imports: []string{"proto"}},
			"store":    {Imports: []string{"proto"}},
			"tui":      {Imports: []string{"proto"}, Tests: []string{"fixtures"}},
		},
		Sealed:      []string{"proto"},
		Clients:     []string{"tui"},
		DaemonOnly:  []string{"store"},
		OnlyImport:  map[string][]string{"x/ken": {"x/ken/engine"}},
		NeverLinked: []string{"x/goinfer"},
	}
}

// testGraph is a graph that keeps every rule of testRules, as `go list -deps` would give it.
func testGraph() []pkg {
	return []pkg{
		{ImportPath: "m/app", Imports: []string{"m/search", "m/store", "m/tui"}},
		{ImportPath: "m/fixtures"},
		{ImportPath: "m/proto", Imports: []string{"fmt"}},
		{ImportPath: "m/search", Imports: []string{"m/proto", "x/ken/engine"}},
		{ImportPath: "m/store", Imports: []string{"m/proto"}},
		{ImportPath: "m/tui", Imports: []string{"m/proto"}, TestImports: []string{"m/fixtures", "testing"},
			XTestImports: []string{"m/tui"}},
		{ImportPath: "x/ken/engine", Imports: []string{"x/ken/internal/search"}},
		{ImportPath: "x/ken/internal/search", Imports: []string{"fmt"}},
		{ImportPath: "x/goinference"}, // shares x/goinfer's prefix, not its module
		{ImportPath: "fmt"},
		{ImportPath: "testing"},
	}
}

func find(g []pkg, path string) *pkg {
	for i := range g {
		if g[i].ImportPath == path {
			return &g[i]
		}
	}
	panic(path)
}

// TestImportViolationsCatchEachBreak breaks the graph (or the table) one way per case and checks the break is
// reported naming its edge, and that nothing else is. The unbroken graph reports nothing.
func TestImportViolationsCatchEachBreak(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "c.go"), []byte("package proto\n\n// int f();\nimport \"C\"\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "x_darwin.go"), []byte("package proto\n"), 0o644)

	cases := []struct {
		name       string
		break_     func(g []pkg, r *importRules)
		bad, broke []string
	}{
		{"unbroken", func([]pkg, *importRules) {}, nil, nil},
		{"code edge its row does not allow", func(g []pkg, r *importRules) {
			find(g, "m/store").Imports = append(find(g, "m/store").Imports, "m/fixtures")
		}, []string{"store imports fixtures, which its row does not allow"}, nil},
		{"test edge its row does not allow", func(g []pkg, r *importRules) {
			find(g, "m/store").XTestImports = []string{"m/store", "m/tui"}
		}, []string{"store's tests import tui, which its row does not allow"}, nil},
		{"a row's Tests do not let code import", func(g []pkg, r *importRules) {
			find(g, "m/tui").Imports = append(find(g, "m/tui").Imports, "m/fixtures")
		}, []string{"tui imports fixtures, which its row does not allow"}, nil},
		{"package with no row", func(g []pkg, r *importRules) {
			delete(r.Layers, "app")
		}, []string{"app has no row in the table (its code imports [search store tui])"}, nil},
		{"sealed package imports the module, even with the edge in its row", func(g []pkg, r *importRules) {
			find(g, "m/proto").Imports = []string{"fmt", "m/fixtures"}
			r.Layers["proto"] = allow{Imports: []string{"fixtures"}}
		}, []string{"proto imports fixtures: it is sealed, and imports no other package of the module"}, nil},
		{"client reaches a daemon-only package through another", func(g []pkg, r *importRules) {
			find(g, "m/search").Imports = append(find(g, "m/search").Imports, "m/store")
			find(g, "m/tui").Imports = append(find(g, "m/tui").Imports, "m/search")
			r.Layers["search"] = allow{Imports: []string{"proto", "store"}}
			r.Layers["tui"] = allow{Imports: []string{"proto", "search"}, Tests: []string{"fixtures"}}
		}, []string{"client tui reaches store, which only the daemon may use: tui -> search -> store"}, nil},
		{"ken internal package", func(g []pkg, r *importRules) {
			find(g, "m/search").Imports = append(find(g, "m/search").Imports, "x/ken/internal/search")
		}, []string{"search imports x/ken/internal/search: of x/ken, only [x/ken/engine] may be imported"}, nil},
		{"ken public package other than engine, from a test", func(g []pkg, r *importRules) {
			find(g, "m/tui").TestImports = append(find(g, "m/tui").TestImports, "x/ken/mcp")
		}, []string{"tui imports x/ken/mcp: of x/ken, only [x/ken/engine] may be imported"}, nil},
		{"goinfer linked through a dependency, reported where it enters", func(g []pkg, r *importRules) {
			find(g, "x/ken/internal/search").Imports = []string{"fmt", "x/goinfer/serve"}
		}, []string{"search links x/goinfer/serve, from a module never linked in: " +
			"search -> x/ken/engine -> x/ken/internal/search -> x/goinfer/serve"}, nil},
		{"goinfer imported by a test", func(g []pkg, r *importRules) {
			find(g, "m/tui").XTestImports = append(find(g, "m/tui").XTestImports, "x/goinfer/client")
		}, []string{"tui links x/goinfer/client, from a module never linked in: tui -> x/goinfer/client"}, nil},
		{"a module sharing goinfer's prefix is not goinfer", func(g []pkg, r *importRules) {
			find(g, "m/store").Imports = append(find(g, "m/store").Imports, "x/goinference")
		}, nil, nil},
		{"a file that imports C, though the build leaves it out", func(g []pkg, r *importRules) {
			p := find(g, "m/proto")
			p.Dir, p.IgnoredGoFiles = dir, []string{"c.go"}
		}, []string{`proto/c.go imports "C": the daemon is one static binary, with no cgo`}, nil},
		{"a package go list could not load makes the graph incomplete", func(g []pkg, r *importRules) {
			p := find(g, "m/store")
			p.Error = &struct{ Err string }{"no required module provides package q"}
		}, nil, []string{"m/store: no required module provides package q"}},
		{"a package this target's constraints leave empty is not broken", func(g []pkg, r *importRules) {
			p := find(g, "m/proto")
			p.Dir, p.IgnoredGoFiles = dir, []string{"x_darwin.go"}
			p.Error = &struct{ Err string }{"build constraints exclude all Go files in " + dir}
		}, nil, nil},
	}
	for _, tc := range cases {
		g, r := testGraph(), testRules()
		tc.break_(g, r)
		bad, broke := r.violations(g)
		if !reflect.DeepEqual(nilIfEmpty(bad), tc.bad) || !reflect.DeepEqual(nilIfEmpty(broke), tc.broke) {
			t.Errorf("%s:\n bad   %q\n want  %q\n broke %q\n want  %q", tc.name, bad, tc.bad, broke, tc.broke)
		}
	}
}

// TestImportTableStale: the table holds the graph exactly, across every configuration. The unbroken table reports
// nothing; each case leaves one entry that no configuration uses.
func TestImportTableStale(t *testing.T) {
	cases := []struct {
		name   string
		break_ func(r *importRules)
		want   []string
	}{
		{"unbroken", func(*importRules) {}, nil},
		{"row for a package that is gone", func(r *importRules) { r.Layers["gone"] = allow{} },
			[]string{"the row for gone names no package"}},
		{"edge nothing imports", func(r *importRules) { r.Layers["store"] = allow{Imports: []string{"proto", "fixtures"}} },
			[]string{"store's row allows fixtures, which its code does not import"}},
		{"test edge no test imports", func(r *importRules) { r.Layers["store"] = allow{Imports: []string{"proto"}, Tests: []string{"tui"}} },
			[]string{"store's row allows its tests tui, which they do not import"}},
		{"edge under both lists", func(r *importRules) {
			r.Layers["tui"] = allow{Imports: []string{"proto"}, Tests: []string{"fixtures", "proto"}}
		}, []string{"tui's row lists proto under both Imports and Tests"}},
		{"rule naming a package that is gone", func(r *importRules) { r.Clients = []string{"acp", "tui"} },
			[]string{"a rule names acp, which is not a package (renamed? rename it in tackRules too)"}},
	}
	for _, tc := range cases {
		r := testRules()
		tc.break_(r)
		if got := r.stale([][]pkg{testGraph()}); !reflect.DeepEqual(nilIfEmpty(got), tc.want) {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}

	// An edge one configuration has is not stale for lack of it in another.
	r := testRules()
	r.Layers["store"] = allow{Imports: []string{"fixtures", "proto"}}
	linux := testGraph()
	find(linux, "m/store").Imports = []string{"m/fixtures", "m/proto"}
	if got := r.stale([][]pkg{testGraph(), linux}); got != nil {
		t.Errorf("edge on one target only: %q, want nothing", got)
	}
}

// TestCheckImportsNotTouched: with no package touched the cells are n/a, so a docs-only change stays INCONCLUSIVE
// (CLAUDE.md: run -all then).
func TestCheckImportsNotTouched(t *testing.T) {
	cells := checkImports("/nonexistent", &tackRules, false)
	if len(cells) != len(importConfigs)+1 {
		t.Fatalf("%d cells, want %d", len(cells), len(importConfigs)+1)
	}
	for _, c := range cells {
		if c.Outcome != gate.NA {
			t.Errorf("%s: %s, want n/a", c.Name, c.Outcome)
		}
	}
	if r := gate.Reconcile(cells); r.Outcome != gate.Inconclusive {
		t.Errorf("outcome %s, want INCONCLUSIVE", r.Outcome)
	}
}
