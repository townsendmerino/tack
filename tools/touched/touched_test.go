package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/townsendmerino/tack/tools/gate"
)

func TestMakePlan(t *testing.T) {
	root := "/r"
	pkgs := []pkg{
		{ImportPath: "m/a", Dir: "/r/a"},
		{ImportPath: "m/b", Dir: "/r/b", Imports: []string{"m/a"}},
		{ImportPath: "m/c", Dir: "/r/c", Imports: []string{"m/b"}},
		{ImportPath: "m/t", Dir: "/r/t", TestImports: []string{"m/a"}},
		{ImportPath: "m/x", Dir: "/r/x"},
	}
	cases := []struct {
		name  string
		files []string
		build []string
		test  []string
	}{
		{"leaf change builds its importers transitively, tests direct importers incl. test-only",
			[]string{"a/a.go"}, []string{"m/a", "m/b", "m/c"}, []string{"m/a", "m/b", "m/t"}},
		{"testdata and fixtures touch their package", []string{"x/testdata/f.json"}, []string{"m/x"}, []string{"m/x"}},
		{"docs touch nothing", []string{"docs/design/DESIGN.md"}, nil, nil},
		{"go.mod touches everything", []string{"go.mod"},
			[]string{"m/a", "m/b", "m/c", "m/t", "m/x"}, []string{"m/a", "m/b", "m/c", "m/t", "m/x"}},
	}
	for _, tc := range cases {
		pl := makePlan(root, tc.files, pkgs)
		if !reflect.DeepEqual(nilIfEmpty(pl.Build), tc.build) || !reflect.DeepEqual(nilIfEmpty(pl.Test), tc.test) {
			t.Errorf("%s: build=%v test=%v, want build=%v test=%v", tc.name, pl.Build, pl.Test, tc.build, tc.test)
		}
	}
}

func nilIfEmpty(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	return s
}

// TestRunCatchesEachDefect breaks a scratch module in one way per package and checks the checker reports each one
// in the right cell: unformatted code, a vet finding, a darwin-only compile error (so linux passes and darwin fails),
// a failing test, an all-skipped package, and a change whose importer must be built and tested.
func TestRunCatchesEachDefect(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	t.Setenv("GOWORK", "off")
	t.Setenv("GOFLAGS", "-mod=mod")
	dir := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitDo := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	write("go.mod", "module m\n\ngo 1.22\n")
	write("good/good.go", "package good\n\nfunc V() int { return 1 }\n")
	write("dep/dep.go", "package dep\n\nimport \"m/good\"\n\nfunc W() int { return good.V() }\n")
	write("dep/dep_test.go", "package dep\n\nimport \"testing\"\n\nfunc TestW(t *testing.T) {\n\tif W() != 1 {\n\t\tt.Fatal(W())\n\t}\n}\n")
	write("untouched/u.go", "package untouched\n")
	gitDo("init", "-q", "-b", "main")
	gitDo("add", "go.mod", "good/good.go", "dep/dep.go", "dep/dep_test.go", "untouched/u.go")
	gitDo("commit", "-q", "-m", "base")

	// The defects, left in the worktree (modified and untracked files both count as touched).
	write("good/good.go", "package good\n\nfunc V() int { return 2 }\n") // breaks dep's test through an importer
	write("fmtbad/f.go", "package fmtbad\nfunc  F() {}\n")
	write("vetbad/v.go", "package vetbad\n\nimport \"fmt\"\n\nfunc F() string { return fmt.Sprintf(\"%d\", \"s\") }\n")
	write("darwinbad/d.go", "package darwinbad\n")
	write("darwinbad/d_darwin.go", "package darwinbad\n\nvar x int = \"not an int\"\n")
	write("skipall/s.go", "package skipall\n")
	write("skipall/s_test.go", "package skipall\n\nimport \"testing\"\n\nfunc TestS(t *testing.T) { t.Skip(\"no\") }\n")

	cells, pl, err := Run(Options{Root: dir, Timeout: "1m", Log: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]gate.Outcome{}
	for _, c := range cells {
		got[c.Name] = c.Outcome
	}
	want := map[string]gate.Outcome{
		"gofmt":          gate.Fail,
		"build/linux":    gate.OK,
		"build/darwin":   gate.Fail,
		"vet/linux":      gate.Fail,
		"vet/darwin":     gate.Fail,
		"test m/dep":     gate.Fail, // only reached because good is touched and dep imports it
		"test m/skipall": gate.Inconclusive,
	}
	for name, w := range want {
		if got[name] != w {
			t.Errorf("cell %q = %q, want %q", name, got[name], w)
		}
	}
	for _, p := range pl.Build {
		if p == "m/untouched" {
			t.Errorf("untouched package was built: %v", pl.Build)
		}
	}
	if r := gate.Reconcile(cells); r.Outcome != gate.Fail {
		t.Errorf("run outcome %s, want FAIL", r.Outcome)
	}
}

// TestCheckGoLeavesNoBinary: the build check over a lone main package writes no executable into the checked tree, as
// `go build` of one main package otherwise does.
func TestCheckGoLeavesNoBinary(t *testing.T) {
	t.Setenv("GOWORK", "off")
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module m\n\ngo 1.22\n"), 0o644)
	os.MkdirAll(filepath.Join(dir, "tools", "x"), 0o755)
	os.WriteFile(filepath.Join(dir, "tools", "x", "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644)
	for _, goos := range []string{"linux", "darwin"} {
		cells := checkGo(dir, goos, "build", []string{"m/tools/x"})
		if len(cells) != 1 || cells[0].Outcome != gate.OK {
			t.Fatalf("build/%s: %+v", goos, cells)
		}
		if entries, _ := os.ReadDir(dir); len(entries) != 2 {
			var names []string
			for _, e := range entries {
				names = append(names, e.Name())
			}
			t.Fatalf("build/%s left files in the tree: %v", goos, names)
		}
	}
}
