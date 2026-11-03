package loop

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/townsendmerino/tack/internal/perms"
	"github.com/townsendmerino/tack/internal/tools"
)

// What a turn did to the tests (DESIGN §6.3): done's answer ends with a report of the test files it deleted, the test
// functions it removed, the skips it added, its edits to tests the task names, the settings it loosened and a last test
// run that ran nothing. The report shows and never refuses: a task may rightly change a test. It reads the turn's own
// edits and commands, so it is a report of what they say and not a boundary: a script that the command only names, a
// find -delete, a change a shell variable hides, are not in it.

// maxTestNotes is how many notes a report lists before it says how many more there were.
const maxTestNotes = 8

// maxTestNoteLen is how many bytes of a note a report keeps; a note quotes paths and commands.
const maxTestNoteLen = 200

// testNotesOpen begins the report. DoneNotes splits on it, and the answers before it are tack's own constants.
const testNotesOpen = noteOpen + "what this turn did to the tests:\n"

// DoneNotes splits done's answer into the answer itself and its report's notes, nil when it has none.
func DoneNotes(result string) (string, []string) {
	answer, report, ok := strings.Cut(result, testNotesOpen)
	if !ok {
		return result, nil
	}
	var notes []string
	for _, line := range strings.Split(strings.TrimSuffix(report, "\n]"), "\n") {
		notes = append(notes, strings.TrimPrefix(line, "- "))
	}
	return answer, notes
}

// testChanges is a turn's record for the report. Edits and writes are read from the workspace at the end, as each file's
// net change, so a test the turn added and removed again is no change; commands are read as they run.
type testChanges struct {
	ws      *tools.Workspace // nil for a session with none
	mark    int              // the workspace's changes when the turn began
	task    string           // the turn's text
	notes   []string
	deleted map[string]int // paths the turn's commands deleted, with the workspace's changes at that moment
	wide    bool           // a go test has run with nothing narrowing it
	narrow  string         // the flag narrowing the latest go test, when an earlier one was wide
}

func newTestChanges(task string, ws *tools.Workspace) testChanges {
	c := testChanges{ws: ws, task: task}
	if ws != nil {
		c.mark = ws.ChangeMark()
	}
	return c
}

// bash records what one command line, as eng parses it, did to the tests. failed says the line exited non-zero; a line of
// one command then did nothing, and a longer one may have done any part of it.
func (c *testChanges) bash(eng *perms.Engine, command string, failed bool) {
	if eng == nil {
		return
	}
	cmds := eng.Analyze(command)
	for _, s := range cmds {
		args, name := s.Args, commandName(s)
		if name == "go" {
			if sub, rest := goSubcommand(s); sub == "test" {
				c.goTest(rest)
			}
			continue
		}
		if failed && len(cmds) == 1 {
			continue
		}
		if name == "git" {
			name, args = gitSubcommand(args)
			name = "git " + name
		}
		switch name {
		case "rm", "unlink", "git rm":
			c.deletes(eng.Root, args)
		case "mv", "git mv":
			c.moves(eng.Root, args)
		case "sed", "perl", "awk", "gawk", "ruby", "python", "python3", "ed":
			if namesTestFile(args, command) && scriptAddsSkip(args) {
				c.note("added a skip through " + name)
			}
		case "echo", "printf", "cat", "tee":
			if (name == "tee" && namesTestFile(args, "")) || redirectsToTestFile(command) {
				if skipRe.MatchString(command) {
					c.note("added a skip through " + name)
				}
			}
		}
	}
}

func (c *testChanges) note(s string) {
	for _, n := range c.notes {
		if n == s {
			return
		}
	}
	c.notes = append(c.notes, s)
}

// goTest steps the narrowing: only a run narrower than an earlier one, with no wider one after it, is a loosened
// setting. Narrowing to debug one test and then running them all says nothing.
func (c *testChanges) goTest(args []string) {
	flag := ""
	for _, a := range args {
		name, _, _ := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(a, "-"), "-"), "=")
		switch strings.TrimPrefix(name, "test.") {
		case "run", "skip", "short":
			if strings.HasPrefix(a, "-") {
				flag = "-" + strings.TrimPrefix(name, "test.")
			}
		}
	}
	switch {
	case flag == "":
		c.wide, c.narrow = true, ""
	case c.wide:
		c.narrow = flag
	}
}

func (c *testChanges) deletes(root string, args []string) {
	recursive := false
	for _, a := range args {
		if a == "--recursive" || (strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.ContainsAny(a, "rR")) {
			recursive = true
		}
	}
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		p, ok := inRepo(root, a)
		if ok && (isTestFile(p) || (recursive && isTestDir(p))) {
			if c.deleted == nil {
				c.deleted = map[string]int{}
			}
			if c.ws != nil {
				c.deleted[p] = c.ws.ChangeMark()
			} else {
				c.deleted[p] = c.mark
			}
			if !c.scratch(p) {
				c.note("deleted " + p)
			}
		}
	}
}

// scratch reports whether the turn itself created the file at p: deleting it again is no change to the tests.
func (c *testChanges) scratch(p string) bool {
	if c.ws == nil {
		return false
	}
	for _, f := range c.ws.ChangesSince(c.mark) {
		if filepath.Clean(f.Path) == p {
			return f.Before == ""
		}
	}
	return false
}

// deletedAt is when the turn's commands deleted p, or a directory around it.
func (c *testChanges) deletedAt(p string) (int, bool) {
	for {
		if at, ok := c.deleted[p]; ok {
			return at, true
		}
		parent := filepath.Dir(p)
		if parent == p || parent == "." {
			return 0, false
		}
		p = parent
	}
}

// moves notes a test file moved to a name that is not one: out of the repository under any name, or inside it to a path
// that is not a test's. Into a directory that exists, the file keeps its own name there.
func (c *testChanges) moves(root string, args []string) {
	var paths []string
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			paths = append(paths, a)
		}
	}
	if len(paths) < 2 {
		return
	}
	dest := paths[len(paths)-1]
	to, inside := inRepo(root, dest)
	st, err := os.Stat(filepath.Join(root, to))
	intoDir := inside && err == nil && st.IsDir()
	for _, a := range paths[:len(paths)-1] {
		p, ok := inRepo(root, a)
		if !ok || !isTestFile(p) {
			continue
		}
		target := to
		if intoDir {
			target = filepath.Join(to, filepath.Base(p))
		}
		if !isTestFile(target) { // out of the repository, target is ""
			c.note("moved " + p + " to " + dest)
		}
	}
}

// report is the text done's answer ends with, "" when there is nothing to say. last is the turn's latest verification and
// change the call number of its latest change: a run that ran nothing before that change is not what done is claiming from.
func (c *testChanges) report(last *verification, change int) string {
	if c.ws != nil {
		for _, f := range c.ws.ChangesSince(c.mark) {
			p := filepath.Clean(f.Path)
			at, deleted := c.deletedAt(p)
			if !deleted {
				c.fileNotes(f)
				continue
			}
			// Only what the turn wrote after deleting it: the deletion already says what went before.
			for _, g := range c.ws.ChangesSince(at) {
				if filepath.Clean(g.Path) == p {
					c.fileNotes(g)
				}
			}
		}
	}
	notes := c.notes
	if c.narrow != "" {
		notes = append(notes, "narrowed the tests with "+c.narrow+" after running them without it")
	}
	if last != nil && last.nothing && last.exit == 0 && last.call > change {
		notes = append(notes, "`"+last.command+"` ran nothing, so it verified nothing")
	}
	if len(notes) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(testNotesOpen)
	for i, n := range notes {
		if i == maxTestNotes {
			fmt.Fprintf(&b, "- and %d more\n", len(notes)-maxTestNotes)
			break
		}
		n = strings.ReplaceAll(escapeControl(n), "\n", " ")
		if len(n) > maxTestNoteLen {
			n = strings.ToValidUTF8(n[:maxTestNoteLen], "") + "…"
		}
		b.WriteString("- " + n + "\n")
	}
	b.WriteString("]")
	return b.String()
}

// fileNotes notes one file's net change over the turn.
func (c *testChanges) fileNotes(f tools.FileChange) {
	if !isTestFile(f.Path) {
		for _, n := range loweredCoverage(f) {
			c.note(n)
		}
		return
	}
	f.Before, f.After = liveCode(f.Path, f.Before), liveCode(f.Path, f.After)
	before, order := testBodies(f.Before)
	after, _ := testBodies(f.After)
	for _, key := range order {
		if _, ok := after[key]; !ok {
			c.note("removed " + testName(key) + " from " + f.Path)
		}
	}
	if len(skipRe.FindAllString(f.After, -1)) > len(skipRe.FindAllString(f.Before, -1)) {
		c.note("added a skip to " + f.Path)
	}
	if f.Before == "" {
		return
	}
	if strings.Contains(c.task, filepath.Base(f.Path)) {
		c.note("edited " + f.Path + ", a test file the task names")
	}
	for _, key := range order {
		if body, ok := after[key]; ok && body != before[key] && taskNames(c.task, testName(key)) {
			c.note("edited " + testName(key) + " in " + f.Path + ", a test the task names")
		}
	}
}

// taskNames reports whether the task holds name as a word; a JavaScript test's title can start or end with anything, so
// one that doesn't start and end like a word is looked for as it stands.
func taskNames(task, name string) bool {
	if wordEnds.MatchString(name) {
		return regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`).MatchString(task)
	}
	return strings.Contains(task, name)
}

var wordEnds = regexp.MustCompile(`^\w(.*\w)?$`)

// Block comments and Python's triple-quoted strings, which hold no live test: commenting one out this way removes it.
var (
	blockComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
	tripleQuoted = regexp.MustCompile(`(?s)""".*?"""|'''.*?'''`)
)

// liveCode is src without what can't run, by the file's language.
func liveCode(path, src string) string {
	if strings.HasSuffix(path, ".py") {
		return tripleQuoted.ReplaceAllString(src, "")
	}
	return blockComment.ReplaceAllString(src, "")
}

// testFuncRe matches the start of a test function in Go, Python, JavaScript and TypeScript, or Rust; a commented-out one
// does not match, so commenting a test out removes it.
var testFuncRe = regexp.MustCompile("(?m)^(?:func ((?:Test|Fuzz)\\w*)\\(|\\s*(?:async\\s+)?def (test\\w*)\\(|\\s*(?:it|test)\\(\\s*['\"`]([^'\"`\\n]+)|\\s*#\\[test\\](?:\\s*#\\[[^\\]\\n]*\\])*\\s*(?:pub\\s+)?(?:async\\s+)?fn (\\w+))")

// testBodies is the file's test functions by key, each with its text (testEnd), and the keys in file order. A key is the
// test's name (testName), with its count when an earlier test has the same name, as JavaScript's in two describes can.
func testBodies(src string) (map[string]string, []string) {
	bodies, seen := map[string]string{}, map[string]int{}
	var order []string
	ms := testFuncRe.FindAllStringSubmatchIndex(src, -1)
	for i, m := range ms {
		name, at, group := "", 0, 0
		for g := 1; g < len(m)/2; g++ {
			if m[2*g] >= 0 {
				name, at, group = src[m[2*g]:m[2*g+1]], m[2*g+1], g
			}
		}
		limit := len(src)
		if i+1 < len(ms) {
			limit = ms[i+1][0]
		}
		end := testEnd(src[:limit], at, group)
		key := name
		if n := seen[name]; n > 0 {
			key = fmt.Sprintf("%s\x00%d", name, n)
		}
		seen[name]++
		order = append(order, key)
		bodies[key] = src[m[0]:end]
	}
	return bodies, order
}

// testName is the name in a key of testBodies.
func testName(key string) string {
	name, _, _ := strings.Cut(key, "\x00")
	return name
}

// testEnd is where a test ends in src, given the end of its name and which group of testFuncRe matched it: in Python at the
// next line indented no more than its def, apart from the ) that closes a long signature; in JavaScript where the call
// closes; elsewhere where the braces opened after the name close. A bracket in a string can throw the count off, so a
// caller's src stops at the next test.
func testEnd(src string, at, group int) int {
	switch group {
	case 2:
		start := strings.LastIndexByte(src[:at], '\n') + 1
		indent := len(src[start:]) - len(strings.TrimLeft(src[start:], " \t"))
		for i := at; ; {
			nl := strings.IndexByte(src[i:], '\n')
			if nl < 0 {
				return len(src)
			}
			i += nl + 1
			line := src[i:]
			if eol := strings.IndexByte(line, '\n'); eol >= 0 {
				line = line[:eol]
			}
			if trimmed := strings.TrimLeft(line, " \t"); trimmed != "" && trimmed[0] != ')' && len(line)-len(trimmed) <= indent {
				return i
			}
		}
	case 3:
		return closes(src, at, '(', ')', 1)
	}
	return closes(src, at, '{', '}', 0)
}

// closes is just past where the bracket closes, counting from at with depth already open, or the end of src.
func closes(src string, at int, open, shut byte, depth int) int {
	for i := at; i < len(src); i++ {
		switch src[i] {
		case open:
			depth++
		case shut:
			if depth--; depth <= 0 {
				return i + 1
			}
		}
	}
	return len(src)
}

// skipRe matches a skip in a test file: Go's, Python's, JavaScript's and Rust's, and a build constraint that drops a Go
// test file.
var skipRe = regexp.MustCompile(`\b[tbf]\.Skip(?:Now|f)?\(|@pytest\.mark\.(?:skip|skipif|xfail)\b|\bpytest\.skip\(|@unittest\.skip|\.skipTest\(|\.skip\(|\bx(?:it|describe|test)\(|#\[ignore\b|//go:build ignore\b`)

// scriptAddsSkip reports whether one of an editor's arguments puts a skip into what it edits: a substitution with more
// skips on its right than its left, or a script that is no substitution and no line address alone.
func scriptAddsSkip(args []string) bool {
	for _, a := range args {
		if !skipRe.MatchString(a) {
			continue
		}
		if m := substRe.FindStringSubmatch(a); m != nil {
			parts := strings.Split(m[2], m[1])
			if len(parts) >= 2 && len(skipRe.FindAllString(parts[1], -1)) > len(skipRe.FindAllString(parts[0], -1)) {
				return true
			}
			continue
		}
		if !addressRe.MatchString(a) {
			return true
		}
	}
	return false
}

var (
	substRe   = regexp.MustCompile(`^\s*\S*?s([^\w\s])(.*)$`)
	addressRe = regexp.MustCompile(`^\s*/.*/\s*[dp]\s*$`)
)

// redirectRe matches a shell redirection's target as the command line writes it.
var redirectRe = regexp.MustCompile(`(?:^|[^-=<>])>>?\s*['"]?([^\s'";&|<>]+)`)

func redirectsToTestFile(command string) bool {
	for _, m := range redirectRe.FindAllStringSubmatch(command, -1) {
		if isTestFile(filepath.Clean(m[1])) {
			return true
		}
	}
	return false
}

// namesTestFile reports whether args name a test file, or command redirects to one.
func namesTestFile(args []string, command string) bool {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") && isTestFile(filepath.Clean(a)) {
			return true
		}
	}
	return redirectsToTestFile(command)
}

// isTestFile reports whether the path, relative to the repository, is a test by its name or by a directory it is in.
func isTestFile(p string) bool {
	dir, base := filepath.Split(filepath.ToSlash(p))
	switch {
	case strings.HasSuffix(base, "_test.go"), strings.HasSuffix(base, "_test.py"),
		strings.HasPrefix(base, "test_") && strings.HasSuffix(base, ".py"),
		strings.Contains(base, ".test."), strings.Contains(base, ".spec."):
		return true
	}
	for _, seg := range strings.Split(dir, "/") {
		if isTestDirName(seg) {
			return true
		}
	}
	return false
}

// isTestDir reports whether the path is, or is inside, a test directory.
func isTestDir(p string) bool {
	return isTestDirName(filepath.Base(p)) || isTestFile(p)
}

func isTestDirName(s string) bool {
	return s == "tests" || s == "test" || s == "__tests__"
}

// inRepo is the path as written made relative to root, and whether it is inside it.
func inRepo(root, p string) (string, bool) {
	if filepath.IsAbs(p) {
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return "", false
		}
		p = rel
	}
	p = filepath.Clean(p)
	if p == ".." || strings.HasPrefix(p, ".."+string(os.PathSeparator)) {
		return "", false
	}
	return p, true
}

// settingRe matches a key and a number, as config files write them.
var settingRe = regexp.MustCompile(`([\w.-]+)["']?\s*[:=]\s*["']?(\d+(?:\.\d+)?)`)

// loweredCoverage notes each coverage setting the change lowered. Source files are not read: a number there is a
// constant of the code, not a gate.
func loweredCoverage(f tools.FileChange) []string {
	switch filepath.Ext(f.Path) {
	case ".go", ".py", ".rs", ".ts", ".tsx", ".jsx", ".c", ".h", ".md":
		return nil
	}
	before := map[string][]string{} // a key's values in file order: sections can each have their own
	for _, m := range settingRe.FindAllStringSubmatch(f.Before, -1) {
		if isCoverageKey(m[1]) {
			before[m[1]] = append(before[m[1]], m[2])
		}
	}
	seen := map[string]int{}
	var notes []string
	for _, m := range settingRe.FindAllStringSubmatch(f.After, -1) {
		i := seen[m[1]]
		seen[m[1]]++
		if i >= len(before[m[1]]) {
			continue
		}
		old := before[m[1]][i]
		var o, n float64
		fmt.Sscan(old, &o)
		fmt.Sscan(m[2], &n)
		if n < o {
			notes = append(notes, fmt.Sprintf("lowered the coverage gate in %s from %s to %s", f.Path, old, m[2]))
		}
	}
	return notes
}

func isCoverageKey(key string) bool {
	k := strings.ToLower(key)
	for _, s := range []string{"fail_under", "fail-under", "failunder", "coverage", "-cov", "_cov"} {
		if strings.Contains(k, s) {
			return true
		}
	}
	return strings.HasPrefix(k, "cov")
}

// commandName is the command as written, without its directory.
func commandName(s perms.Simple) string {
	if s.Builtin != "" {
		return s.Builtin
	}
	return filepath.Base(s.Name)
}

// gitSubcommand is git's subcommand and the arguments after it, past git's own -C and -c.
func gitSubcommand(args []string) (string, []string) {
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-C" || args[i] == "-c":
			i++
		case !strings.HasPrefix(args[i], "-"):
			return args[i], args[i+1:]
		}
	}
	return "", nil
}

// ranNothing reports whether go test's output says every package it looked at ran no test. go test reports package by
// package, so a run where another one ran, or failed, is not one.
func ranNothing(output string) bool {
	saw := false
	for _, line := range strings.Split(output, "\n") {
		switch {
		case strings.Contains(line, "[no tests to run]"), strings.Contains(line, "[no test files]"):
			saw = true
		case strings.HasPrefix(line, "ok  "), strings.HasPrefix(line, "FAIL"):
			return false
		}
	}
	return saw
}
