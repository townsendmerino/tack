package main

import (
	"fmt"
	"go/parser"
	"go/token"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/townsendmerino/tack/tools/gate"
)

// allow is one row of an import table. Paths are relative to the module.
type allow struct {
	Imports []string // the module's packages this package's code may import
	Tests   []string // the further ones only its tests may import: a test edge decides what touched tests (makePlan)
}

// importRules is a module's import table and the rules held on the graph whatever the table says, so that adding a
// row or an edge cannot waive a rule.
type importRules struct {
	Module      string
	Layers      map[string]allow    // a row for every package in the module, and for nothing else
	Sealed      []string            // packages whose code imports no other package of the module
	Clients     []string            // packages that must not reach DaemonOnly, directly or through other packages
	DaemonOnly  []string            // matched with their subpackages
	OnlyImport  map[string][]string // module -> the only packages of it this module's packages may import directly
	NeverLinked []string            // modules none of whose packages is linked into a package or imported by its tests
}

// tackRules is tack's import table and the product rules (CLAUDE.md, "Rules the product never breaks") that are
// statements about the graph. The table holds today's graph exactly, never an aspiration: an edge nothing uses fails
// the imports/table cell, so a row is edited when an import is added or removed.
var tackRules = importRules{
	Module: "github.com/townsendmerino/tack",
	Layers: tackLayers,
	// internal/proto is the source of truth for the protocol.
	Sealed: []string{"internal/proto"},
	// Thin clients: the daemon owns file access, clients render.
	Clients:    []string{"internal/acp", "internal/render", "internal/tui"},
	DaemonOnly: []string{"internal/sandbox", "internal/shell", "internal/store", "internal/tools"},
	// D6: tack uses ken in-process through its public engine package (DESIGN §12, K1).
	OnlyImport: map[string][]string{path.Dir(kenEngine): {kenEngine}},
	// goinfer runs as its own process, never linked in. DESIGN §13 reaches it only over HTTP and its admin socket and
	// names no goinfer package; internal/server/goinfer is tack's own client.
	NeverLinked: []string{"github.com/townsendmerino/goinfer"},
}

// kenEngine is the one ken package tack imports. ken's module path is derived from it, not spelled out: internal/kensearch's
// TestOnlyEnginesPublicSurface reads every source file's text and takes any other quoted ken path for an import.
const kenEngine = "github.com/townsendmerino/ken/engine"

var tackLayers = map[string]allow{
	"cmd/tack": {
		Imports: []string{"internal/config", "internal/core", "internal/daemon", "internal/eval",
			"internal/eval/mine", "internal/eval/pilot", "internal/halt", "internal/kensearch", "internal/loop",
			"internal/models", "internal/perms", "internal/proto", "internal/sandbox", "internal/serve",
			"internal/server", "internal/server/cassette", "internal/server/goinfer", "internal/shell",
			"internal/snapshot", "internal/store", "internal/tools", "internal/tui", "internal/usage",
			"internal/window", "tools/gate"},
		Tests: []string{"internal/kenrun", "internal/server/goinfer/goinfertest"},
	},
	"internal/acp":    {},
	"internal/config": {},
	"internal/core": {
		Imports: []string{"internal/halt", "internal/loop", "internal/models", "internal/perms", "internal/proto",
			"internal/sandbox", "internal/server", "internal/snapshot", "internal/store", "internal/tools",
			"internal/usage", "internal/window"},
		Tests: []string{"internal/eval/pilot", "internal/server/goinfer/goinfertest", "internal/shell"},
	},
	"internal/daemon": {
		Imports: []string{"internal/config", "internal/core", "internal/proc", "internal/proto"},
		Tests:   []string{"internal/loop", "internal/models", "internal/server/goinfer/goinfertest", "internal/store"},
	},
	"internal/eval": {
		Imports: []string{"internal/models", "internal/proto", "internal/server", "tools/gate"},
		Tests:   []string{"internal/server/goinfer", "internal/server/goinfer/goinfertest"},
	},
	"internal/eval/mine": {},
	"internal/eval/pilot": {
		Imports: []string{"internal/eval/mine", "internal/kensearch", "internal/loop", "internal/proto",
			"internal/tools"},
		Tests: []string{"internal/config"},
	},
	"internal/halt": {
		Imports: []string{"internal/proto", "internal/sandbox"},
	},
	"internal/kenrun": {},
	"internal/kensearch": {
		Imports: []string{"internal/tools"},
		Tests:   []string{"internal/window"},
	},
	"internal/loop": {
		Imports: []string{"internal/halt", "internal/models", "internal/perms", "internal/proto", "internal/server",
			"internal/shell", "internal/store", "internal/tools"},
		Tests: []string{"internal/sandbox", "internal/server/cassette", "internal/server/goinfer",
			"internal/server/goinfer/goinfertest", "internal/snapshot"},
	},
	"internal/models": {
		Imports: []string{"internal/config", "internal/proc", "internal/server", "internal/server/goinfer",
			"internal/server/openai"},
		Tests: []string{"internal/server/goinfer/goinfertest"},
	},
	"internal/perms": {
		Imports: []string{"internal/proto"},
	},
	"internal/proc": {},
	"internal/proto": {
		Tests: []string{"proto/fixtures"},
	},
	"internal/proto/swiftgen": {
		Imports: []string{"internal/proto"},
	},
	"internal/render":  {},
	"internal/sandbox": {},
	"internal/serve": {
		Imports: []string{"internal/models"},
		Tests:   []string{"internal/eval", "internal/server/goinfer"},
	},
	"internal/server": {
		Imports: []string{"internal/proto"},
	},
	"internal/server/cassette": {},
	"internal/server/goinfer": {
		Imports: []string{"internal/proto", "internal/server"},
		Tests:   []string{"internal/server/cassette", "internal/server/goinfer/goinfertest"},
	},
	"internal/server/goinfer/goinfertest": {
		Imports: []string{"internal/proto", "internal/server"},
	},
	"internal/server/openai": {
		Imports: []string{"internal/proto", "internal/server"},
		Tests:   []string{"internal/loop", "internal/store", "internal/tools"},
	},
	"internal/shell": {
		Imports: []string{"internal/perms", "internal/sandbox"},
	},
	"internal/snapshot": {},
	"internal/store": {
		Imports: []string{"internal/proto"},
	},
	"internal/tools": {
		Imports: []string{"internal/perms", "internal/server", "internal/shell"},
	},
	"internal/tui": {
		Imports: []string{"internal/proto"},
		Tests:   []string{"proto/fixtures"},
	},
	"internal/usage": {
		Imports: []string{"internal/proto", "internal/store"},
	},
	"internal/window": {
		Imports: []string{"internal/proto", "internal/tools"},
	},
	"proto/fixtures": {},
	"tools/calreport": {
		Imports: []string{"internal/eval/pilot", "tools/gate"},
	},
	"tools/cassettes": {
		Imports: []string{"internal/models", "internal/server/cassette", "internal/server/goinfer",
			"internal/server/goinfer/goinfertest"},
	},
	"tools/gate": {},
	"tools/injectceiling": {
		Imports: []string{"internal/eval/pilot", "internal/kensearch", "internal/window", "tools/gate"},
	},
	"tools/lint": {
		Imports: []string{"tools/gate"},
	},
	"tools/lint/canary": {},
	"tools/protogen": {
		Imports: []string{"internal/proto/swiftgen"},
	},
	"tools/recordrun": {
		Imports: []string{"internal/kenrun", "internal/models"},
	},
	"tools/replaycontext": {
		Imports: []string{"internal/eval/mine", "internal/eval/pilot", "tools/gate"},
		Tests:   []string{"internal/proto"},
	},
	"tools/replayparse": {
		Imports: []string{"internal/eval/pilot", "internal/proto", "internal/tools"},
	},
	"tools/replayrepeats": {
		Imports: []string{"internal/eval/pilot", "tools/gate"},
	},
	"tools/replaytolerant": {
		Imports: []string{"internal/eval/pilot", "internal/loop", "internal/proto", "internal/tools", "tools/gate"},
	},
	"tools/repomapgate": {
		Imports: []string{"internal/core", "internal/eval/pilot", "internal/kensearch", "internal/window",
			"tools/gate"},
	},
	"tools/searchfilescheck": {
		Imports: []string{"internal/kensearch", "internal/tools", "internal/window", "tools/gate"},
	},
	"tools/searchfileslat": {
		Imports: []string{"internal/kensearch", "tools/gate"},
	},
	"tools/summarycheck": {
		Imports: []string{"internal/loop", "internal/proto", "internal/server", "internal/server/goinfer",
			"tools/gate"},
		Tests: []string{"internal/server/goinfer/goinfertest"},
	},
	"tools/synctime": {},
	"tools/touched": {
		Imports: []string{"tools/gate"},
	},
}

// importConfig is one build configuration the graph is read under, always with cgo off.
type importConfig struct{ Name, GOOS, Tags string }

// importConfigs are the configurations whose graphs are checked: build constraints change the graph, and sandboxtest
// is the tag touched's tests run with on darwin.
var importConfigs = []importConfig{
	{"darwin", "darwin", ""},
	{"linux", "linux", ""},
	{"darwin+sandboxtest", "darwin", "sandboxtest"},
}

// checkImports reads the module's graph under each of importConfigs and checks it against rules: one cell per
// configuration, then imports/table for what the rules name that no configuration has. It is n/a when touched is
// false: only a Go file or go.mod changes the graph.
func checkImports(root string, rules *importRules, touched bool) []gate.Cell {
	var cells []gate.Cell
	if !touched {
		for _, c := range importConfigs {
			cells = append(cells, gate.Cell{Name: "imports/" + c.Name, Outcome: gate.NA})
		}
		return append(cells, gate.Cell{Name: "imports/table", Outcome: gate.NA})
	}
	graphs := make([][]pkg, len(importConfigs))
	errs := make([]error, len(importConfigs))
	var wg sync.WaitGroup
	for i, c := range importConfigs {
		wg.Go(func() { graphs[i], errs[i] = listDeps(root, c) })
	}
	wg.Wait()

	complete := true
	for i, c := range importConfigs {
		cell := gate.Cell{Name: "imports/" + c.Name}
		if errs[i] != nil {
			cell.Outcome, cell.Detail = gate.Inconclusive, errs[i].Error()
			complete = false
			cells = append(cells, cell)
			continue
		}
		bad, broken := rules.violations(graphs[i])
		cell.Fields = []gate.Field{gate.F("packages", rules.count(graphs[i]))}
		switch {
		case len(bad) > 0:
			cell.Outcome = gate.Fail
			cell.Detail = strings.Join(bad, "\n") + "\nan intended new edge goes in its row in tools/touched/imports.go:tackLayers;" +
				" the rules in tackRules hold whatever the table says"
			if len(broken) > 0 {
				cell.Detail += "\nand the graph is incomplete:\n" + strings.Join(broken, "\n")
			}
		case len(broken) > 0:
			cell.Outcome = gate.Inconclusive
			cell.Detail = "the graph is incomplete:\n" + strings.Join(broken, "\n")
			complete = false
		default:
			cell.Outcome = gate.OK
		}
		cells = append(cells, cell)
	}

	table := gate.Cell{Name: "imports/table", Fields: []gate.Field{gate.F("rows", len(rules.Layers))}}
	switch stale := rules.stale(graphs); {
	case !complete:
		table.Outcome, table.Detail = gate.Inconclusive, "not judged: a configuration's graph is incomplete"
	case len(stale) > 0:
		table.Outcome = gate.Fail
		table.Detail = strings.Join(stale, "\n") + "\nthe table holds today's graph: edit tools/touched/imports.go:tackLayers"
	default:
		table.Outcome = gate.OK
	}
	return append(cells, table)
}

// listDeps runs `go list -e -deps -json ./...` under c with cgo off: the module's packages and everything they link.
func listDeps(root string, c importConfig) ([]pkg, error) {
	args := []string{"list", "-e", "-deps", "-json"}
	if c.Tags != "" {
		args = append(args, "-tags", c.Tags)
	}
	return goList(root, []string{"CGO_ENABLED=0", "GOOS=" + c.GOOS}, append(args, "./...")...)
}

// rel returns path relative to the module, and whether path is in it.
func (r *importRules) rel(path string) (string, bool) {
	return strings.CutPrefix(path, r.Module+"/")
}

// count is the number of the module's packages in a graph.
func (r *importRules) count(graph []pkg) int {
	n := 0
	for _, p := range graph {
		if _, ok := r.rel(p.ImportPath); ok {
			n++
		}
	}
	return n
}

// within reports whether path is base or a package under it.
func within(path, base string) bool {
	return path == base || strings.HasPrefix(path, base+"/")
}

// violations checks one configuration's graph against the table and the rules. bad has one line per offending edge;
// broken lists what makes the graph incomplete (a package go list could not load, a file that does not parse).
func (r *importRules) violations(graph []pkg) (bad, broken []string) {
	byPath := map[string]*pkg{}
	for i := range graph {
		byPath[graph[i].ImportPath] = &graph[i]
	}
	for i := range graph {
		p := &graph[i]
		if p.Error != nil && !onlyExcluded(p) {
			broken = append(broken, p.ImportPath+": "+p.Error.Err)
		}
		name, ok := r.rel(p.ImportPath)
		if !ok {
			continue
		}
		cb, cbr := cgoFiles(p, name)
		bad, broken = append(bad, cb...), append(broken, cbr...)

		tests := slices.Concat(p.TestImports, p.XTestImports)
		row, listed := r.Layers[name]
		if !listed {
			var mine []string
			for _, imp := range p.Imports {
				if q, ok := r.rel(imp); ok {
					mine = append(mine, q)
				}
			}
			bad = append(bad, fmt.Sprintf("%s has no row in the table (its code imports %v)", name, mine))
		} else {
			for _, imp := range p.Imports {
				if q, ok := r.rel(imp); ok && !slices.Contains(row.Imports, q) {
					bad = append(bad, fmt.Sprintf("%s imports %s, which its row does not allow", name, q))
				}
			}
			for _, imp := range tests {
				if q, ok := r.rel(imp); ok && q != name && !slices.Contains(row.Imports, q) && !slices.Contains(row.Tests, q) {
					bad = append(bad, fmt.Sprintf("%s's tests import %s, which its row does not allow", name, q))
				}
			}
		}

		for _, imp := range slices.Concat(p.Imports, tests) {
			for mod, only := range r.OnlyImport {
				if within(imp, mod) && !slices.Contains(only, imp) {
					bad = append(bad, fmt.Sprintf("%s imports %s: of %s, only %v may be imported", name, imp, mod, only))
				}
			}
		}
		if slices.Contains(r.Sealed, name) {
			for _, imp := range p.Imports {
				if q, ok := r.rel(imp); ok {
					bad = append(bad, fmt.Sprintf("%s imports %s: it is sealed, and imports no other package of the module", name, q))
				}
			}
		}
		if slices.Contains(r.Clients, name) {
			for _, d := range r.DaemonOnly {
				target := r.Module + "/" + d
				if chain := shortestPath(byPath, p.ImportPath, func(x string) bool { return within(x, target) }); chain != nil {
					bad = append(bad, fmt.Sprintf("client %s reaches %s, which only the daemon may use: %s", name, d, r.short(chain)))
				}
			}
		}
		// A never-linked module is reported where it enters the module's graph: at the package whose own import is
		// that module or leads to it outside the module. A test-only import outside the module is not in a -deps
		// listing, so only the import itself is checked for it, not what it imports.
		for _, mod := range r.NeverLinked {
			for _, imp := range slices.Concat(p.Imports, tests) {
				if _, inside := r.rel(imp); inside {
					continue
				}
				if chain := shortestPath(byPath, imp, func(x string) bool { return within(x, mod) }); chain != nil {
					bad = append(bad, fmt.Sprintf("%s links %s, from a module never linked in: %s", name, chain[len(chain)-1],
						r.short(append([]string{p.ImportPath}, chain...))))
				}
			}
		}
	}
	slices.Sort(bad)
	slices.Sort(broken)
	return slices.Compact(bad), slices.Compact(broken)
}

// onlyExcluded reports whether a package's error is only that this configuration's build constraints leave none of
// its files: the state of a darwin-only package on linux, not a broken graph.
func onlyExcluded(p *pkg) bool {
	return len(p.GoFiles)+len(p.CgoFiles)+len(p.TestGoFiles)+len(p.XTestGoFiles) == 0 && len(p.IgnoredGoFiles) > 0
}

// cgoFiles lists p's files that import "C". It reads every file go list names for p, including those this
// configuration's constraints leave out: with cgo off, a file that imports "C" is dropped from the build silently, so
// the build cells cannot see it.
func cgoFiles(p *pkg, name string) (bad, broken []string) {
	for _, f := range slices.Concat(p.GoFiles, p.CgoFiles, p.TestGoFiles, p.XTestGoFiles, p.IgnoredGoFiles) {
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(p.Dir, f), nil, parser.ImportsOnly)
		if err != nil {
			broken = append(broken, err.Error())
			continue
		}
		for _, s := range file.Imports {
			if s.Path.Value == `"C"` {
				bad = append(bad, fmt.Sprintf("%s/%s imports \"C\": the daemon is one static binary, with no cgo", name, f))
			}
		}
	}
	return bad, broken
}

// shortestPath returns the shortest chain of imports from start to a package matching target, start included, or nil
// if none reaches one. Packages missing from byPath are leaves.
func shortestPath(byPath map[string]*pkg, start string, target func(string) bool) []string {
	prev := map[string]string{start: ""}
	queue := []string{start}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if target(cur) {
			var chain []string
			for x := cur; x != ""; x = prev[x] {
				chain = append(chain, x)
			}
			slices.Reverse(chain)
			return chain
		}
		if p := byPath[cur]; p != nil {
			for _, next := range p.Imports {
				if _, seen := prev[next]; !seen {
					prev[next] = cur
					queue = append(queue, next)
				}
			}
		}
	}
	return nil
}

// short joins a chain of import paths with the module's own written relative to it.
func (r *importRules) short(chain []string) string {
	out := make([]string, len(chain))
	for i, x := range chain {
		if q, ok := r.rel(x); ok {
			x = q
		}
		out[i] = x
	}
	return strings.Join(out, " -> ")
}

// stale lists what the rules name that no configuration's graph has: a row for a package that is gone, an edge a row
// allows that nothing uses, an edge listed under both Imports and Tests, and a package named by a rule that is gone.
func (r *importRules) stale(graphs [][]pkg) []string {
	have := map[string]bool{}
	code := map[string]bool{}  // "pkg imp" edges used by code
	tests := map[string]bool{} // "pkg imp" edges used by tests
	for _, g := range graphs {
		for _, p := range g {
			name, ok := r.rel(p.ImportPath)
			if !ok {
				continue
			}
			have[name] = true
			for _, imp := range p.Imports {
				if q, ok := r.rel(imp); ok {
					code[name+" "+q] = true
				}
			}
			for _, imp := range slices.Concat(p.TestImports, p.XTestImports) {
				if q, ok := r.rel(imp); ok {
					tests[name+" "+q] = true
				}
			}
		}
	}
	var out []string
	for name, row := range r.Layers {
		if !have[name] {
			out = append(out, fmt.Sprintf("the row for %s names no package", name))
			continue
		}
		for _, q := range row.Imports {
			if !code[name+" "+q] {
				out = append(out, fmt.Sprintf("%s's row allows %s, which its code does not import", name, q))
			}
		}
		for _, q := range row.Tests {
			switch {
			case slices.Contains(row.Imports, q):
				out = append(out, fmt.Sprintf("%s's row lists %s under both Imports and Tests", name, q))
			case !tests[name+" "+q]:
				out = append(out, fmt.Sprintf("%s's row allows its tests %s, which they do not import", name, q))
			}
		}
	}
	for _, name := range slices.Concat(r.Sealed, r.Clients, r.DaemonOnly) {
		if !have[name] {
			out = append(out, fmt.Sprintf("a rule names %s, which is not a package (renamed? rename it in tackRules too)", name))
		}
	}
	slices.Sort(out)
	return out
}
