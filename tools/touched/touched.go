package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/townsendmerino/tack/tools/gate"
)

// goosTargets are the targets every build and vet runs for (CLAUDE.md: darwin-only code compiles only on darwin).
var goosTargets = []string{"darwin", "linux"}

// Options selects what the checker looks at.
type Options struct {
	Root    string // repository root, which is also the module root
	Base    string // git ref the diff is taken against (through its merge base); empty means origin/main, else main
	All     bool   // check every package and Go file, as CI does
	Timeout string // go test -timeout
	Log     io.Writer
}

// pkg is the slice of `go list -json` the checker uses.
type pkg struct {
	ImportPath   string
	Dir          string
	GoFiles      []string
	TestGoFiles  []string
	XTestGoFiles []string
	Imports      []string
	TestImports  []string
	XTestImports []string
}

// plan is what a run checks: the Go files to gofmt, the packages to build and vet, and the packages to test.
type plan struct {
	GoFiles []string // repo-relative
	Build   []string // import paths, built and vetted for each target that has files for them
	Test    []string // import paths
}

// Run computes the plan and runs every check. It never stops at the first failure: the report counts every check.
func Run(o Options) ([]gate.Cell, plan, error) {
	// go list reports resolved directories (/private/var on macOS), so the root must be resolved to match them.
	root, err := filepath.EvalSymlinks(o.Root)
	if err != nil {
		return nil, plan{}, err
	}
	o.Root = root
	pkgs, err := listPackages(o.Root, "")
	if err != nil {
		return nil, plan{}, err
	}
	var files []string
	if o.All {
		files = []string{"go.mod"}
		for _, p := range pkgs {
			for _, f := range append(append(append([]string{}, p.GoFiles...), p.TestGoFiles...), p.XTestGoFiles...) {
				rel, _ := filepath.Rel(o.Root, filepath.Join(p.Dir, f))
				files = append(files, rel)
			}
		}
	} else {
		files, err = changedFiles(o.Root, o.Base)
		if err != nil {
			return nil, plan{}, err
		}
	}
	pl := makePlan(o.Root, files, pkgs)
	fmt.Fprintf(o.Log, "touched: %d files; gofmt %d, build+vet %d packages, test %d packages\n",
		len(files), len(pl.GoFiles), len(pl.Build), len(pl.Test))

	var cells []gate.Cell
	cells = append(cells, checkGofmt(o.Root, pl.GoFiles))
	cells = append(cells, checkModules(o.Root, len(pl.Build) > 0)...)
	for _, goos := range goosTargets {
		cells = append(cells, checkGo(o.Root, goos, "build", pl.Build)...)
		cells = append(cells, checkGo(o.Root, goos, "vet", pl.Build)...)
	}
	cells = append(cells, checkTests(o.Root, pl.Test, o.Timeout, o.Log)...)
	return cells, pl, nil
}

// changedFiles lists files that differ between the merge base of base and HEAD and the working tree, staged or not,
// plus untracked files git does not ignore. Paths are repo-relative and may name deleted files.
func changedFiles(root, base string) ([]string, error) {
	if base == "" {
		base = "main"
		if _, err := git(root, "rev-parse", "--verify", "--quiet", "origin/main"); err == nil {
			base = "origin/main"
		}
	}
	mb, err := git(root, "merge-base", base, "HEAD")
	if err != nil {
		return nil, fmt.Errorf("merge-base %s HEAD: %w", base, err)
	}
	diff, err := git(root, "diff", "--name-only", strings.TrimSpace(mb))
	if err != nil {
		return nil, err
	}
	untracked, err := git(root, "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, l := range strings.Split(diff+"\n"+untracked, "\n") {
		if l = strings.TrimSpace(l); l != "" && !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	sort.Strings(out)
	return out, nil
}

// makePlan maps touched files to checks. A file belongs to the package in its own directory; a non-Go file there
// (a fixture, an embedded asset, testdata) touches that package too. go.mod or go.sum touches every package. Builds and
// vets cover the touched packages and everything in the module that imports them, transitively, so a changed API is
// checked at its callers. Tests cover the touched packages and their direct importers, test-only imports included.
func makePlan(root string, files []string, pkgs []pkg) plan {
	byDir := map[string]*pkg{}
	byPath := map[string]*pkg{}
	for i := range pkgs {
		rel, _ := filepath.Rel(root, pkgs[i].Dir)
		byDir[filepath.ToSlash(rel)] = &pkgs[i]
		byPath[pkgs[i].ImportPath] = &pkgs[i]
	}
	touched := map[string]bool{}
	var pl plan
	for _, f := range files {
		f = filepath.ToSlash(f)
		if f == "go.mod" || f == "go.sum" {
			for p := range byPath {
				touched[p] = true
			}
			continue
		}
		if strings.HasSuffix(f, ".go") {
			if _, err := os.Stat(filepath.Join(root, f)); err == nil {
				pl.GoFiles = append(pl.GoFiles, f)
			}
		}
		// The nearest enclosing package directory, so a file in a package's testdata/ touches the package.
		for d := filepath.ToSlash(filepath.Dir(f)); ; d = filepath.ToSlash(filepath.Dir(d)) {
			if p, ok := byDir[d]; ok {
				touched[p.ImportPath] = true
				break
			}
			if d == "." || d == "/" {
				break
			}
		}
	}

	// Reverse edges within the module.
	importers := map[string][]string{}     // code imports only
	testImporters := map[string][]string{} // code and test imports
	for _, p := range pkgs {
		for _, imp := range p.Imports {
			importers[imp] = append(importers[imp], p.ImportPath)
			testImporters[imp] = append(testImporters[imp], p.ImportPath)
		}
		for _, imp := range append(append([]string{}, p.TestImports...), p.XTestImports...) {
			testImporters[imp] = append(testImporters[imp], p.ImportPath)
		}
	}
	build := map[string]bool{}
	var walk func(string)
	walk = func(p string) {
		if build[p] {
			return
		}
		build[p] = true
		for _, q := range importers[p] {
			walk(q)
		}
	}
	test := map[string]bool{}
	for p := range touched {
		walk(p)
		test[p] = true
		for _, q := range testImporters[p] {
			test[q] = true
		}
	}
	pl.Build = sortedKeys(build)
	pl.Test = sortedKeys(test)
	sort.Strings(pl.GoFiles)
	return pl
}

func checkGofmt(root string, files []string) gate.Cell {
	c := gate.Cell{Name: "gofmt", Fields: []gate.Field{gate.F("files", len(files))}}
	if len(files) == 0 {
		c.Outcome = gate.NA
		return c
	}
	out, err := run(root, nil, "gofmt", append([]string{"-l"}, files...)...)
	switch {
	case err != nil:
		c.Outcome, c.Detail = gate.Inconclusive, out
	case strings.TrimSpace(out) != "":
		c.Outcome, c.Detail = gate.Fail, "not gofmt-clean:\n"+out
	default:
		c.Outcome = gate.OK
	}
	return c
}

// checkGo runs `go build` or `go vet` for one GOOS over the packages that have files for that GOOS. cgo is off: tack
// is one static binary (CLAUDE.md), so a build that needs cgo is a failure.
func checkGo(root, goos, verb string, pkgs []string) []gate.Cell {
	c := gate.Cell{Name: verb + "/" + goos}
	if len(pkgs) == 0 {
		c.Outcome = gate.NA
		return []gate.Cell{c}
	}
	have, err := listPackages(root, goos)
	if err != nil {
		c.Outcome, c.Detail = gate.Inconclusive, err.Error()
		return []gate.Cell{c}
	}
	ok := map[string]bool{}
	for _, p := range have {
		if len(p.GoFiles) > 0 || len(p.TestGoFiles) > 0 || len(p.XTestGoFiles) > 0 {
			ok[p.ImportPath] = true
		}
	}
	var args []string
	for _, p := range pkgs {
		if ok[p] {
			args = append(args, p)
		}
	}
	c.Fields = []gate.Field{gate.F("packages", len(args))}
	if len(args) == 0 {
		c.Outcome = gate.NA
		return []gate.Cell{c}
	}
	flags := []string{verb}
	if verb == "build" {
		flags = append(flags, "-o", os.DevNull) // a lone main package would otherwise be written into root
	}
	out, err := run(root, []string{"GOOS=" + goos, "CGO_ENABLED=0"}, "go", append(flags, args...)...)
	if err != nil {
		c.Outcome, c.Detail = gate.Fail, out
	} else {
		c.Outcome = gate.OK
	}
	return []gate.Cell{c}
}

// testEvent is one line of `go test -json`.
type testEvent struct {
	Action  string
	Package string
	Test    string
	Output  string
}

// checkTests runs the packages' tests on the host and gives one cell per package. A package whose tests all skipped
// is INCONCLUSIVE, not a pass (CLAUDE.md: a skip is not a pass). Each package's result line streams to log as it
// finishes.
func checkTests(root string, pkgs []string, timeout string, log io.Writer) []gate.Cell {
	if len(pkgs) == 0 {
		return []gate.Cell{{Name: "test", Outcome: gate.NA}}
	}
	args := []string{"test", "-json", "-timeout", timeout}
	var extra []gate.Cell
	if runtime.GOOS == "darwin" {
		if canSandbox() {
			args = append(args, "-tags", "sandboxtest") // the escape tests (internal/sandbox, internal/shell)
		} else if needsSandbox(pkgs) {
			extra = append(extra, gate.Cell{Name: "test sandbox escapes", Outcome: gate.Inconclusive,
				Detail: "sandbox-exec cannot start here (already inside a sandbox?): the escape tests did not run; run touched outside tack's sandbox"})
		}
	}
	cmd := exec.Command("go", append(args, pkgs...)...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return []gate.Cell{{Name: "test", Outcome: gate.Inconclusive, Detail: err.Error()}}
	}
	if err := cmd.Start(); err != nil {
		return []gate.Cell{{Name: "test", Outcome: gate.Inconclusive, Detail: err.Error()}}
	}
	type tally struct {
		pass, fail, skip int
		result           string // the package-level action: pass, fail or skip
		output           strings.Builder
	}
	byPkg := map[string]*tally{}
	get := func(p string) *tally {
		if byPkg[p] == nil {
			byPkg[p] = &tally{}
		}
		return byPkg[p]
	}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		var ev testEvent
		if json.Unmarshal(sc.Bytes(), &ev) != nil || ev.Package == "" {
			continue
		}
		t := get(ev.Package)
		switch {
		case ev.Action == "output":
			t.output.WriteString(ev.Output)
		case ev.Test != "" && ev.Action == "pass":
			t.pass++
		case ev.Test != "" && ev.Action == "fail":
			t.fail++
		case ev.Test != "" && ev.Action == "skip":
			t.skip++
		case ev.Test == "" && (ev.Action == "pass" || ev.Action == "fail" || ev.Action == "skip"):
			t.result = ev.Action
			fmt.Fprintf(log, "  test %s %s (pass=%d fail=%d skip=%d)\n", ev.Action, ev.Package, t.pass, t.fail, t.skip)
		}
	}
	waitErr := cmd.Wait()

	var cells []gate.Cell
	for _, p := range pkgs {
		t := byPkg[p]
		c := gate.Cell{Name: "test " + p}
		if t == nil {
			// No events at all: the package failed to build, or go test did not run.
			c.Outcome, c.Detail = gate.Fail, stderr.String()
			if waitErr == nil {
				c.Outcome = gate.Inconclusive
			}
			cells = append(cells, c)
			continue
		}
		c.Fields = []gate.Field{gate.F("pass", t.pass), gate.F("fail", t.fail), gate.F("skip", t.skip)}
		switch {
		case t.result == "fail" || t.fail > 0:
			c.Outcome, c.Detail = gate.Fail, t.output.String()+stderr.String()
		case t.result == "skip" && t.pass+t.skip == 0:
			c.Outcome = gate.NA // no test files
		case t.pass == 0 && t.skip > 0:
			c.Outcome, c.Detail = gate.Inconclusive, "every test skipped"
		case t.result == "pass" || t.result == "skip":
			c.Outcome = gate.OK
		default:
			c.Outcome, c.Detail = gate.Inconclusive, "no package result (timeout or crash?)\n"+t.output.String()
		}
		cells = append(cells, c)
	}
	return append(cells, extra...)
}

// canSandbox reports whether sandbox-exec can start a process here: it cannot inside an already sandboxed process.
func canSandbox() bool {
	return exec.Command("/usr/bin/sandbox-exec", "-p", "(version 1)(allow default)", "/usr/bin/true").Run() == nil
}

// needsSandbox reports whether pkgs include one with escape tests (build tag sandboxtest).
func needsSandbox(pkgs []string) bool {
	for _, p := range pkgs {
		if strings.HasSuffix(p, "/internal/sandbox") || strings.HasSuffix(p, "/internal/shell") {
			return true
		}
	}
	return false
}

// listPackages runs `go list -e -json ./...` with GOOS set when goos is non-empty.
func listPackages(root, goos string) ([]pkg, error) {
	env := []string{"CGO_ENABLED=0"}
	if goos != "" {
		env = append(env, "GOOS="+goos)
	}
	cmd := exec.Command("go", "list", "-e", "-json", "./...")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), env...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list: %v: %s", err, stderr.String())
	}
	var pkgs []pkg
	dec := json.NewDecoder(bytes.NewReader(out))
	for dec.More() {
		var p pkg
		if err := dec.Decode(&p); err != nil {
			return nil, fmt.Errorf("go list output: %w", err)
		}
		pkgs = append(pkgs, p)
	}
	return pkgs, nil
}

func git(root string, args ...string) (string, error) {
	return run(root, nil, "git", args...)
}

// run executes a command in dir with extra environment and returns its combined output.
func run(dir string, env []string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// checkModules resolves every package and its test dependencies with no workspace and a read-only go.mod, as CI
// does. A local go.work keeps its own sums in go.work.sum, so a build that passes here can still fail in CI on a
// go.sum that lacks a dependency's transitive modules; this cell catches that by day.
func checkModules(root string, touched bool) []gate.Cell {
	var cells []gate.Cell
	for _, goos := range goosTargets {
		c := gate.Cell{Name: "modules/" + goos}
		if !touched {
			c.Outcome = gate.NA
			cells = append(cells, c)
			continue
		}
		out, err := run(root, []string{"GOWORK=off", "GOFLAGS=-mod=readonly", "CGO_ENABLED=0", "GOOS=" + goos},
			"go", "list", "-deps", "-test", "./...")
		if err != nil {
			c.Outcome, c.Detail = gate.Fail, "with no go.work, as CI builds:\n"+lastLines(out, 12)+"\nfix: GOWORK=off go mod tidy"
		} else {
			c.Outcome = gate.OK
		}
		cells = append(cells, c)
	}
	return cells
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
