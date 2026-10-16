package mine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestStubCheckRejectsAnOracleThatCannotTellTheFix verifies, through Verify, a commit whose named test only calls the
// new function (it fails on the parent because the function does not exist, and passes on the fix). With the
// function stubbed it still passes, so the candidate is rejected. The function's body is the only user of an import,
// so the stubbed file builds only if that import is dropped.
func TestStubCheckRejectsAnOracleThatCannotTellTheFix(t *testing.T) {
	t.Setenv("GOFLAGS", "")
	dir := t.TempDir()
	write := func(rel, body string) {
		os.MkdirAll(filepath.Join(dir, filepath.Dir(rel)), 0o755)
		os.WriteFile(filepath.Join(dir, rel), []byte(body), 0o644)
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
	gitDo("init", "-q", "-b", "main")
	write("go.mod", "module m\n\ngo 1.22\n")
	write("warn/warn.go", "package warn\n\nfunc Keep() int { return 1 }\n")
	gitDo("add", "-A")
	gitDo("commit", "-q", "-m", "base")
	base := gitDo("rev-parse", "HEAD")
	write("warn/warn.go", "package warn\n\nimport \"strings\"\n\nfunc Keep() int { return 1 }\n\n// Check warns.\nfunc Check(s string) {\n\tif strings.TrimSpace(s) == \"\" {\n\t\tprintln(\"empty\") // a comment inside the body\n\t}\n}\n")
	write("warn/warn_test.go", "package warn\n\nimport \"testing\"\n\nfunc TestCheck(t *testing.T) { Check(\"\") }\n")
	gitDo("add", "-A")
	gitDo("commit", "-q", "-m", "weak")
	r := Repo{Name: "weak", Path: dir, Frozen: gitDo("rev-parse", "HEAD")}
	c := Candidate{Repo: "weak", Commit: r.Frozen, Base: base, Tests: []string{"TestCheck"}, Packages: []string{"./warn"},
		TestFiles: []string{"warn/warn_test.go"}}
	work := filepath.Join(t.TempDir(), "clone")
	if err := PrepareClone(r, work); err != nil {
		t.Fatal(err)
	}
	v := Verify(context.Background(), r, c, work)
	if v.OK || v.Stub == nil || v.Stub.Rejected != RejectPassed || v.Stub.Err != "" || v.Stub.Results["TestCheck"] != "pass" ||
		strings.Join(v.Stub.Funcs, ",") != "warn/warn.go: Check" || !strings.Contains(v.Why, "stub check") {
		t.Fatalf("a no-op oracle must be rejected by the stub check: %+v stub %+v", v, v.Stub)
	}
}

func TestStubSource(t *testing.T) {
	old := "package p\n\nfunc Same() int { return 1 }\n\nfunc (t *T) M() (n int, err error) { return 1, nil }\n\ntype T struct{}\n"
	src := "package p\n\nimport (\n\t\"errors\"\n\t\"fmt\"\n)\n\nfunc Same() int { return 1 }\n\nfunc (t *T) M() (n int, err error) { return 2, errors.New(\"x\") }\n\nfunc New() (*T, fmt.Stringer, error) { return &T{}, nil, nil }\n\ntype T struct{}\n"
	out, funcs, err := stubSource(t.TempDir(), "p.go", old, src)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if strings.Join(funcs, ",") != "p.go: New,p.go: T.M" {
		t.Fatalf("stubbed %v", funcs)
	}
	for _, want := range []string{"func Same() int { return 1 }", "func (t *T) M() (n int, err error) { return }",
		"return *new(*T), *new(fmt.Stringer), *new(error)", "\"fmt\""} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if strings.Contains(got, "errors") {
		t.Errorf("the import only M's old body used was kept:\n%s", got)
	}
}

// TestStubVerdict: a stubbed tree that does not build is a check that did not run, and rejects; so does an oracle
// that passes while stubbed. Only an oracle that fails on the stubbed tree keeps the candidate.
func TestStubVerdict(t *testing.T) {
	oracle := []string{"TestA", "TestB"}
	for _, tc := range []struct {
		res  map[string]string
		want string
	}{
		{map[string]string{"TestA": "pass", "TestB": "pass"}, RejectPassed},
		{map[string]string{"TestA": "pass", "TestB": "build-fail"}, RejectNoBuild},
		{map[string]string{"TestA": "fail", "TestB": "build-fail"}, RejectNoBuild},
		{map[string]string{"TestA": "pass", "TestB": "fail"}, ""},
		{map[string]string{"TestA": "skip", "TestB": "pass"}, ""},
	} {
		if got := stubVerdict(oracle, tc.res); got != tc.want {
			t.Errorf("%v: %q, want %q", tc.res, got, tc.want)
		}
	}
}
