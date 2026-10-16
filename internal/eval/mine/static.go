// Package mine finds E1's tasks in the history of goinfer, ken and aikit, by the rules registered in
// docs/measurements/e1-task-selection-preregistration-2026-10-16.md: a static filter (stage 1), verification in a
// parent-only tree (stage 2), and a fixed-order choice (stage 3). Rule numbers in comments are that page's.
package mine

import (
	"bufio"
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os/exec"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// Repo is one repository's frozen history.
type Repo struct {
	Name   string // "goinfer", "ken", "aikit"
	Path   string // a local clone
	Frozen string // the commit the history is read back from (first parent)
}

// Candidate is a commit that passed stage 1.
type Candidate struct {
	Repo       string    `json:"repo"`
	Commit     string    `json:"commit"`
	Base       string    `json:"base"` // the parent
	Subject    string    `json:"subject"`
	Tests      []string  `json:"tests"`    // named tests (rule 5)
	Packages   []string  `json:"packages"` // their packages, as ./dir from the module root
	TestFiles  []string  `json:"test_files"`
	Why        Measure   `json:"why"`
	Verified   *Verified `json:"verified,omitempty"` // stage 2
	Statement  string    `json:"statement,omitempty"`
	ReviewedBy string    `json:"reviewed_by,omitempty"`
}

// Measure is the stage-1 evidence a task was picked on.
type Measure struct {
	NonTestFiles   int `json:"non_test_files"`
	NonTestLines   int `json:"non_test_lines"`
	DiffLines      int `json:"diff_lines"`
	LargestFileLen int `json:"largest_file_lines"`
}

// Static limits (rules 2–4).
const (
	maxNonTestFiles = 3
	maxNonTestLines = 80
	maxDiffLines    = 400
	maxFileLines    = 1000
	maxTestdataSize = 1 << 20
)

// git runs a git command in dir.
func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, stderr.String())
	}
	return string(out), nil
}

// subModules is the directories of the frozen commit's nested go.mod files: everything under them is outside the root
// module.
func subModules(r Repo) ([]string, error) {
	out, err := git(r.Path, "ls-tree", "-r", "--name-only", r.Frozen)
	if err != nil {
		return nil, err
	}
	var dirs []string
	for _, f := range strings.Split(out, "\n") {
		if path.Base(f) == "go.mod" && path.Dir(f) != "." {
			dirs = append(dirs, path.Dir(f)+"/")
		}
	}
	return dirs, nil
}

func inRoot(f string, subs []string) bool {
	for _, s := range subs {
		if strings.HasPrefix(f, s) {
			return false
		}
	}
	return true
}

func isTestFile(f string) bool { return strings.HasSuffix(f, "_test.go") }

func isNonTestGo(f string) bool {
	return strings.HasSuffix(f, ".go") && !isTestFile(f) && !strings.Contains("/"+f, "/testdata/")
}

// Static runs stage 1 over a repository's first-parent history and returns the candidates, oldest first, with the
// count of commits read.
func Static(r Repo) ([]Candidate, int, error) {
	subs, err := subModules(r)
	if err != nil {
		return nil, 0, err
	}
	log, err := git(r.Path, "log", "--first-parent", "--format=%H%x00%P%x00%s", r.Frozen)
	if err != nil {
		return nil, 0, err
	}
	var out []Candidate
	n := 0
	for _, line := range strings.Split(strings.TrimSpace(log), "\n") {
		f := strings.SplitN(line, "\x00", 3)
		if len(f) != 3 {
			continue
		}
		n++
		commit, parents, subject := f[0], strings.Fields(f[1]), f[2]
		if len(parents) != 1 || strings.HasPrefix(subject, "Revert") || strings.HasPrefix(subject, "Merge") { // rule 1
			continue
		}
		c, ok, err := check(r, subs, commit, parents[0], subject)
		if err != nil {
			return nil, n, fmt.Errorf("%s %s: %w", r.Name, commit[:8], err)
		}
		if ok {
			out = append(out, c)
		}
	}
	// git log is newest first; return oldest first.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, n, nil
}

var generated = regexp.MustCompile(`(?m)^// Code generated .* DO NOT EDIT\.$`)

// check applies rules 2–7 to one commit.
func check(r Repo, subs []string, commit, parent, subject string) (Candidate, bool, error) {
	ns, err := git(r.Path, "diff", "--numstat", "--no-renames", parent, commit)
	if err != nil {
		return Candidate{}, false, err
	}
	c := Candidate{Repo: r.Name, Commit: commit, Base: parent, Subject: subject}
	var nonTest, testFiles []string
	for _, l := range strings.Split(strings.TrimSpace(ns), "\n") {
		fs := strings.Fields(l)
		if len(fs) != 3 {
			continue
		}
		add, err1 := strconv.Atoi(fs[0])
		del, err2 := strconv.Atoi(fs[1])
		if err1 != nil || err2 != nil {
			continue // binary
		}
		file := fs[2]
		c.Why.DiffLines += add + del
		switch {
		case !inRoot(file, subs):
			if strings.HasSuffix(file, ".go") {
				return c, false, nil // touches Go outside the root module
			}
		case isTestFile(file):
			testFiles = append(testFiles, file)
		case isNonTestGo(file):
			nonTest = append(nonTest, file)
			c.Why.NonTestLines += add + del
		}
	}
	c.Why.NonTestFiles = len(nonTest)
	if len(nonTest) < 1 || len(nonTest) > maxNonTestFiles || c.Why.NonTestLines < 1 ||
		c.Why.NonTestLines > maxNonTestLines || c.Why.DiffLines > maxDiffLines || len(testFiles) == 0 { // rules 2, 3, 5
		return c, false, nil
	}
	for _, f := range append(append([]string{}, nonTest...), testFiles...) {
		src, err := git(r.Path, "show", commit+":"+f)
		if err != nil {
			return c, false, nil // deleted at the commit
		}
		if generated.MatchString(src) || strings.Contains(src, "//go:build") || strings.Contains(src, "\nimport \"C\"") { // rules 2, 6
			return c, false, nil
		}
		if !isTestFile(f) {
			lines := strings.Count(src, "\n")
			if lines > maxFileLines { // rule 4
				return c, false, nil
			}
			c.Why.LargestFileLen = max(c.Why.LargestFileLen, lines)
		}
	}
	tests, pkgs, files, err := namedTests(r, parent, commit, testFiles)
	if err != nil || len(tests) == 0 { // rule 5
		return c, false, err
	}
	for _, p := range pkgs { // rule 7
		big, err := bigTestdata(r, commit, p)
		if err != nil || big {
			return c, false, err
		}
	}
	c.Tests, c.Packages, c.TestFiles = tests, pkgs, files
	return c, true, nil
}

// namedTests finds the top-level TestXxx(t *testing.T) functions the commit adds or changes: those whose span in the
// commit's file contains a changed line, or the position of a deletion.
func namedTests(r Repo, parent, commit string, testFiles []string) (tests, pkgs, files []string, err error) {
	seenPkg := map[string]bool{}
	for _, f := range testFiles {
		diff, err := git(r.Path, "diff", "-U0", "--no-renames", parent, commit, "--", f)
		if err != nil {
			return nil, nil, nil, err
		}
		changed := changedLines(diff)
		src, err := git(r.Path, "show", commit+":"+f)
		if err != nil {
			continue // the test file was deleted
		}
		fset := token.NewFileSet()
		af, err := parser.ParseFile(fset, f, src, 0)
		if err != nil {
			continue
		}
		found := false
		for _, d := range af.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Recv != nil || !isTestFunc(fd) {
				continue
			}
			from, to := fset.Position(fd.Pos()).Line, fset.Position(fd.End()).Line
			for _, l := range changed {
				if l >= from && l <= to {
					tests = append(tests, fd.Name.Name)
					found = true
					break
				}
			}
		}
		if found {
			files = append(files, f)
			p := "./" + path.Dir(f)
			if path.Dir(f) == "." {
				p = "."
			}
			if !seenPkg[p] {
				seenPkg[p] = true
				pkgs = append(pkgs, p)
			}
		}
	}
	return tests, pkgs, files, nil
}

func isTestFunc(fd *ast.FuncDecl) bool {
	name := fd.Name.Name
	if !strings.HasPrefix(name, "Test") || name == "TestMain" || fd.Type.Params == nil || len(fd.Type.Params.List) != 1 {
		return false
	}
	if len(name) > 4 {
		if r := name[4]; r >= 'a' && r <= 'z' {
			return false
		}
	}
	star, ok := fd.Type.Params.List[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "T"
}

var hunk = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@`)

// changedLines is the new-file lines a -U0 diff adds, plus the new-file position of each pure deletion.
func changedLines(diff string) []int {
	var out []int
	sc := bufio.NewScanner(strings.NewReader(diff))
	for sc.Scan() {
		m := hunk.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		start, _ := strconv.Atoi(m[1])
		count := 1
		if m[2] != "" {
			count, _ = strconv.Atoi(m[2])
		}
		if count == 0 {
			out = append(out, start, start+1) // a deletion after line start
			continue
		}
		for i := 0; i < count; i++ {
			out = append(out, start+i)
		}
	}
	return out
}

// bigTestdata reports whether the package's testdata at the commit holds a file over the size limit.
func bigTestdata(r Repo, commit, pkg string) (bool, error) {
	dir := strings.TrimPrefix(path.Join(strings.TrimPrefix(pkg, "./"), "testdata"), "./")
	out, err := git(r.Path, "ls-tree", "-r", "-l", commit, "--", dir)
	if err != nil {
		return false, err
	}
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		fs := strings.Fields(l)
		if len(fs) >= 4 {
			if size, err := strconv.Atoi(fs[3]); err == nil && size > maxTestdataSize {
				return true, nil
			}
		}
	}
	return false, nil
}
