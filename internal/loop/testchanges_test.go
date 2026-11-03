package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
	"github.com/townsendmerino/tack/internal/shell"
	"github.com/townsendmerino/tack/internal/tools"
)

const twoTests = "package a\n\nfunc TestA(t *testing.T) {}\n\nfunc TestB(t *testing.T) {}\n"

func call(id, name string, input map[string]any) goinfertest.Reply {
	b, err := json.Marshal(input)
	if err != nil {
		panic(err)
	}
	return goinfertest.Calls("", id, name, string(b))
}

func bashCall(id, command string) goinfertest.Reply {
	return call(id, "bash", map[string]any{"command": command})
}

func readCall(id, path string) goinfertest.Reply {
	return call(id, "read", map[string]any{"path": path})
}

func editCall(id, path, old, new string) goinfertest.Reply {
	return call(id, "edit", map[string]any{"path": path, "old_string": old, "new_string": new})
}

// deletingShell answers every command with output and carries out a plain rm of files in root, so a write after it sees the file gone.
type deletingShell struct {
	root   string
	output string
}

func (d *deletingShell) Run(_ context.Context, command string, _ time.Duration) (shell.Result, error) {
	if rest, ok := strings.CutPrefix(command, "rm "); ok {
		for _, p := range strings.Fields(rest) {
			if !strings.HasPrefix(p, "-") && !filepath.IsAbs(p) {
				os.Remove(filepath.Join(d.root, p))
			}
		}
	}
	return shell.Result{Output: d.output}, nil
}

// doneAnswer runs a turn over files, with sh's output for every command, and returns done's answer. verify is Config.VerifyDone.
func doneAnswer(t *testing.T, verify bool, task string, files map[string]string, output string, calls ...goinfertest.Reply) string {
	t.Helper()
	replies := append(calls, goinfertest.Calls("", "d1", "done", `{}`), goinfertest.Says("ok"))
	h := buildHarness(t, proto.ModeAuto, &buildShell{}, replies...)
	cfg := h.sess.cfg
	sh := &deletingShell{root: h.ws.Root(), output: output}
	cfg.Tools, cfg.Shell = tools.New(tools.Options{Write: true, Shell: sh, Done: true}), sh
	cfg.BuildAfterEdit, cfg.EditTask, cfg.VerifyDone = false, true, verify
	cfg.Approve = &approver{answer: Approval{Allow: true}} // the guard asks about some of these, and the report is of what ran
	h.sess = New(cfg)
	for p, content := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(h.ws.Root(), p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(h.ws.Root(), p), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.sess.Turn(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	return h.finished("d1").Result
}

// What a turn did to the tests is in done's answer, from its own edits and commands.
func TestDoneReportsTestChanges(t *testing.T) {
	files := map[string]string{"a_test.go": twoTests, "s_test.go": "package a\n\nfunc TestS(t *testing.T) { t.Skip() }\n", "a.go": "package a\n// x\n", ".coveragerc": "fail_under = 80\nmax_lines = 80\n", "h_test.go": "package a\n\nfunc TestLast(t *testing.T) {\n\tt.Log(1)\n}\n\nfunc helper() {}\n", "tests/test_h.py": "def test_a():\n    pass\n\ndef helper():\n    pass\n", "sub/x": "", "tests/h.py": "x = 1\n", "tests/more/y": "", "tests/test_l.py": "def test_l(\n    x,\n):\n    assert x\n", "c.test.js": "it('handles }', () => {\n  expect(1)\n})\n", "d.test.js": "describe('a', () => {\n  it('works', () => { one() })\n})\ndescribe('b', () => {\n  it('works', () => { two() })\n})\n", "e.test.js": "it('works', () => expect(1).toBe(1))\n\nconst h = 1\n", "tests/test_b.py": "def test_b():\n    x = 1\n\n    assert x\n", "two.cfg": "[a]\nfail_under = 50\n[b]\nfail_under = 80\nx = 1\n", "b.test.js": "it('works', () => {})\n", "tests/r.rs": "#[test]\nfn adds() {}\n", "c.go": "package a\nconst coverage = 80\n", ".retry": "recover = 80\n", "tests/test_x.py": "def test_a():\n    pass\n"}
	skip := "func TestA(t *testing.T) { t.Skip() }"
	for _, c := range []struct {
		name  string
		task  string
		calls []goinfertest.Reply
		want  []string // each in the notes; none means no notes at all
		not   string   // in no note
	}{
		{name: "a test function removed", calls: []goinfertest.Reply{readCall("r", "a_test.go"), editCall("e", "a_test.go", "func TestA(t *testing.T) {}\n\n", "")}, want: []string{"removed TestA from a_test.go"}},
		{name: "a python test removed", calls: []goinfertest.Reply{readCall("r", "tests/test_x.py"), editCall("e", "tests/test_x.py", "def test_a():\n    pass\n", "x = 1\n")}, want: []string{"removed test_a from tests/test_x.py"}},
		{name: "a skip added", calls: []goinfertest.Reply{readCall("r", "a_test.go"), editCall("e", "a_test.go", "func TestA(t *testing.T) {}", skip)}, want: []string{"added a skip to a_test.go"}},
		{name: "write removes a test", calls: []goinfertest.Reply{readCall("r", "a_test.go"), call("w", "write", map[string]any{"path": "a_test.go", "content": "package a\n\nfunc TestB(t *testing.T) {}\n"})}, want: []string{"removed TestA from a_test.go"}},
		{name: "write adds a skip", calls: []goinfertest.Reply{readCall("r", "a_test.go"), call("w", "write", map[string]any{"path": "a_test.go", "content": "package a\n\n" + skip + "\n\nfunc TestB(t *testing.T) {}\n"})}, want: []string{"added a skip to a_test.go"}},
		{name: "an edit to a test the task names", task: "make a_test.go pass", calls: []goinfertest.Reply{readCall("r", "a_test.go"), editCall("e", "a_test.go", "package a", "package a // c")}, want: []string{"a_test.go, a test file the task names"}},
		{name: "deleted by rm", calls: []goinfertest.Reply{bashCall("b", "rm a_test.go")}, want: []string{"deleted a_test.go"}},
		{name: "deleted by git rm", calls: []goinfertest.Reply{bashCall("b", "git rm -q a_test.go")}, want: []string{"deleted a_test.go"}},
		{name: "a test directory deleted", calls: []goinfertest.Reply{bashCall("b", "rm -rf tests")}, want: []string{"deleted tests"}},
		{name: "a skip through sed", calls: []goinfertest.Reply{bashCall("b", `sed -i 's/{}/{ t.Skip() }/' a_test.go`)}, want: []string{"added a skip through sed"}},
		{name: "a skip through perl", calls: []goinfertest.Reply{bashCall("b", `perl -pi -e 's/x/t.Skip()/' a_test.go`)}, want: []string{"added a skip through perl"}},
		{name: "a skip through echo", calls: []goinfertest.Reply{bashCall("b", `echo 't.Skip()' >> a_test.go`)}, want: []string{"added a skip through echo"}},
		{name: "a narrower -run", calls: []goinfertest.Reply{bashCall("b", "go test ./..."), bashCall("c", "go test -run TestA ./...")}, want: []string{"narrowed the tests with -run"}},
		{name: "a narrower -run=", calls: []goinfertest.Reply{bashCall("b", "go test ./..."), bashCall("c", "go test -run=TestA ./...")}, want: []string{"narrowed the tests with -run"}},
		{name: "the first of two coverage keys lowered", calls: []goinfertest.Reply{readCall("r", "two.cfg"), editCall("e", "two.cfg", "fail_under = 50", "fail_under = 40")}, want: []string{"from 50 to 40"}},
		{name: "a lower coverage gate", calls: []goinfertest.Reply{readCall("r", ".coveragerc"), editCall("e", ".coveragerc", "fail_under = 80", "fail_under = 60")}, want: []string{"lowered the coverage gate in .coveragerc from 80 to 60"}},
		{name: "a test commented out", calls: []goinfertest.Reply{readCall("r", "a_test.go"), editCall("e", "a_test.go", "func TestA(t *testing.T) {}", "// func TestA(t *testing.T) {}")}, want: []string{"removed TestA from a_test.go"}},
		{name: "a named test edited", task: "TestA fails", calls: []goinfertest.Reply{readCall("r", "a_test.go"), editCall("e", "a_test.go", "func TestA(t *testing.T) {}", "func TestA(t *testing.T) { t.Log(1) }")}, want: []string{"edited TestA in a_test.go, a test the task names"}},
		{name: "moved out of the tests", calls: []goinfertest.Reply{bashCall("b", "mv a_test.go a_test.go.off")}, want: []string{"moved a_test.go to a_test.go.off"}},
		{name: "a build constraint drops the file", calls: []goinfertest.Reply{readCall("r", "a_test.go"), editCall("e", "a_test.go", "package a", "//go:build ignore\n\npackage a")}, want: []string{"added a skip to a_test.go"}},
		{name: "narrow, wide, narrow", calls: []goinfertest.Reply{bashCall("b", "go test -run X ./..."), bashCall("c", "go test ./..."), bashCall("d", "go test -short ./...")}, want: []string{"narrowed the tests with -short"}},
		{name: "git -C rm", calls: []goinfertest.Reply{bashCall("b", "git -C . rm a_test.go")}, want: []string{"deleted a_test.go"}},
		{name: "unlink", calls: []goinfertest.Reply{bashCall("b", "unlink a_test.go")}, want: []string{"deleted a_test.go"}},
		{name: "rm by absolute path in the repository is reported", calls: []goinfertest.Reply{bashCall("b", "rm ./a_test.go")}, want: []string{"deleted a_test.go"}},
		{name: "a skip through tee", calls: []goinfertest.Reply{bashCall("b", `echo 't.Skip()' | tee -a a_test.go`)}, want: []string{"added a skip through tee"}},
		{name: "a skip through a heredoc", calls: []goinfertest.Reply{bashCall("b", "cat > a_test.go <<X\nt.Skip()\nX")}, want: []string{"added a skip through cat"}},
		{name: "a js test removed", calls: []goinfertest.Reply{readCall("r", "b.test.js"), editCall("e", "b.test.js", "it('works', () => {})\n", "")}, want: []string{"removed works from b.test.js"}},
		{name: "a rust test removed", calls: []goinfertest.Reply{readCall("r", "tests/r.rs"), editCall("e", "tests/r.rs", "#[test]\nfn adds() {}\n", "")}, want: []string{"removed adds from tests/r.rs"}},

		{name: "a narrower -skip", calls: []goinfertest.Reply{bashCall("b", "go test ./..."), bashCall("c", "go test -skip TestA ./...")}, want: []string{"narrowed the tests with -skip"}},
		{name: "a line that failed later still ran", calls: []goinfertest.Reply{bashCall("b", "rm a_test.go; false")}, want: []string{"deleted a_test.go"}},

		{name: "deleted and written again", calls: []goinfertest.Reply{bashCall("b", "rm a_test.go"), call("w", "write", map[string]any{"path": "a_test.go", "content": "package a\n"})}, want: []string{"deleted a_test.go"}},
		{name: "deleted and written again with a skip", calls: []goinfertest.Reply{bashCall("b", "rm a_test.go"), call("w", "write", map[string]any{"path": "a_test.go", "content": skip + "\n"})}, want: []string{"deleted a_test.go", "added a skip to a_test.go"}},
		{name: "a rust test given #[ignore]", calls: []goinfertest.Reply{readCall("r", "tests/r.rs"), editCall("e", "tests/r.rs", "#[test]\nfn adds", "#[test]\n#[ignore]\nfn adds")}, want: []string{"added a skip to tests/r.rs"}, not: "removed"},
		{name: "moved out of the repository", calls: []goinfertest.Reply{bashCall("b", "mv a_test.go /tmp/a_test.go")}, want: []string{"moved a_test.go to /tmp/a_test.go"}},
		{name: "moved out to a directory", calls: []goinfertest.Reply{bashCall("b", "mv a_test.go /tmp/")}, want: []string{"moved a_test.go to /tmp/"}},
		{name: "a named test edited at its end", task: "TestLast fails", calls: []goinfertest.Reply{readCall("r", "h_test.go"), editCall("e", "h_test.go", "t.Log(1)\n}", "t.Log(2)\n}")}, want: []string{"edited TestLast in h_test.go"}},
		{name: "a named python test edited past a blank line", task: "test_b fails", calls: []goinfertest.Reply{readCall("r", "tests/test_b.py"), editCall("e", "tests/test_b.py", "    assert x\n", "    assert not x\n")}, want: []string{"edited test_b in tests/test_b.py"}},
		{name: "a named python test edited", task: "test_a fails", calls: []goinfertest.Reply{readCall("r", "tests/test_x.py"), editCall("e", "tests/test_x.py", "    pass\n", "    assert 1\n")}, want: []string{"edited test_a in tests/test_x.py"}},

		{name: "a directory's test moved to a directory that isn't one", calls: []goinfertest.Reply{bashCall("b", "mv tests/h.py sub")}, want: []string{"moved tests/h.py to sub"}},
		{name: "a test commented out with a block", calls: []goinfertest.Reply{readCall("r", "a_test.go"), editCall("e", "a_test.go", "func TestA(t *testing.T) {}", "/*\nfunc TestA(t *testing.T) {}\n*/")}, want: []string{"removed TestA from a_test.go"}},
		{name: "a python test in a docstring", calls: []goinfertest.Reply{readCall("r", "tests/test_x.py"), editCall("e", "tests/test_x.py", "def test_a():\n    pass\n", "\"\"\"\ndef test_a():\n    pass\n\"\"\"\n")}, want: []string{"removed test_a from tests/test_x.py"}},
		{name: "a named python test with a long signature", task: "test_l fails", calls: []goinfertest.Reply{readCall("r", "tests/test_l.py"), editCall("e", "tests/test_l.py", "    assert x\n", "    assert not x\n")}, want: []string{"edited test_l in tests/test_l.py"}},
		{name: "a named js test with a brace in its title", task: "handles } fails", calls: []goinfertest.Reply{readCall("r", "c.test.js"), editCall("e", "c.test.js", "expect(1)", "expect(2)")}, want: []string{"edited handles } in c.test.js"}},
		{name: "one of two tests with the same title removed", calls: []goinfertest.Reply{readCall("r", "d.test.js"), editCall("e", "d.test.js", "  it('works', () => { two() })\n", "")}, want: []string{"removed works from d.test.js"}},

		// None of these says anything.
		{name: "a scratch test with a skip, written and deleted", calls: []goinfertest.Reply{call("w", "write", map[string]any{"path": "n_test.go", "content": skip + "\n"}), bashCall("b", "rm n_test.go")}},
		{name: "a helper after a one-line js test", task: "works fails", calls: []goinfertest.Reply{readCall("r", "e.test.js"), editCall("e", "e.test.js", "const h = 1", "const h = 2")}},
		{name: "a skip in a block comment", calls: []goinfertest.Reply{readCall("r", "a_test.go"), editCall("e", "a_test.go", "package a", "package a\n/* t.Skip() */")}},
		{name: "a test's arrow in an echo", calls: []goinfertest.Reply{bashCall("b", `echo 'x -> a_test.go t.Skip()'`)}},
		{name: "a test moved to a test directory", calls: []goinfertest.Reply{bashCall("b", "mv tests/test_x.py tests/more")}},
		{name: "a test file edited, then its directory deleted", want: []string{"deleted tests"}, not: "edited", task: "test_x.py", calls: []goinfertest.Reply{readCall("r", "tests/test_x.py"), editCall("e", "tests/test_x.py", "pass", "assert 1"), bashCall("b", "rm -rf tests")}},
		{name: "a helper after the named test", task: "TestLast fails", calls: []goinfertest.Reply{readCall("r", "h_test.go"), editCall("e", "h_test.go", "func helper() {}", "func helper() { _ = 1 }")}},
		{name: "a helper after a named python test", task: "test_a fails", calls: []goinfertest.Reply{readCall("r", "tests/test_h.py"), editCall("e", "tests/test_h.py", "def helper():\n    pass\n", "def helper():\n    return 1\n")}},
		{name: "a helper's text in a named one's file, with a brace in a string", task: "TestB fails", calls: []goinfertest.Reply{readCall("r", "a_test.go"), editCall("e", "a_test.go", "package a", "package a // {")}},
		{name: "moved into a test directory", calls: []goinfertest.Reply{bashCall("b", "mv tests/test_x.py tests")}},
		{name: "moved to a directory that exists", calls: []goinfertest.Reply{bashCall("b", "mv a_test.go sub")}},
		{name: "the same coverage key twice, one edit elsewhere", calls: []goinfertest.Reply{readCall("r", "two.cfg"), editCall("e", "two.cfg", "x = 1", "x = 2")}},
		{name: "the second of two coverage keys raised", calls: []goinfertest.Reply{readCall("r", "two.cfg"), editCall("e", "two.cfg", "fail_under = 80", "fail_under = 90")}},
		{name: "a sed that keeps the skips even", calls: []goinfertest.Reply{bashCall("b", `sed -i 's/t.Skip()/t.Skipf("x")/' s_test.go`)}},
		{name: "an echo with no skip", calls: []goinfertest.Reply{bashCall("b", `echo '// note' >> a_test.go`)}},
		{name: "a sed on another file", calls: []goinfertest.Reply{bashCall("b", `sed -i 's/a/t.Skip()/' notes.txt`)}},
		{name: "a test file created that the task names", task: "add n_test.go", calls: []goinfertest.Reply{call("w", "write", map[string]any{"path": "n_test.go", "content": twoTests})}},
		{name: "a task naming another test", task: "TestB fails", calls: []goinfertest.Reply{readCall("r", "a_test.go"), editCall("e", "a_test.go", "func TestA(t *testing.T) {}", "func TestA(t *testing.T) { t.Log(1) }")}},
		{name: "a task whose word holds a test's name", task: "see TestAB", calls: []goinfertest.Reply{readCall("r", "a_test.go"), editCall("e", "a_test.go", "func TestA(t *testing.T) {}", "func TestA(t *testing.T) { t.Log(1) }")}},
		{name: "a coverage number in source", calls: []goinfertest.Reply{readCall("r", "c.go"), editCall("e", "c.go", "coverage = 80", "coverage = 60")}},
		{name: "a word holding cov", calls: []goinfertest.Reply{readCall("r", ".retry"), editCall("e", ".retry", "recover = 80", "recover = 60")}},
		{name: "wide, narrow to debug, wide", calls: []goinfertest.Reply{bashCall("b", "go test ./..."), bashCall("c", "go test -run TestA ./..."), bashCall("d", "go test ./...")}},
		{name: "a scratch test written and deleted", calls: []goinfertest.Reply{call("w", "write", map[string]any{"path": "n_test.go", "content": twoTests}), bashCall("b", "rm n_test.go")}},
		{name: "a test removed and put back", calls: []goinfertest.Reply{readCall("r", "a_test.go"), editCall("e", "a_test.go", "func TestA(t *testing.T) {}", "// gone"), editCall("f", "a_test.go", "// gone", "func TestA(t *testing.T) {}")}},
		{name: "a skip added and taken out", calls: []goinfertest.Reply{readCall("r", "a_test.go"), editCall("e", "a_test.go", "func TestA(t *testing.T) {}", skip), editCall("f", "a_test.go", skip, "func TestA(t *testing.T) { }")}},
		{name: "a sed that takes a skip out", calls: []goinfertest.Reply{bashCall("b", `sed -i 's/t.Skip()//' s_test.go`)}},
		{name: "a sed that deletes the line", calls: []goinfertest.Reply{bashCall("b", `sed -i '/t.Skip()/d' s_test.go`)}},
		{name: "a sed that prints it", calls: []goinfertest.Reply{bashCall("b", `sed -n '/t.Skip()/p' s_test.go`)}},
		{name: "a cat piped to grep", calls: []goinfertest.Reply{bashCall("b", "cat s_test.go | grep t.Skip")}},
		{name: "rm outside the repository", calls: []goinfertest.Reply{bashCall("b", "rm /tmp/x_test.go")}},
		{name: "rm of a file called test", calls: []goinfertest.Reply{bashCall("b", "rm test")}},
		{name: "mv between test names", calls: []goinfertest.Reply{bashCall("b", "mv a_test.go b_test.go")}},
		{name: "mv into a directory", calls: []goinfertest.Reply{bashCall("b", "mv a_test.go sub/")}},
		{name: "git status", calls: []goinfertest.Reply{bashCall("b", "git status a_test.go")}},
		{name: "an ordinary edit", calls: []goinfertest.Reply{readCall("r", "a.go"), editCall("e", "a.go", "// x", "// y")}},
		{name: "a test added", calls: []goinfertest.Reply{readCall("r", "a_test.go"), editCall("e", "a_test.go", "package a\n", "package a\n\nfunc TestC(t *testing.T) {}\n")}},
		{name: "a test body edited", calls: []goinfertest.Reply{readCall("r", "a_test.go"), editCall("e", "a_test.go", "func TestA(t *testing.T) {}", "func TestA(t *testing.T) { t.Log(1) }")}},
		{name: "a skip outside a test file", calls: []goinfertest.Reply{readCall("r", "a.go"), editCall("e", "a.go", "// x", "// t.Skip()")}},
		{name: "a skip already there", calls: []goinfertest.Reply{readCall("r", "s_test.go"), editCall("e", "s_test.go", "t.Skip()", "t.Log(1); t.Skip()")}},
		{name: "a skip taken out", calls: []goinfertest.Reply{readCall("r", "s_test.go"), editCall("e", "s_test.go", "t.Skip()", "t.Log(1)")}},
		{name: "rm of another file", calls: []goinfertest.Reply{bashCall("b", "rm a.go")}},
		{name: "grep for a skip", calls: []goinfertest.Reply{bashCall("b", "grep t.Skip a_test.go")}},
		{name: "sed with no skip", calls: []goinfertest.Reply{bashCall("b", `sed -i 's/a/b/' a_test.go`)}},
		{name: "echo with no test file", calls: []goinfertest.Reply{bashCall("b", `echo 't.Skip()' >> notes.txt`)}},
		{name: "-run from the start", calls: []goinfertest.Reply{bashCall("b", "go test -run TestA ./...")}},
		{name: "a wider run after -run", calls: []goinfertest.Reply{bashCall("b", "go test -run TestA ./..."), bashCall("c", "go test ./...")}},
		{name: "a higher coverage gate", calls: []goinfertest.Reply{readCall("r", ".coveragerc"), editCall("e", ".coveragerc", "fail_under = 80", "fail_under = 90")}},
		{name: "another number in that file", calls: []goinfertest.Reply{readCall("r", ".coveragerc"), editCall("e", ".coveragerc", "max_lines = 80", "max_lines = 60")}},
		{name: "a test file the task doesn't name", task: "fix a.go", calls: []goinfertest.Reply{readCall("r", "a_test.go"), editCall("e", "a_test.go", "package a", "package a // c")}},
	} {
		t.Run(c.name, func(t *testing.T) {
			task := c.task
			if task == "" {
				task = "fix it"
			}
			answer, notes := DoneNotes(doneAnswer(t, false, task, files, "ok\n", c.calls...))
			if answer != "done" {
				t.Errorf("answer %q", answer)
			}
			if len(c.want) == 0 && len(notes) != 0 {
				t.Errorf("notes: %q", notes)
			}
			if c.not != "" && strings.Contains(strings.Join(notes, "\n"), c.not) {
				t.Errorf("notes %q hold %q", notes, c.not)
			}
			for _, w := range c.want {
				if !strings.Contains(strings.Join(notes, "\n"), w) {
					t.Errorf("notes %q lack %q", notes, w)
				}
			}
		})
	}
}

// A test run that ran nothing verifies nothing. go test says so package by package, so a run where another package ran tests isn't one.
func TestRanNothing(t *testing.T) {
	for _, c := range []struct {
		output string
		want   bool
	}{
		{"ok  \tx/a\t0.1s [no tests to run]\n", true},
		{"ok  \tx/a\t0.1s [no tests to run]\nok  \tx/b\t0.1s [no tests to run]\n", true},
		{"testing: warning: no tests to run\nPASS\nok  \tx/a\t0.1s [no tests to run]\n", true},
		{"?   \tx/a\t[no test files]\n", true},
		{"?   \tx/a\t[no test files]\nok  \tx/b\t0.1s [no tests to run]\n", true},
		{"ok  \tx/a\t0.1s [no tests to run]\nok  \tx/b\t0.1s\n", false},
		{"?   \tx/a\t[no test files]\nok  \tx/b\t0.1s\n", false},
		{"ok  \tx/a\t(cached)\n", false},
		{"--- FAIL: TestA (0.00s)\nFAIL\nFAIL\tx/a\t0.1s\n", false},
		{"ok  \tx/a\t0.1s [no tests to run]\nFAIL\tx/b [build failed]\n", false},
		{"# x/a\na.go:1:1: bad\n", false},
		{"", false},
	} {
		if got := ranNothing(c.output); got != c.want {
			t.Errorf("%q: %v, want %v", c.output, got, c.want)
		}
	}
}

// With VerifyDone, done's answer says a run that ran nothing verified nothing. It isn't refused for it: done still ends the turn at once, so
// the runs of E1 are what they were (DESIGN §6.3, shown and not refused).
func TestDoneAfterATestRunThatRanNothing(t *testing.T) {
	files := map[string]string{"a.go": "package a\n// x\n"}
	calls := []goinfertest.Reply{readCall("r", "a.go"), editCall("e", "a.go", "// x", "// y"), bashCall("b", "go test -run NoSuch ./...")}
	answer, notes := DoneNotes(doneAnswer(t, true, "fix it", files, "ok  \tx/a\t0.1s [no tests to run]\n", calls...))
	if answer != DoneNoTests {
		t.Errorf("answer %q, want %q", answer, DoneNoTests)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "ran nothing") || !strings.Contains(notes[0], "go test -run NoSuch") {
		t.Errorf("notes %q", notes)
	}
	// A build after it does verify.
	calls = append(calls[:3:3], bashCall("c", "go build ./..."))
	if answer, _ := DoneNotes(doneAnswer(t, true, "fix it", files, "", calls...)); answer != "done" {
		t.Errorf("after a build: %q", answer)
	}
	// With nothing changed, nothing needs verifying, and the run is still reported.
	answer, notes = DoneNotes(doneAnswer(t, false, "fix it", files, "ok  \tx/a\t0.1s [no tests to run]\n", bashCall("b", "go test -run NoSuch ./...")))
	if answer != "done" || len(notes) != 1 {
		t.Errorf("verify off: %q %q", answer, notes)
	}
}

// The notes are text from the model and the repository, so they get the escaping any tool output does, and there's a limit to them.
func TestDoneNotesAreEscapedAndCapped(t *testing.T) {
	_, notes := DoneNotes(doneAnswer(t, false, "fix it", nil, "ok\n", bashCall("b", "rm 'a\x1b[2K_test.go'")))
	if len(notes) != 1 || strings.ContainsRune(notes[0], 0x1b) || !strings.Contains(notes[0], `\x1b[2K`) {
		t.Errorf("notes %q", notes)
	}
	var many []string
	for i := range maxTestNotes + 3 {
		many = append(many, fmt.Sprintf("a%d_test.go", i))
	}
	_, notes = DoneNotes(doneAnswer(t, false, "fix it", nil, "ok\n", bashCall("b", "rm "+strings.Join(many, " "))))
	if len(notes) != maxTestNotes+1 || !strings.Contains(notes[maxTestNotes], "3 more") {
		t.Errorf("%d notes: %q", len(notes), notes)
	}
	// A note is one line, whatever the path holds.
	_, notes = DoneNotes(doneAnswer(t, false, "fix it", nil, "ok\n", bashCall("b", "rm 'a\nb_test.go'")))
	if len(notes) != 1 || !strings.Contains(notes[0], "a b_test.go") {
		t.Errorf("notes %q", notes)
	}
	// A long path is cut.
	_, notes = DoneNotes(doneAnswer(t, false, "fix it", nil, "ok\n", bashCall("b", "rm "+strings.Repeat("a", 2*maxTestNoteLen)+"_test.go")))
	if len(notes) != 1 || len(notes[0]) > maxTestNoteLen+len("…") {
		t.Errorf("notes %q", notes)
	}
	// The same thing twice is said once.
	_, notes = DoneNotes(doneAnswer(t, false, "fix it", nil, "ok\n", bashCall("b", "rm a_test.go"), bashCall("c", "rm -f a_test.go")))
	if len(notes) != 1 {
		t.Errorf("notes %q", notes)
	}
}

// An answer with no notes is itself.
func TestDoneNotesOfAPlainAnswer(t *testing.T) {
	for _, a := range []string{"done", DoneUnverified, DoneVerifyFailed, DoneNoTests} {
		if got, notes := DoneNotes(a); got != a || notes != nil {
			t.Errorf("%q: %q %q", a, got, notes)
		}
	}
}

func TestIsTestFile(t *testing.T) {
	for p, want := range map[string]bool{
		"a_test.go": true, "x/a_test.go": true, "a_test.py": true, "test_a.py": true, "a.test.js": true, "a.spec.ts": true,
		"tests/h.py": true, "test/h.go": true, "src/__tests__/h.js": true,
		"a.go": false, "test.go": false, "latest.py": false, "contest/h.go": false, "testdata/h.go": false, "test": false,
		"test_a.go": false,
	} {
		if got := isTestFile(p); got != want {
			t.Errorf("%s: %v", p, got)
		}
	}
}

// Every skip the report knows.
func TestSkipRe(t *testing.T) {
	for _, s := range []string{"t.Skip()", "t.Skipf(x)", "t.SkipNow()", "b.Skip()", "f.Skip()", "@pytest.mark.skip", "@pytest.mark.skipif(x)", "@pytest.mark.xfail",
		"pytest.skip(x)", "@unittest.skip(x)", "self.skipTest(x)", "it.skip(x)", "describe.skip(x)", "xit(x)", "xdescribe(x)", "xtest(x)", "#[ignore]", "//go:build ignore"} {
		if !skipRe.MatchString(s) {
			t.Errorf("%q", s)
		}
	}
	for _, s := range []string{"st.Skip", "t.Skipped()", "SkipDir", "exit(1)", "@pytest.mark.parametrize", "//go:build linux", "#[inline]"} {
		if skipRe.MatchString(s) {
			t.Errorf("%q", s)
		}
	}
}

// A report covers its own turn, and a file a command deleted says only that.
func TestDoneNotesCoverTheirTurn(t *testing.T) {
	sh := &buildShell{result: shell.Result{Output: "ok\n"}}
	done := goinfertest.Calls("", "d1", "done", `{}`)
	h := buildHarness(t, proto.ModeAuto, sh, readCall("r", "a_test.go"), editCall("e", "a_test.go", "func TestA(t *testing.T) {}\n\n", ""), done,
		goinfertest.Calls("", "d2", "done", `{}`))
	if err := os.WriteFile(filepath.Join(h.ws.Root(), "a_test.go"), []byte(twoTests), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := h.sess.cfg
	cfg.Tools = tools.New(tools.Options{Write: true, Shell: sh, Done: true})
	cfg.BuildAfterEdit, cfg.EditTask = false, true
	h.sess = New(cfg)
	if _, err := h.sess.Turn(context.Background(), "one"); err != nil {
		t.Fatal(err)
	}
	if _, notes := DoneNotes(h.finished("d1").Result); len(notes) != 1 {
		t.Fatalf("first turn: %q", notes)
	}
	if _, err := h.sess.Turn(context.Background(), "two"); err != nil {
		t.Fatal(err)
	}
	if r := h.finished("d2").Result; r != "done" {
		t.Errorf("second turn: %q", r)
	}

	_, notes := DoneNotes(doneAnswer(t, false, "fix it", map[string]string{"a_test.go": twoTests}, "ok\n", readCall("r", "a_test.go"),
		editCall("e", "a_test.go", "func TestA(t *testing.T) {}\n\n", ""), bashCall("b", "rm a_test.go")))
	if len(notes) != 1 || notes[0] != "deleted a_test.go" {
		t.Errorf("deleted: %q", notes)
	}
}

// A command refused, or a lone one that failed, did nothing, so the report doesn't say it did.
func TestDoneNotesOnlyWhatRan(t *testing.T) {
	sh := &buildShell{result: shell.Result{Output: "ok\n"}}
	h := buildHarness(t, proto.ModeAuto, sh, bashCall("b", "rm -rf tests"), goinfertest.Calls("", "d1", "done", `{}`), goinfertest.Says("ok"))
	cfg := h.sess.cfg
	cfg.Tools = tools.New(tools.Options{Write: true, Shell: sh, Done: true})
	cfg.BuildAfterEdit, cfg.EditTask = false, true
	h.sess = New(cfg) // no one to ask, so the guard's question refuses it
	if _, err := h.sess.Turn(context.Background(), "fix it"); err != nil {
		t.Fatal(err)
	}
	if len(sh.ran) != 0 {
		t.Fatalf("ran %q", sh.ran)
	}
	if _, notes := DoneNotes(h.finished("d1").Result); notes != nil {
		t.Errorf("refused: %q", notes)
	}

	sh = &buildShell{result: shell.Result{Exit: 1, Output: "rm: no\n"}}
	h = buildHarness(t, proto.ModeAuto, sh, bashCall("b", "rm a_test.go"), goinfertest.Calls("", "d1", "done", `{}`), goinfertest.Says("ok"))
	cfg = h.sess.cfg
	cfg.Tools = tools.New(tools.Options{Write: true, Shell: sh, Done: true})
	cfg.BuildAfterEdit, cfg.EditTask = false, true
	h.sess = New(cfg)
	if _, err := h.sess.Turn(context.Background(), "fix it"); err != nil {
		t.Fatal(err)
	}
	if _, notes := DoneNotes(h.finished("d1").Result); notes != nil {
		t.Errorf("failed: %q", notes)
	}

	// In a longer line the failure may be another command's, so what the line says it did is reported.
	sh = &buildShell{result: shell.Result{Exit: 1, Output: "FAIL\n"}}
	h = buildHarness(t, proto.ModeAuto, sh, bashCall("b", "rm a_test.go; go test ./..."), goinfertest.Calls("", "d1", "done", `{}`), goinfertest.Says("ok"))
	cfg = h.sess.cfg
	cfg.Tools = tools.New(tools.Options{Write: true, Shell: sh, Done: true})
	cfg.BuildAfterEdit, cfg.EditTask = false, true
	h.sess = New(cfg)
	if _, err := h.sess.Turn(context.Background(), "fix it"); err != nil {
		t.Fatal(err)
	}
	if _, notes := DoneNotes(h.finished("d1").Result); len(notes) != 1 || notes[0] != "deleted a_test.go" {
		t.Errorf("a longer line: %q", notes)
	}
}

// A build beside a test run that ran nothing did verify, and only the last verification counts for the note.
func TestRanNothingBesideABuild(t *testing.T) {
	files := map[string]string{"a.go": "package a\n// x\n"}
	nothing := "ok  \tx/a\t0.1s [no tests to run]\n"
	edit := []goinfertest.Reply{readCall("r", "a.go"), editCall("e", "a.go", "// x", "// y")}
	answer, notes := DoneNotes(doneAnswer(t, true, "fix it", files, nothing, append(edit[:2:2], bashCall("b", "go build ./... && go test -run NoSuch ./..."))...))
	if answer != "done" || notes != nil {
		t.Errorf("with a build: %q %q", answer, notes)
	}
	answer, notes = DoneNotes(doneAnswer(t, true, "fix it", files, nothing, append(edit[:2:2], bashCall("b", "go vet ./... ; go test -run NoSuch ./..."))...))
	if answer != "done" || notes != nil {
		t.Errorf("with a vet: %q %q", answer, notes)
	}
	// A pipe may have taken away the lines that said a test ran, so its output says nothing about the run.
	answer, notes = DoneNotes(doneAnswer(t, true, "fix it", files, "?   \tx/a\t[no test files]\n", append(edit[:2:2], bashCall("b", "go test ./... | tail -1"))...))
	if answer != "done" || notes != nil {
		t.Errorf("through a pipe: %q %q", answer, notes)
	}
	// Changed after the empty run: unverified is what done says.
	answer, notes = DoneNotes(doneAnswer(t, true, "fix it", files, nothing, readCall("r", "a.go"), bashCall("b", "go test -run NoSuch ./..."), editCall("e", "a.go", "// x", "// y"), bashCall("c", "echo hi"), goinfertest.Calls("", "d0", "done", `{}`), goinfertest.Calls("", "d9", "done", `{}`), goinfertest.Calls("", "d8", "done", `{}`)))
	if answer != DoneUnverified || notes != nil {
		t.Errorf("changed after: %q %q", answer, notes)
	}
	// A | that isn't a pipe doesn't get in the way.
	for _, command := range []string{"go test -run 'TestA|TestB' ./...", "go test -run NoSuch ./... || true"} {
		answer, notes = DoneNotes(doneAnswer(t, true, "fix it", files, nothing, append(edit[:2:2], bashCall("b", command))...))
		if answer != DoneNoTests || len(notes) != 1 {
			t.Errorf("%s: %q %q", command, answer, notes)
		}
	}
	// |& is a pipe too.
	answer, notes = DoneNotes(doneAnswer(t, true, "fix it", files, nothing, append(edit[:2:2], bashCall("b", "go test ./... |& tail -1"))...))
	if answer != "done" || notes != nil {
		t.Errorf("through |&: %q %q", answer, notes)
	}
}

// A path in the repository, written from its root or relative to it, is the same path; one that leaves isn't in it.
func TestInRepo(t *testing.T) {
	for _, c := range []struct {
		p, want string
		ok      bool
	}{{"/r/a_test.go", "a_test.go", true}, {"a_test.go", "a_test.go", true}, {"./x/a_test.go", "x/a_test.go", true}, {"/elsewhere/a_test.go", "", false}, {"../a_test.go", "", false}, {"..", "", false}} {
		if got, ok := inRepo("/r", c.p); got != c.want || ok != c.ok {
			t.Errorf("%s: %q %v", c.p, got, ok)
		}
	}
}

// A line that ran nothing and then failed says it failed, not that it ran nothing.
func TestRanNothingThatFailed(t *testing.T) {
	v := &verification{call: 2, nothing: true, exit: 1}
	c := newTestChanges("x", nil)
	if got := c.report(v, 1); got != "" {
		t.Errorf("report %q", got)
	}
}
