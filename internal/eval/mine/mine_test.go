package mine

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// scratchRepo builds a repository whose commits each exercise one stage-1 rule, and returns it with the subjects of
// the commits that must become candidates.
func scratchRepo(t *testing.T) Repo {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(dir, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitDo := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	commit := func(msg string) {
		gitDo("add", "-A")
		gitDo("commit", "-q", "-m", msg)
	}
	gitDo("init", "-q", "-b", "main")
	write("go.mod", "module m\n\ngo 1.22\n")
	write("calc/calc.go", "package calc\n\nfunc Add(a, b int) int { return a - b }\n\nfunc Mul(a, b int) int { return a * b }\n")
	commit("base")

	// A good task: a bug fix with the test that catches it.
	write("calc/calc.go", "package calc\n\nfunc Add(a, b int) int { return a + b }\n\nfunc Mul(a, b int) int { return a * b }\n")
	write("calc/calc_test.go", "package calc\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(2, 2) != 4 {\n\t\tt.Fatal(Add(2, 2))\n\t}\n}\n")
	commit("fix Add")

	// A new function and its test: compile-fails on the parent, which counts as failing.
	write("calc/sub.go", "package calc\n\nfunc Sub(a, b int) int { return a - b }\n")
	write("calc/sub_test.go", "package calc\n\nimport \"testing\"\n\nfunc TestSub(t *testing.T) {\n\tif Sub(5, 3) != 2 {\n\t\tt.Fatal(Sub(5, 3))\n\t}\n}\n")
	commit("add Sub")

	// A refactor whose new test already passes on the parent: static candidate, rejected by verification.
	write("calc/calc.go", "package calc\n\n// Add adds.\nfunc Add(a, b int) int { return a + b }\n\nfunc Mul(a, b int) int { return a * b }\n")
	write("calc/mul_test.go", "package calc\n\nimport \"testing\"\n\nfunc TestMul(t *testing.T) {\n\tif Mul(2, 3) != 6 {\n\t\tt.Fatal(Mul(2, 3))\n\t}\n}\n")
	commit("document Add")

	write("README.md", "docs\n")
	commit("docs only") // rule 2: no non-test Go

	write("calc/calc_test.go", "package calc\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 1) != 2 {\n\t\tt.Fatal(Add(1, 1))\n\t}\n}\n")
	commit("test only") // rule 2: no non-test Go

	var big strings.Builder
	big.WriteString("package calc\n\n")
	for i := 0; i < 90; i++ {
		fmt.Fprintf(&big, "func F%d() int { return %d }\n", i, i)
	}
	write("calc/big.go", big.String())
	write("calc/big_test.go", "package calc\n\nimport \"testing\"\n\nfunc TestF1(t *testing.T) {\n\tif F1() != 1 {\n\t\tt.Fatal()\n\t}\n}\n")
	commit("too big") // rule 3

	write("calc/tagged.go", "//go:build linux\n\npackage calc\n\nfunc T() int { return 1 }\n")
	write("calc/tagged_test.go", "//go:build linux\n\npackage calc\n\nimport \"testing\"\n\nfunc TestT(t *testing.T) {\n\tif T() != 1 {\n\t\tt.Fatal()\n\t}\n}\n")
	commit("tagged") // rule 6

	write("calc/msg.go", "package calc\n\nfunc Msg() string { return \"the quick brown fox jumps over\" }\n")
	write("calc/msg_test.go", "package calc\n\nimport \"testing\"\n\nfunc TestMsg(t *testing.T) {\n\tif Msg() != \"the quick brown fox jumps over\" {\n\t\tt.Fatal(Msg())\n\t}\n}\n")
	commit("literal leak") // rule 8: the asserted 30-character string is in the fix

	write("calc/hi.go", "package calc\n\nfunc Hi() string { return \"hello\" }\n")
	write("calc/hi_test.go", "package calc\n\nimport \"testing\"\n\nfunc TestHi(t *testing.T) {\n\tif Hi() != \"hello\" {\n\t\tt.Fatal(Hi())\n\t}\n}\n")
	commit("short literal") // rule 8 keeps it: under 20 characters

	write("sub/go.mod", "module m/sub\n\ngo 1.22\n")
	write("sub/x.go", "package sub\n\nfunc X() int { return 1 }\n")
	write("sub/x_test.go", "package sub\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) {}\n")
	commit("submodule") // outside the root module

	return Repo{Name: "scratch", Path: dir, Frozen: gitDo("rev-parse", "HEAD")}
}

func TestStaticAndVerify(t *testing.T) {
	t.Setenv("GOFLAGS", "")
	r := scratchRepo(t)
	cs, st, err := Static(r)
	if err != nil {
		t.Fatal(err)
	}
	if st.Read != 11 || st.Rule8 != 1 {
		t.Fatalf("read %d commits, rule 8 removed %d; want 11 and 1", st.Read, st.Rule8)
	}
	var got []string
	for _, c := range cs {
		got = append(got, c.Subject+":"+strings.Join(c.Tests, ","))
	}
	want := []string{"fix Add:TestAdd", "add Sub:TestSub", "document Add:TestMul", "short literal:TestHi"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("candidates %v, want %v", got, want)
	}
	if cs[0].Packages[0] != "./calc" || cs[0].Why.NonTestLines != 2 || cs[0].Why.NonTestFiles != 1 {
		t.Fatalf("measure %+v packages %v", cs[0].Why, cs[0].Packages)
	}

	ctx := context.Background()
	results := map[string]Verified{}
	work := filepath.Join(t.TempDir(), "clone")
	if err := PrepareClone(r, work); err != nil {
		t.Fatal(err)
	}
	for _, c := range cs {
		results[c.Subject] = Verify(ctx, r, c, work)
	}
	if v := results["fix Add"]; !v.OK || v.OnParent["TestAdd"] != "fail" || v.OnCommit[1]["TestAdd"] != "pass" ||
		v.Stub == nil || v.Stub.Rejected != "" || v.Stub.Results["TestAdd"] != "fail" || strings.Join(v.Stub.Funcs, ",") != "calc/calc.go: Add" {
		t.Errorf("fix Add: %+v", v)
	}
	if v := results["add Sub"]; !v.OK || v.OnParent["TestSub"] != "build-fail" {
		t.Errorf("add Sub (compile failure on the parent counts as failing): %+v", v)
	}
	if v := results["document Add"]; v.OK || !strings.Contains(v.Why, "no named test fails on the parent") {
		t.Errorf("document Add must be rejected: %+v", v)
	}
	if c := results["fix Add"].CheckCmd; !strings.HasPrefix(c, "GOWORK=off GOFLAGS=-mod=mod CGO_ENABLED=0 go test") ||
		!strings.Contains(c, "-buildvcs=false -run '^(TestAdd)$' ./calc") {
		t.Errorf("check command %q", results["fix Add"].CheckCmd)
	}
}

func TestChooseIsFixedDisjointAndCapped(t *testing.T) {
	var cs []Candidate
	for i := 0; i < 40; i++ {
		repo := []string{"goinfer", "ken", "aikit"}[i%3]
		if i < 30 {
			repo = "goinfer" // most candidates in one repo, so the cap bites
		}
		cs = append(cs, Candidate{Repo: repo, Commit: fmt.Sprintf("%040d", i)})
	}
	excluded := Order(cs)[25].Commit // a pilot task chosen earlier, say, that this set no longer picks
	pilot, e1 := Choose(cs, 10, 4, map[string]bool{excluded: true})
	pilot2, e1b := Choose(append(cs[20:], cs[:20]...), 10, 4, map[string]bool{excluded: true}) // input order must not matter
	if fmt.Sprint(pilot) != fmt.Sprint(pilot2) || fmt.Sprint(e1) != fmt.Sprint(e1b) {
		t.Fatal("choice depends on input order")
	}
	per := map[string]int{}
	seen := map[string]bool{}
	for _, c := range pilot {
		per[c.Repo]++
		seen[c.Commit] = true
	}
	if per["goinfer"] > 4 {
		t.Fatalf("pilot has %d goinfer tasks, cap 4", per["goinfer"])
	}
	// E1's order is every other candidate, uncut and uncapped, in stage 3's order, less the excluded one.
	if len(e1) != len(cs)-len(pilot)-1 {
		t.Fatalf("e1 order has %d, want %d", len(e1), len(cs)-len(pilot)-1)
	}
	ordered := Order(e1)
	for i, c := range e1 {
		if seen[c.Commit] {
			t.Fatalf("%s is in both sets", c.Commit)
		}
		if c.Commit == excluded {
			t.Fatalf("%s was excluded", c.Commit)
		}
		if ordered[i].Commit != c.Commit {
			t.Fatalf("e1 is not in stage 3's order at %d", i)
		}
	}
	// Take applies E1's cap when N is set.
	per = map[string]int{}
	for _, c := range Take(e1, 25, 12) {
		per[c.Repo]++
	}
	if per["goinfer"] != 12 {
		t.Fatalf("Take gave %d goinfer tasks, cap 12", per["goinfer"])
	}
}
