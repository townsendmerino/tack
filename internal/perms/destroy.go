package perms

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// What asks in every mode (DESIGN §6.6): commands that destroy work inside the workspace, which the sandbox lets them
// reach, and those that change what the owner's checks and standing orders do. A rule or a session's allow never
// covers them, and auto does not skip them. Everything here reads parsed words, never the command's text, so a commit
// message that quotes one matches nothing.
//
// The rules decide when to ask; they don't bound what a program can do. A script, or a program that deletes files on
// its own, is not visible here. §6.8's snapshots are what make such a loss undoable.

// unknownWord stands for a word that expands at run time: it matches no rule, and asks where a rule would read it.
const unknownWord = ""

// scan is where a command is being read.
type scan struct {
	dir   string    // where it runs
	depth int       // the programs it is inside
	lost  bool      // dir can't be read: a cd moved it where the parse can't follow (walkDirs)
	rec   *Approval // when set, notes what it finds
}

func (sc scan) in() scan {
	sc.depth++
	return sc
}

// unseenEnd ends every reason for what the parse can't read: when it cannot see exactly what will run, it asks (DESIGN §6.6).
const unseenEnd = ", so what will run can't be seen before it does"

// unseen is the reason for code the parse can't read at all: anything may run.
func unseen(what string) string { return what + unseenEnd }

// unseenIn is the reason for a part it can't read: what a program gets, a redirection's target, a variable's value.
func unseenIn(scope, what string) string { return scope + ": " + what + unseenEnd }

// runsTextEnd ends what a reason says of code run from text, which may be anything.
const runsTextEnd = " runs code from text"

// hole stands in Approval for a word that expands: it matches any number of words.
const hole = "\x00"

// holeOne is a hole that matches at most one word.
const holeOne = "\x02"

// Statement is a command whose text is all there, and whether the parse asked about it.
type Statement struct {
	Text  []string `json:"text"`
	Asked bool     `json:"asked"`
}

// Approval is what a user's yes to a command covers when the command runs (DESIGN §6.6): each command the parse asked about, for any
// reason, with a hole where it expands. It doesn't go by what a reason says, since two statements can say the same. A command the
// parse had nothing to say about is what it is, so no hole covers it, and what only shows when it runs still stops it.
type Approval struct {
	Anything bool       `json:"anything"` // it runs code from text, so anything may run: eval, source, trap
	Commands [][]string `json:"commands"` // the commands it asked about, with holes
	// Order is the commands with nothing that expands, in order. What runs can't say which statement it is, so the n-th run of a text is taken
	// to be its n-th statement here, and the last for any more.
	Order []Statement `json:"order"`
	// Redirects are the targets it asked about, absolute where it can tell, or as written after a cd it lost, or hole for one that
	// expands. LiteralRedirects are those it didn't, absolute.
	Redirects        []string   `json:"redirects"`
	LiteralRedirects []string   `json:"literal_redirects"`
	Vars             []string   `json:"vars"`    // the variables it asked about
	AnyVar           bool       `json:"any_var"` // it asked about what may set any variable: a word that names it expands
	Fed              [][]string `json:"fed"`     // the shells it asked about being handed code
}

// ApprovalFor is what a user's yes to command covers.
func (e *Engine) ApprovalFor(command string) Approval {
	var a Approval
	f, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil {
		return a
	}
	e.scanAsks(f, scan{dir: e.startDir(), rec: &a})
	for _, name := range sortedKeys(e.SessionEnv) {
		if v := e.SessionEnv[name]; v != os.Getenv(name) && HooksOff(name, v) != "" && runsProgramWith(f, name) {
			a.Vars = append(a.Vars, name)
		}
	}
	return a
}

// Covers is CoversAt for a text's first run.
func (a Approval) Covers(args []string) bool { return a.CoversAt(args, 0) }

// CoversAt reports whether a yes covers the n-th run (from 0) of a program with args, as it runs: args[0] as written, and its arguments
// expanded. A text written out in the command goes by its statement: covered if the parse asked about it, and otherwise what it is, so no hole
// covers it. Anything else may be what a hole became.
func (a Approval) CoversAt(args []string, n int) bool {
	var same []Statement
	for _, s := range a.Order {
		if slices.Equal(s.Text, args) {
			same = append(same, s)
		}
	}
	if len(same) > 0 {
		return same[min(n, len(same)-1)].Asked
	}
	return a.Anything || slices.ContainsFunc(a.Commands, func(c []string) bool { return matchHoles(c, args) })
}

// CoversRedirect reports whether a yes covers a redirection to target, as it expanded, whose absolute path is abs.
func (a Approval) CoversRedirect(target, abs string) bool {
	if slices.Contains(a.LiteralRedirects, abs) {
		return false
	}
	return a.Anything || slices.Contains(a.Redirects, abs) || slices.Contains(a.Redirects, filepath.Clean(target)) || slices.Contains(a.Redirects, hole)
}

// ShowsIn reports whether the user was shown word in the very command that runs as args, as a flag that runs another program must have
// been: a word of a command the parse asked about that matches args, not of another statement.
func (a Approval) ShowsIn(args []string, word string) bool {
	return slices.ContainsFunc(a.Commands, func(c []string) bool { return matchHoles(c, args) && slices.Contains(c, word) })
}

// Saw reports whether the user was shown exactly this command, whatever the parse had to say about it.
func (a Approval) Saw(args []string) bool {
	return slices.ContainsFunc(a.Order, func(s Statement) bool { return slices.Equal(s.Text, args) })
}

// CoversVar reports whether a yes covers a variable's value.
func (a Approval) CoversVar(name string) bool {
	return a.Anything || a.AnyVar || slices.Contains(a.Vars, name)
}

// CoversFed reports whether a yes covers a shell handed code: only one it asked about for that, so a yes to the same command for another
// reason doesn't.
func (a Approval) CoversFed(args []string) bool {
	return a.Anything || slices.ContainsFunc(a.Fed, func(c []string) bool { return matchHoles(c, args) })
}

// matchHoles reports whether words match pattern, a hole taking any number of them.
func matchHoles(pattern, words []string) bool {
	if len(pattern) == 0 {
		return len(words) == 0
	}
	if pattern[0] == holeOne {
		return matchHoles(pattern[1:], words) || (len(words) > 0 && matchHoles(pattern[1:], words[1:]))
	}
	if pattern[0] == hole {
		for i := 0; i <= len(words); i++ {
			if matchHoles(pattern[1:], words[i:]) {
				return true
			}
		}
		return false
	}
	return len(words) > 0 && pattern[0] == words[0] && matchHoles(pattern[1:], words[1:])
}

// template is a simple command as the program it hands on runs: holes where it expands, and without command, builtin and exec, which the
// interpreter runs itself. whole is true if nothing in it expands. A word that expands right after those is a flag or the program, so it
// takes at most one word, or it would match any command that ends the same.
func template(words []*syntax.Word) (argv []string, whole bool) {
	argv, lit := args(words)
	whole = !slices.Contains(lit, false)
	for i := range argv {
		if !lit[i] {
			argv[i] = hole
		}
	}
	prefixed := false
	for len(argv) > 1 && (argv[0] == "command" || argv[0] == "builtin" || argv[0] == "exec") {
		isExec := argv[0] == "exec"
		prefixed = true
		argv = argv[1:]
		for len(argv) > 1 && strings.HasPrefix(argv[0], "-") {
			if isExec && argv[0] == "-a" && len(argv) > 2 {
				argv = argv[1:] // -a takes the next word
			}
			argv = argv[1:]
		}
	}
	if prefixed && len(argv) > 1 && argv[0] == hole {
		argv[0] = holeOne
	}
	return argv, whole
}

// record notes one simple command and what the parse said about it. The interpreter runs command, builtin and exec itself, so what
// it hands on is the command under them.
func (a *Approval) record(words []*syntax.Word, why []string) {
	argv, whole := template(words)
	switch {
	case slices.ContainsFunc(why, func(r string) bool { return strings.HasSuffix(r, runsTextEnd+unseenEnd) }):
		a.Anything = true
		if argv[0] == "eval" {
			a.recordText(evalText(words))
		}
	case len(why) > 0:
		a.Commands = append(a.Commands, argv)
	}
	if whole {
		a.Order = append(a.Order, Statement{argv, len(why) > 0})
	}
}

// evalText is the code an eval runs as the user was shown it, expansions and all, so the rest of it counts as shown.
func evalText(words []*syntax.Word) string {
	var code []string
	seen := false
	for _, w := range words {
		a, ok := literal(w)
		switch {
		case !seen:
			seen = ok && a == "eval"
		case ok:
			code = append(code, a)
		default:
			code = append(code, strings.Trim(source(w), `"`))
		}
	}
	return strings.Join(code, " ")
}

// recordText notes the commands in code from text that the user was shown, as commands the parse asked about.
func (a *Approval) recordText(code string) {
	f, err := syntax.NewParser().Parse(strings.NewReader(code), "")
	if err != nil {
		return
	}
	syntax.Walk(f, func(n syntax.Node) bool {
		if c, ok := n.(*syntax.CallExpr); ok && len(c.Args) > 0 {
			a.record(c.Args, []string{"it runs from text"})
		}
		return true
	})
}

// recordRedirect notes one output redirection and what the parse said about it.
func (a *Approval) recordRedirect(r *syntax.Redirect, why []string, sc scan) {
	target, ok := literal(r.Word)
	if !ok {
		a.Redirects = append(a.Redirects, hole)
		return
	}
	if !filepath.IsAbs(target) && !sc.lost {
		target = filepath.Join(sc.dir, target)
	}
	if len(why) > 0 {
		a.Redirects = append(a.Redirects, filepath.Clean(target))
	} else {
		a.LiteralRedirects = append(a.LiteralRedirects, filepath.Clean(target))
	}
}

// AlwaysAsks returns why s asks in every mode, one reason per cause, or nil. dir is where s runs, for its relative paths.
// internal/shell's exec check passes it what a command expanded to, as a second layer behind the parse.
func (e *Engine) AlwaysAsks(s Simple, dir string) []string {
	if s.Program == "" {
		return nil
	}
	args := make([]string, len(s.Args))
	for i, a := range s.Args {
		if s.Literal[i] {
			args[i] = a
		}
	}
	var why []string
	for _, name := range []string{filepath.Base(s.Program), filepath.Base(s.Name)} {
		if name != "." {
			why = appendNew(why, e.wordsAsk(name, args, scan{dir: dir})...)
		}
	}
	return why
}

// AlwaysAsksIn returns why command asks in every mode, as Decide reads it before it runs.
func (e *Engine) AlwaysAsksIn(command string) []string {
	_, _, always := e.checkShell(command)
	return always
}

// startDir is where the session's next command starts.
func (e *Engine) startDir() string {
	if e.SessionDir != "" {
		return e.SessionDir
	}
	return e.Root
}

// walkDirs visits every node under node in order, with where it runs: dir, moved by each cd it can place. lost is true after a cd it
// can't, or a change of CDPATH: paths from there on can't be read. A subshell's or a substitution's cd ends with it.
func walkDirs(node syntax.Node, dir string, visit func(n syntax.Node, dir string, lost bool)) {
	type frame struct {
		node syntax.Node
		dir  string
		lost bool
	}
	lost := false
	var stack []frame
	syntax.Walk(node, func(n syntax.Node) bool {
		if n == nil {
			top := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			switch top.node.(type) {
			case *syntax.Subshell, *syntax.CmdSubst:
				dir, lost = top.dir, top.lost
			}
			return true
		}
		path := make([]syntax.Node, len(stack))
		for i, f := range stack {
			path[i] = f.node
		}
		stack = append(stack, frame{n, dir, lost})
		visit(n, dir, lost)
		switch n := n.(type) {
		case *syntax.Assign:
			name, _, dynamic := declared(n)
			lost = lost || dynamic || name == "CDPATH"
		case *syntax.WordIter:
			lost = lost || (n.Name != nil && n.Name.Value == "CDPATH")
		case *syntax.ParamExp:
			name, dynamic := expansionSets(n)
			lost = lost || dynamic || name == "CDPATH"
		case *syntax.CallExpr:
			if len(n.Args) == 0 {
				return true
			}
			if slices.Contains(namedVars(n.Args), "CDPATH") || slices.Contains(namedVars(n.Args), unknownWord) {
				lost = true
			}
			if !movesDir(n.Args) {
				return true
			}
			name, _ := literal(n.Args[0])
			to, ok := cdTarget(n.Args, dir)
			if name == "cd" && ok && cdPlaced(path, n) {
				dir = to
			} else {
				lost = true
			}
		}
		return true
	})
}

// movesDir reports whether a simple command may change the shell's directory: cd, pushd or popd, under builtin or command too, or a
// program that expands, which may be any of them.
func movesDir(words []*syntax.Word) bool {
	prefixed := false
	for _, w := range words {
		name, ok := literal(w)
		switch {
		case !ok:
			return true
		case prefixed && strings.HasPrefix(name, "-"):
		case name == "builtin" || name == "command":
			prefixed = true
		case name == "cd" || name == "pushd" || name == "popd":
			return true
		default:
			return false
		}
	}
	return false
}

// cdPlaced reports whether call, under the nodes in path, runs exactly once and in order, so that a cd in it moves what
// follows. A function body, a branch, a loop, a pipeline, a process substitution, the background, a negated statement and the
// right-hand side of || may not; a statement with a redirection opens it before the cd moves; an assignment on it may be CDPATH.
// The right-hand side of && counts only after cds, whose failure stops the chain.
func cdPlaced(path []syntax.Node, call *syntax.CallExpr) bool {
	if len(call.Assigns) > 0 {
		return false
	}
	for i, n := range path {
		switch n := n.(type) {
		case *syntax.FuncDecl, *syntax.IfClause, *syntax.WhileClause, *syntax.ForClause, *syntax.CaseClause, *syntax.CoprocClause, *syntax.ProcSubst:
			return false
		case *syntax.Stmt:
			if n.Negated || n.Background || n.Coprocess || n.Disown || len(n.Redirs) > 0 {
				return false
			}
		case *syntax.BinaryCmd:
			if n.Op == syntax.Pipe || n.Op == syntax.PipeAll {
				return false
			}
			if i+1 < len(path) && path[i+1] == syntax.Node(n.Y) && (n.Op == syntax.OrStmt || !onlyCds(n.X)) {
				return false
			}
		}
	}
	return true
}

// onlyCds reports whether a statement is cds it can place, joined by &&.
func onlyCds(s *syntax.Stmt) bool {
	if s.Negated || s.Background || len(s.Redirs) > 0 {
		return false
	}
	switch c := s.Cmd.(type) {
	case *syntax.BinaryCmd:
		return c.Op == syntax.AndStmt && onlyCds(c.X) && onlyCds(c.Y)
	case *syntax.CallExpr:
		if len(c.Args) > 0 {
			name, ok := literal(c.Args[0])
			_, placed := cdTarget(c.Args, "")
			return ok && name == "cd" && placed
		}
	}
	return false
}

// cdTarget is where a simple command leaves the shell, and whether that can be seen. Anything but cd stays in dir.
func cdTarget(words []*syntax.Word, dir string) (string, bool) {
	if name, ok := literal(words[0]); !ok || name != "cd" {
		return dir, true
	}
	for i, w := range words[1:] {
		t, ok := literal(w)
		switch {
		case !ok || t == "-":
			return dir, false
		case t == "--":
		case strings.HasPrefix(t, "-"):
			if strings.Trim(t[1:], "LPe@") != "" { // a flag cd doesn't know: it fails, and stays
				return dir, false
			}
		case i+2 < len(words): // more than one target: it fails, and stays
			return dir, false
		case filepath.IsAbs(t):
			return filepath.Clean(t), true
		default:
			return filepath.Join(dir, t), true
		}
	}
	return dir, false // to $HOME
}

// scanAsks is AlwaysAsks for every simple command under node, noting what it finds in sc.rec if set. It doesn't need a program to
// resolve: a command that fails to may still be there when it runs.
func (e *Engine) scanAsks(node syntax.Node, sc scan) []string {
	var why []string
	walkDirs(node, sc.dir, func(n syntax.Node, dir string, lost bool) {
		sc := sc
		sc.dir, sc.lost = dir, lost
		switch n := n.(type) {
		case *syntax.Assign:
			asks := assignAsks(n)
			if sc.rec != nil && len(asks) > 0 {
				if name, _, _ := declared(n); name != "" {
					sc.rec.Vars = append(sc.rec.Vars, name)
				} else {
					sc.rec.AnyVar = true
				}
			}
			why = appendNew(why, asks...)
		case *syntax.ParamExp:
			name, dynamic := expansionSets(n)
			switch {
			case dynamic:
				if sc.rec != nil {
					sc.rec.AnyVar = true
				}
				why = appendNew(why, unseen("an expansion sets a variable named when it runs, which may be any"))
			case isHookVar(name):
				if sc.rec != nil {
					sc.rec.Vars = append(sc.rec.Vars, name)
				}
				why = appendNew(why, unseenIn(name, "an expansion sets it"))
			}
		case *syntax.WordIter:
			if n.Name != nil && isHookVar(n.Name.Value) {
				if sc.rec != nil {
					sc.rec.Vars = append(sc.rec.Vars, n.Name.Value)
				}
				why = appendNew(why, unseenIn(n.Name.Value, "a loop sets it to values that can't be read here"))
			}
		case *syntax.BinaryCmd, *syntax.Stmt:
			asks, calls := e.stdinAsks(n, sc)
			if sc.rec != nil {
				for _, c := range calls {
					sc.rec.record(c.Args, asks)
					argv, _ := template(c.Args)
					sc.rec.Fed = append(sc.rec.Fed, argv)
				}
			}
			why = appendNew(why, asks...)
		case *syntax.Redirect:
			if !writes(n) {
				return
			}
			asks := e.redirectAsks(n, sc)
			if sc.rec != nil {
				sc.rec.recordRedirect(n, asks, sc)
			}
			why = appendNew(why, asks...)
		case *syntax.CallExpr:
			if len(n.Args) > 0 {
				asks := e.callAsks(n.Args, sc)
				if sc.rec != nil {
					sc.rec.record(n.Args, asks)
					for _, v := range namedVars(n.Args) {
						if v == unknownWord {
							sc.rec.AnyVar = true
						} else if isHookVar(v) {
							sc.rec.Vars = append(sc.rec.Vars, v)
						}
					}
				}
				why = appendNew(why, asks...)
			}
		}
	})
	return why
}

// callAsks reads one simple command by the name it is written with and the program that name resolves to.
func (e *Engine) callAsks(words []*syntax.Word, sc scan) []string {
	name, ok := literal(words[0])
	if !ok {
		return []string{unseen("the program " + source(words[0]) + " expands when it runs")}
	}
	argv, lit := args(words[1:])
	for i := range argv {
		if !lit[i] {
			argv[i] = unknownWord
		}
	}
	if filepath.Base(name) == "git" {
		// A message in quotes is one word, a value and not a flag. Anywhere else, or unquoted, it may split into anything.
		for _, i := range commitValues(argv) {
			if argv[i] == unknownWord && oneWord(words[i+1]) {
				argv[i] = oneValue
			}
		}
	}
	why := e.programAsks(name, argv, sc)
	for _, v := range namedVars(words) {
		if v == unknownWord {
			why = appendNew(why, unseen(name+" sets a variable named when it runs, which may be any"))
		} else if isHookVar(v) {
			why = appendNew(why, unseenIn(v, name+" sets it to a value that can't be read here"))
		}
	}
	return why
}

// commitValues returns where the values of git commit's own flags are in git's arguments, or nil for another subcommand.
func commitValues(args []string) []int {
	i := 0
	for i < len(args) && strings.HasPrefix(args[i], "-") {
		if slices.Contains(gitValued, args[i]) {
			i++
		}
		i++
	}
	if i >= len(args) || args[i] != "commit" {
		return nil
	}
	var out []int
	for j := i + 1; j+1 < len(args); j++ {
		if gitValuedFlags["commit"].takesNext(args[j]) {
			j++
			out = append(out, j)
		}
	}
	return out
}

// oneValue stands for a word that expands but stays one word.
const oneValue = "\x01"

// oneWord reports whether a word expands to exactly one word: all of it in double quotes, without $@ or $*.
func oneWord(w *syntax.Word) bool {
	if len(w.Parts) != 1 {
		return false
	}
	q, ok := w.Parts[0].(*syntax.DblQuoted)
	if !ok {
		return false
	}
	text := source(w)
	return len(q.Parts) > 0 && !strings.Contains(text, "$@") && !strings.Contains(text, "$*") && !strings.Contains(text, "[@]") && !strings.Contains(text, "{@") && !strings.Contains(text, "{*") && !strings.Contains(text, "[*]")
}

// namedVars returns the variables a builtin sets by name, under builtin or command too, unknownWord for one it can't read: read's, printf
// -v's, and a declaration's, which the parser reads as a plain command under those two. printf's first word may be that -v if it expands.
func namedVars(words []*syntax.Word) []string {
	for len(words) > 0 {
		if name, ok := literal(words[0]); !ok || (name != "builtin" && name != "command") {
			break
		}
		words = words[1:]
		for len(words) > 0 {
			if a, ok := literal(words[0]); !ok || !strings.HasPrefix(a, "-") {
				break
			}
			words = words[1:]
		}
	}
	if len(words) < 2 {
		return nil
	}
	name, ok := literal(words[0])
	if !ok {
		return nil
	}
	var out []string
	switch name {
	case "printf":
		first, lit := literal(words[1])
		switch {
		case !lit:
			out = append(out, unknownWord)
		case first == "-v" && len(words) > 2:
			v, ok := literal(words[2])
			if !ok {
				v = unknownWord
			}
			out = append(out, v)
		}
	case "read":
		for i := 1; i < len(words); i++ {
			a, lit := literal(words[i])
			switch {
			case !lit:
				out = append(out, unknownWord)
			case !strings.HasPrefix(a, "-"):
				out = append(out, a)
			case len(a) > 1 && strings.IndexByte("pdnNtu", a[len(a)-1]) >= 0: // the next word is its value, not a name
				i++
			}
		}
	case "export", "declare", "typeset", "local", "readonly":
		for _, w := range words[1:] {
			a, lit := literal(w)
			if !lit {
				out = append(out, unknownWord)
			} else if !strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "+") {
				v, _, _ := strings.Cut(a, "=")
				out = append(out, v)
			}
		}
	}
	return out
}

// isHookVar reports whether a variable is one that may turn the owner's hooks off.
func isHookVar(name string) bool { return name == "HUSKY" || name == "HUSKY_SKIP_HOOKS" }

// programAsks reads a program by the name it is written with and what that name resolves to from where it runs.
func (e *Engine) programAsks(name string, args []string, sc scan) []string {
	why := e.wordsAsk(filepath.Base(name), args, sc)
	if sc.lost && strings.Contains(name, "/") && !filepath.IsAbs(name) {
		why = appendNew(why, unseenIn(name, "it follows a cd that can't be read"))
	}
	if real, err := e.resolveFrom(name, sc.dir); err == nil && filepath.Base(real) != filepath.Base(name) {
		why = appendNew(why, e.wordsAsk(filepath.Base(real), args, sc)...)
	}
	return why
}

// writes reports whether a redirection writes its target.
func writes(r *syntax.Redirect) bool {
	switch r.Op {
	case syntax.RdrOut, syntax.AppOut, syntax.RdrClob, syntax.AppClob, syntax.RdrAll, syntax.AppAll, syntax.RdrInOut:
		return r.Word != nil
	}
	return false
}

// redirectAsks is PathAsks for a redirection that writes, and asks for one whose target it can't read: it expands, or it is relative
// to a directory it has lost.
func (e *Engine) redirectAsks(r *syntax.Redirect, sc scan) []string {
	target, ok := literal(r.Word)
	if !ok {
		return []string{unseenIn("redirect", "a redirection to "+source(r.Word)+" expands when it runs")}
	}
	if !filepath.IsAbs(target) {
		if sc.lost {
			return []string{unseenIn("redirect", "a redirection to "+target+" follows a cd that can't be read")}
		}
		target = filepath.Join(sc.dir, target)
	}
	return e.PathAsks(target)
}

// maxNesting bounds wrappers inside wrappers; past it a command asks, since nothing real nests so deep.
const maxNesting = 8

// shellNames are the shells: what they are handed runs where nothing can read it first.
var shellNames = []string{"sh", "bash", "zsh", "dash", "ksh", "ash", "fish", "csh", "tcsh"}

// wordsAsk is AlwaysAsks for a program named name, looking through what runs another program.
func (e *Engine) wordsAsk(name string, args []string, sc scan) []string {
	if sc.depth > maxNesting {
		return []string{unseen(name + " nests too many programs")}
	}
	if stringRunners[name] {
		return []string{unseen(name + runsTextEnd)}
	}
	if safeBuiltins[name] {
		return nil
	}
	if r, ok := runners[name]; ok {
		return e.runnerAsks(name, r, args, sc)
	}
	if slices.Contains(shellNames, name) {
		return shellAsks(name, args, sc)
	}
	if name == "parallel" {
		return []string{unseenIn(name, "it runs commands through a shell, with words from stdin")}
	}
	switch name {
	case "find":
		return e.findAsks(args, sc)
	case "git":
		return e.gitAsks(args, sc)
	case "rm":
		return e.rmAsks(args, sc)
	}
	return appendNew(dataLossAsks(name, args), agentAsks(name, args)...)
}

func appendNew(to []string, more ...string) []string {
	for _, s := range more {
		if !slices.Contains(to, s) {
			to = append(to, s)
		}
	}
	return to
}

// runner is a program that runs the program named in its arguments. A flag not listed here asks: it might take a value,
// or run something itself.
type runner struct {
	valued  string // the flags that take a value
	plain   string // the flags that don't
	unseen  string // those it can't follow: code as text, or another directory
	skip    int    // how many positional arguments come before the program
	assigns bool   // it takes NAME=value words before the program
}

var runners = map[string]runner{
	"env":        {valued: "-u --unset", plain: "-i --ignore-environment -0 --null -v --debug", unseen: "-S --split-string -C --chdir", assigns: true},
	"nice":       {valued: "-n --adjustment"},
	"nohup":      {},
	"time":       {valued: "-f --format -o --output", plain: "-p -v --verbose -a --append"},
	"timeout":    {valued: "-k --kill-after -s --signal", plain: "--preserve-status --foreground -v --verbose", skip: 1},
	"stdbuf":     {valued: "-i --input -o --output -e --error"},
	"sudo":       {valued: "-u --user -g --group -h --host -p --prompt -r --role -t --type -T --command-timeout -U --other-user", plain: "-n --non-interactive -E --preserve-env -H --set-home -b --background -k --reset-timestamp -S --stdin", unseen: "-D --chdir -R --chroot", assigns: true},
	"doas":       {valued: "-u", plain: "-n"},
	"xargs":      {valued: "-I -n --max-args -L --max-lines -P --max-procs -d --delimiter -E -s --max-chars -a --arg-file -J -R -S", plain: "-0 --null -r --no-run-if-empty -t --verbose -x --exit -o"},
	"caffeinate": {valued: "-t -w", plain: "-d -i -m -s -u"},
	"setsid":     {plain: "-f --fork -w --wait -c --ctty"},
	"exec":       {valued: "-a", plain: "-c -l"},
	"command":    {plain: "-p"},
	"builtin":    {},
	"npx":        {valued: "-p --package", plain: "-y --yes --no -q --quiet", unseen: "-c --call"},
	"bunx":       {valued: "-p --package", plain: "-b --bun"},
	"uvx":        {valued: "--from --with", plain: "-q --quiet"},
}

func (e *Engine) runnerAsks(name string, r runner, args []string, sc scan) []string {
	if name == "command" && len(args) > 0 && (args[0] == "-v" || args[0] == "-V") {
		return nil // it looks a program up, and runs nothing
	}
	why, first, unsure := runnerRead(name, r, args)
	for _, i := range programsFrom(args, first, unsure) {
		rest := args[i+1:]
		if name == "xargs" { // it adds words from stdin, which can't be seen
			rest = append(slices.Clone(rest), unknownWord)
			if mark := xargsMark(args[:i]); mark != "" {
				if strings.Contains(args[i], mark) {
					why = appendNew(why, unseenIn(name, "what it runs is filled in from stdin"))
					continue
				}
				rest = fillIn(rest, mark)
			}
			if shell := e.shellUnder(args[i], rest, sc); shell != "" {
				why = appendNew(why, unseenIn(shell, "it is handed code"))
				continue
			}
		}
		why = appendNew(why, e.programAsks(args[i], rest, sc.in())...)
	}
	return why
}

// xargsMark returns the word -I or -J says xargs replaces with what it reads, or "".
func xargsMark(flags []string) string {
	for i, a := range flags {
		if (a == "-I" || a == "-J") && i+1 < len(flags) {
			return flags[i+1]
		}
		if (strings.HasPrefix(a, "-I") || strings.HasPrefix(a, "-J")) && len(a) > 2 {
			return a[2:]
		}
	}
	return ""
}

// fillIn is words with each holding mark, which a program replaces with what it finds, as a word that can't be seen.
func fillIn(words []string, mark string) []string {
	out := slices.Clone(words)
	for i, w := range out {
		if strings.Contains(w, mark) {
			out[i] = unknownWord
		}
	}
	return out
}

// runnerRead reads a runner's own words, and returns why they ask, the index of the program it runs or -1, and unsure if a flag it doesn't
// know may have taken that word (programsFrom). A word it can't read doesn't stop it, so what the runner runs is still shown. A flag it can't
// follow does.
func runnerRead(name string, r runner, args []string) (why []string, prog int, unsure bool) {
	valued, plain, hidden := strings.Fields(r.valued), strings.Fields(r.plain), strings.Fields(r.unseen)
	skip := r.skip
	for i := 0; i < len(args); i++ {
		a := args[i]
		flag, value, hasValue := strings.Cut(a, "=")
		switch {
		case a == unknownWord:
			why = appendNew(why, unseenIn(name, "an argument expands when it runs"))
			if skip > 0 {
				skip--
			}
		case a == "--":
		case slices.Contains(hidden, flag) || (len(a) > 2 && slices.Contains(hidden, a[:2])):
			return appendNew(why, unseenIn(name, flag+" can't be followed")), -1, false
		case slices.Contains(valued, a):
			i++
			if i < len(args) && args[i] == unknownWord {
				why = appendNew(why, unseenIn(name, "a value of "+a+" expands when it runs"))
			}
		case hasValue && slices.Contains(valued, flag):
		case !strings.HasPrefix(a, "--") && len(a) > 2 && a[0] == '-' && slices.Contains(valued, a[:2]): // -n5
		case slices.Contains(plain, a):
		case strings.HasPrefix(a, "-") && len(a) > 1:
			why = appendNew(why, unseenIn(name, "the flag "+a+" isn't one it knows"))
			unsure = true
		case r.assigns && hasValue:
			if r := HooksOff(flag, value); r != "" {
				why = appendNew(why, r)
			}
		case skip > 0:
			skip--
		default:
			return why, i, unsure
		}
	}
	return why, -1, false
}

// programsFrom returns where the program may be in a runner's words: at i, or, after a flag it can't read, which may take the next word, at any
// word from there that isn't a flag.
func programsFrom(args []string, i int, unsure bool) []int {
	if i < 0 {
		return nil
	}
	if !unsure {
		return []int{i}
	}
	var out []int
	for j := i; j < len(args); j++ {
		if args[j] != unknownWord && !strings.HasPrefix(args[j], "-") {
			out = append(out, j)
		}
	}
	return out
}

// shellAsks asks for a shell handed code in any way: -c, stdin, a script that is stdin, or a flag it can't read. A script named
// with no flags is left alone, like any other program: what a program does when it runs is the sandbox's to hold. feedsShell
// reads what's fed to one from outside its arguments.
func shellAsks(name string, args []string, sc scan) []string {
	if len(args) > 0 && args[0] != unknownWord && !strings.HasPrefix(args[0], "-") && !strings.HasPrefix(args[0], "+") {
		if sc.lost && !filepath.IsAbs(args[0]) {
			return []string{unseenIn(name, "its script follows a cd that can't be read")}
		}
		if !scriptIsStdin(args[0], sc.dir) {
			return nil
		}
	}
	return []string{unseenIn(name, "it is handed code")}
}

// scriptIsStdin reports whether a shell's script argument is a device rather than a file (/dev/stdin, /dev/fd/N,
// /proc/self/fd/N), or a symlink to one, which it can't follow by resolving: those don't resolve to a path.
func scriptIsStdin(script, dir string) bool {
	p := script
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	for range maxNesting {
		p = filepath.Clean(p)
		if real, err := filepath.EvalSymlinks(filepath.Dir(p)); err == nil {
			p = filepath.Join(real, filepath.Base(p))
		}
		if strings.HasPrefix(p, "/dev/") || strings.HasPrefix(p, "/proc/") {
			return true
		}
		to, err := os.Readlink(p)
		if err != nil {
			return false
		}
		if !filepath.IsAbs(to) {
			to = filepath.Join(filepath.Dir(p), to)
		}
		p = to
	}
	return true
}

// ShellFed is the reason a program with these arguments, as it runs with something on its stdin, is a shell handed code, or "". The
// run-time check has no parse of the pipe, only that there is one.
func (e *Engine) ShellFed(args []string, dir string) string {
	if shell := e.shellUnder(args[0], args[1:], scan{dir: dir}); shell != "" {
		return unseenIn(shell, "it is handed code")
	}
	return ""
}

// feedsShell returns the shell a simple command runs, directly, under a wrapper, or through a symlink, or "".
func (e *Engine) feedsShell(words []*syntax.Word, sc scan) string {
	if len(words) == 0 {
		return ""
	}
	first, ok := literal(words[0])
	if !ok {
		return ""
	}
	argv, lit := args(words[1:])
	for i := range argv {
		if !lit[i] {
			argv[i] = unknownWord
		}
	}
	return e.shellUnder(first, argv, sc)
}

// shellUnder returns the shell a program with these arguments runs, or "". What a wrapper runs is read as the wrapper reads it, so an
// argument named sh is only an argument. After a cd it lost, a relative program may be anything, a shell too.
func (e *Engine) shellUnder(name string, args []string, sc scan) string {
	for {
		base := filepath.Base(name)
		if sc.lost && strings.Contains(name, "/") && !filepath.IsAbs(name) {
			return base
		}
		if real, err := e.resolveFrom(name, sc.dir); err == nil {
			if r := filepath.Base(real); slices.Contains(shellNames, r) {
				return r
			}
		}
		if slices.Contains(shellNames, base) {
			return base
		}
		r, ok := runners[base]
		if !ok {
			return ""
		}
		_, first, unsure := runnerRead(base, r, args)
		programs := programsFrom(args, first, unsure)
		if len(programs) == 0 {
			return ""
		}
		for _, i := range programs[1:] {
			if shell := e.shellUnder(args[i], args[i+1:], sc); shell != "" {
				return shell
			}
		}
		name, args = args[programs[0]], args[programs[0]+1:]
	}
}

// stdinAsks asks for what is fed to a shell: a pipe into it, or an input redirection, a here-document or a here-string on it, on a
// block or subshell around it too. It returns the commands it asks about, since the reason is on the pipe or the redirection.
func (e *Engine) stdinAsks(n syntax.Node, sc scan) ([]string, []*syntax.CallExpr) {
	fed := func(c syntax.Command) ([]string, []*syntax.CallExpr) {
		if c == nil {
			return nil, nil
		}
		var why []string
		var calls []*syntax.CallExpr
		syntax.Walk(c, func(n syntax.Node) bool {
			if call, ok := n.(*syntax.CallExpr); ok {
				if shell := e.feedsShell(call.Args, sc); shell != "" {
					why = appendNew(why, unseenIn(shell, "it is handed code"))
					calls = append(calls, call)
				}
			}
			return true
		})
		return why, calls
	}
	switch n := n.(type) {
	case *syntax.BinaryCmd:
		if n.Op == syntax.Pipe || n.Op == syntax.PipeAll {
			return fed(n.Y.Cmd)
		}
	case *syntax.Stmt:
		for _, r := range n.Redirs {
			switch r.Op {
			case syntax.RdrIn, syntax.RdrInOut, syntax.DplIn, syntax.Hdoc, syntax.DashHdoc, syntax.WordHdoc:
				return fed(n.Cmd)
			}
		}
	}
	return nil, nil
}

// findAsks reads the commands find is told to run.
func (e *Engine) findAsks(args []string, sc scan) []string {
	var why []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case unknownWord: // it could be -exec or -delete
			why = appendNew(why, unseenIn("find", "an argument expands when it runs"))
		case "-delete", "-fprint", "-fprint0", "-fprintf", "-fls":
			why = appendNew(why, "find "+args[i]+" deletes or writes files, and what it matches can't be seen before it runs")
		case "-execdir", "-okdir": // it runs where each match is, which paths in it depend on
			why = appendNew(why, unseenIn("find", args[i]+" runs in each match's directory"))
			i = len(args)
		case "-exec", "-ok":
			end := i + 1
			for end < len(args) && args[end] != ";" && args[end] != "+" {
				end++
			}
			switch {
			case i+1 >= end:
			case args[i+1] == unknownWord:
				why = appendNew(why, unseenIn("find", "the program it runs expands when it runs"))
			case strings.Contains(args[i+1], "{}"):
				why = appendNew(why, unseenIn("find", "what it runs is filled in from what it finds"))
			default:
				rest := fillIn(args[i+2:end], "{}") // what find found, which can't be seen
				if shell := e.shellUnder(args[i+1], rest, sc); shell != "" {
					why = appendNew(why, unseenIn(shell, "it is handed code"))
				}
				why = appendNew(why, e.programAsks(args[i+1], rest, sc.in())...)
			}
			i = end
		}
	}
	return why
}

// shortFlag reports whether arg is a cluster of one-letter flags holding letter, stopping at a flag in valued, whose
// value is the rest of the word.
func shortFlag(arg string, letter byte, valued string) bool {
	if len(arg) < 2 || arg[0] != '-' || arg[1] == '-' {
		return false
	}
	for i := 1; i < len(arg); i++ {
		if arg[i] == letter {
			return true
		}
		if strings.IndexByte(valued, arg[i]) >= 0 {
			return false
		}
	}
	return false
}

// gitValued and gitPlain are the git global flags it reads: those that take the next word as a value, and those that
// don't. Any other form before the subcommand asks, an attached -cname=value and -Cdir among them.
var (
	gitValued = []string{"-C", "-c", "--git-dir", "--work-tree", "--namespace", "--super-prefix", "--config-env", "--attr-source"}
	gitPlain  = []string{"-p", "--paginate", "-P", "--no-pager", "--bare", "--no-replace-objects", "--literal-pathspecs", "--glob-pathspecs",
		"--noglob-pathspecs", "--icase-pathspecs", "--no-optional-locks", "--no-lazy-fetch", "--no-advice", "--version", "-v", "--help", "-h",
		"--html-path", "--man-path", "--info-path", "--exec-path"}
)

// noVerifySubcommands are the ones whose --no-verify skips the owner's hooks.
var noVerifySubcommands = []string{"commit", "push", "merge", "am", "rebase", "cherry-pick", "revert", "pull"}

func (e *Engine) gitAsks(args []string, sc scan) []string {
	dir := sc.dir
	var why []string
	i := 0
	for ; i < len(args); i++ {
		a := args[i]
		if a == unknownWord {
			why = appendNew(why, unseenIn("git", "a word before its subcommand expands when it runs"))
			continue
		}
		if !strings.HasPrefix(a, "-") {
			break
		}
		flag, _, hasValue := strings.Cut(a, "=")
		switch {
		case flag == "--work-tree":
			// It moves what the path rules below read.
			why = appendNew(why, unseenIn("git", "--work-tree moves where it works"))
			if !hasValue {
				i++
			}
		case a == "-c" || flag == "--config-env":
			// An alias runs inside git, where nothing sees it, and --config-env's value is an environment variable's.
			why = appendNew(why, unseenIn("git", flag+" sets config on the command line, which changes what git does"))
			if !hasValue {
				i++
			}
		case slices.Contains(gitValued, a):
			i++
			if i >= len(args) {
				return why
			}
			if args[i] == unknownWord {
				why = appendNew(why, unseenIn("git", "a value of "+a+" expands when it runs"))
				continue
			}
			if a == "-C" {
				if filepath.IsAbs(args[i]) {
					dir = args[i]
				} else {
					dir = filepath.Join(dir, args[i])
				}
			}
		case hasValue && strings.HasPrefix(flag, "--") && slices.Contains(gitValued, flag):
		case slices.Contains(gitPlain, a):
		default:
			why = appendNew(why, unseenIn("git", "the flag "+a+" isn't one it reads"))
		}
	}
	if i >= len(args) {
		return why
	}
	sub, rest := args[i], args[i+1:]
	if slices.ContainsFunc(flagsOf(sub, rest), gitValuedFlags[sub].abbreviated) {
		why = appendNew(why, unseenIn("git", "a flag of "+sub+" is cut short, and its value can't be read"))
	}
	if gitWordsMatter(sub, rest) {
		why = appendNew(why, unseenIn("git", "an argument of "+sub+" expands when it runs"))
	}
	if slices.Contains(noVerifySubcommands, sub) && slices.ContainsFunc(flagsOf(sub, rest), func(a string) bool { return flagIs(a, "--no-verify") }) {
		why = append(why, "git "+sub+" --no-verify skips the owner's hooks")
	}
	discard := func(what string) { why = append(why, "git "+what+" discards work that cannot be recovered") }
	switch sub {
	case "reset":
		for _, f := range []string{"--hard", "--merge", "--keep"} {
			if slices.ContainsFunc(flagsOf(sub, rest), func(a string) bool { return flagIs(a, f) }) {
				discard("reset " + f)
			}
		}
	case "checkout":
		if sc.lost && !slices.ContainsFunc(rest, func(a string) bool { return a == "-b" || a == "-B" || a == "--orphan" }) {
			why = appendNew(why, unseenIn("git", "checkout follows a cd that can't be read"))
		}
		if checkoutDiscards(rest, dir) {
			discard("checkout, used this way,")
		}
	case "switch":
		if slices.ContainsFunc(flagsOf(sub, rest), func(a string) bool {
			return flagIs(a, "--discard-changes") || flagIs(a, "--force") || shortFlag(a, 'f', gitValuedFlags["switch"].letters())
		}) {
			discard("switch with --discard-changes or --force")
		}
	case "restore":
		staged := lastWins(flagsOf(sub, rest), func(a string) bool {
			return flagIs(a, "--staged") || shortFlag(a, 'S', gitValuedFlags["restore"].letters())
		}, "staged")
		worktree := lastWins(flagsOf(sub, rest), func(a string) bool {
			return flagIs(a, "--worktree") || shortFlag(a, 'W', gitValuedFlags["restore"].letters())
		}, "worktree")
		if !staged || worktree {
			discard("restore on the working tree")
		}
	case "clean":
		flags := flagsOf(sub, rest)
		dry := lastWins(flags, func(a string) bool {
			return flagIs(a, "--dry-run") || shortFlag(a, 'n', gitValuedFlags["clean"].letters())
		}, "dry-run")
		force := slices.ContainsFunc(flags, func(a string) bool {
			return flagIs(a, "--force") || shortFlag(a, 'f', gitValuedFlags["clean"].letters())
		})
		if force && !dry {
			discard("clean -f")
		}
	case "branch":
		del := slices.ContainsFunc(flagsOf(sub, rest), func(a string) bool {
			return flagIs(a, "--delete") || shortFlag(a, 'd', gitValuedFlags["branch"].letters())
		})
		force := slices.ContainsFunc(flagsOf(sub, rest), func(a string) bool {
			return flagIs(a, "--force") || shortFlag(a, 'f', gitValuedFlags["branch"].letters())
		})
		if (del && force) || slices.ContainsFunc(flagsOf(sub, rest), func(a string) bool { return shortFlag(a, 'D', gitValuedFlags["branch"].letters()) }) {
			discard("branch -D")
		}
	case "stash":
		if len(rest) > 0 && (rest[0] == "drop" || rest[0] == "clear") {
			discard("stash " + rest[0])
		}
	case "commit":
		if slices.ContainsFunc(flagsOf(sub, rest), func(a string) bool { return shortFlag(a, 'n', gitValuedFlags["commit"].letters()) }) {
			why = appendNew(why, "git commit --no-verify skips the owner's hooks")
		}
	case "config":
		if configSetsHooksPath(rest) {
			why = append(why, "git core.hooksPath skips the owner's hooks")
		}
	}
	return why
}

// gitWordsMatter reports whether an argument of sub that expands could be what a rule above looks for.
func gitWordsMatter(sub string, rest []string) bool {
	if !slices.Contains(noVerifySubcommands, sub) && !slices.Contains([]string{"reset", "checkout", "switch", "restore", "clean", "branch", "stash", "config"}, sub) {
		return false
	}
	return slices.Contains(rest, unknownWord)
}

// flagsOf is a subcommand's arguments up to --, after which they are paths, without its flags' values (gitValuedFlags), which could spell
// a flag. A value that expands is kept, since it may split into flags.
func flagsOf(sub string, rest []string) []string {
	valued := gitValuedFlags[sub]
	var out []string
	for j := 0; j < len(rest); j++ {
		a := rest[j]
		if a == "--" {
			break
		}
		if valued.takesNext(a) && j+1 < len(rest) && rest[j+1] != unknownWord {
			out = append(out, a)
			j++
			continue
		}
		out = append(out, a)
	}
	return out
}

// valuedFlags are a git subcommand's flags that take a value. The long ones and the letters in next take the next word. Those in attached
// take one only attached (-uno), so the next word is still a flag, as real git shows: git commit -u --no-verify skips the hooks.
type valuedFlags struct {
	long           []string
	next, attached string
}

var gitValuedFlags = map[string]valuedFlags{
	"commit":  {long: []string{"--message", "--file", "--template", "--reuse-message", "--reedit-message", "--author", "--date", "--trailer", "--fixup", "--squash", "--cleanup", "--pathspec-from-file"}, next: "mFcCt", attached: "uS"},
	"clean":   {long: []string{"--exclude"}, next: "e"},
	"restore": {long: []string{"--source", "--pathspec-from-file"}, next: "s"},
	"switch":  {long: []string{"--create", "--force-create", "--orphan"}, next: "cC"},
	"branch":  {long: []string{"--contains", "--no-contains", "--merged", "--no-merged", "--points-at", "--sort", "--format", "--set-upstream-to"}, next: "u"},
	"reset":   {long: []string{"--pathspec-from-file"}},
}

// letters is every letter that takes a value, which ends a cluster of one-letter flags.
func (v valuedFlags) letters() string { return v.next + v.attached }

// abbreviated reports whether a may be one of these long flags cut short. Then it can't be said if the next word is its value, so it asks.
func (v valuedFlags) abbreviated(a string) bool {
	return len(a) > 2 && strings.HasPrefix(a, "--") && !slices.Contains(v.long, a) &&
		slices.ContainsFunc(v.long, func(full string) bool { return strings.HasPrefix(full, a) })
}

// takesNext reports whether the next word is this flag's value: a long flag in full, or letters that end in one that takes it.
func (v valuedFlags) takesNext(a string) bool {
	if strings.HasPrefix(a, "--") {
		return slices.Contains(v.long, a)
	}
	if len(a) < 2 || a[0] != '-' {
		return false
	}
	for i := 1; i < len(a); i++ {
		if strings.IndexByte(v.attached, a[i]) >= 0 {
			return false
		}
		if strings.IndexByte(v.next, a[i]) >= 0 {
			return i == len(a)-1
		}
	}
	return false
}

// lastWins reports whether a flag is on after all of flags, the last of it and its --no- form winning, as in git.
func lastWins(flags []string, on func(string) bool, name string) bool {
	state := false
	for _, a := range flags {
		switch {
		case on(a):
			state = true
		case flagIs(a, "--no-"+name):
			state = false
		}
	}
	return state
}

// flagIs reports whether a is a long flag, spelled in full or by the prefix git accepts for it: git reset --har is --hard.
func flagIs(a, full string) bool {
	return a == full || (len(a) > 2 && strings.HasPrefix(a, "--") && strings.HasPrefix(full, a))
}

// checkoutDiscards reports whether git checkout's arguments overwrite the working tree: paths after --, a path that
// exists, a pathspec, ".", or -f. A new branch's name is not a path.
func checkoutDiscards(args []string, dir string) bool {
	creating := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			return i+1 < len(args)
		case flagIs(a, "--force") || shortFlag(a, 'f', "bB") || strings.HasPrefix(a, "--pathspec-from-file"):
			return true
		case a == "-b" || a == "-B" || a == "--orphan":
			creating = true
			i++
		case strings.HasPrefix(a, "-"), a == unknownWord:
		case a == ".":
			return true
		case creating:
		case strings.ContainsAny(a, "*?[") || strings.HasPrefix(a, ":"): // a pathspec, whatever it matches
			return true
		default:
			p := a
			if !filepath.IsAbs(p) {
				p = filepath.Join(dir, p)
			}
			if _, err := os.Lstat(p); err == nil {
				return true
			}
		}
	}
	return false
}

// configSetsHooksPath reports whether git config's arguments change core.hooksPath. Reading it changes nothing. Whatever follows the
// key is a value, however it looks.
func configSetsHooksPath(args []string) bool {
	changes := false
	for i, a := range args {
		switch {
		case strings.EqualFold(a, "core.hooksPath"):
			return changes || i+1 < len(args)
		case a == "--get" || a == "--get-all" || a == "--get-regexp" || a == "--list" || a == "-l" || a == "get" || a == "list":
			return false
		case flagIs(a, "--unset") || flagIs(a, "--unset-all") || flagIs(a, "--add") || flagIs(a, "--replace-all") || a == "set" || a == "unset":
			changes = true
		}
	}
	return false
}

// rebuilt are the directories a build makes again, which rm -r may take without asking when they are directly where
// the command runs (the guardrail library's list).
var rebuilt = []string{"node_modules", "dist", "build", "out", "coverage", "target", ".next", ".turbo", ".cache", ".vite", ".parcel-cache"}

// rmAsks asks for rm -r on anything but a rebuilt directory or a path under the temp directory, and for rm touching the
// hooks. The sandbox refuses most paths outside the workspace anyway; asking costs less than finding out.
func (e *Engine) rmAsks(args []string, sc scan) []string {
	dir := sc.dir
	recursive, ended := false, false
	var targets []string
	for _, a := range args {
		switch {
		case ended:
			targets = append(targets, a)
		case a == "--":
			ended = true
		case a == "--recursive" || shortFlag(a, 'r', "") || shortFlag(a, 'R', ""):
			recursive = true
		case strings.HasPrefix(a, "-") && len(a) > 1:
		default:
			targets = append(targets, a)
		}
	}
	var why []string
	for _, t := range targets {
		if t == unknownWord { // it could be a flag, a directory or the hooks
			why = appendNew(why, unseenIn("rm", "an argument expands when it runs"))
			continue
		}
		if sc.lost && !filepath.IsAbs(t) {
			why = appendNew(why, unseenIn("rm", t+" follows a cd that can't be read"))
			continue
		}
		p := t
		if !filepath.IsAbs(p) {
			p = filepath.Join(dir, p)
		}
		p = filepath.Clean(p)
		if inHooks(p) || inHooks(resolveExisting(p)) {
			why = appendNew(why, "rm "+t+" removes the owner's hooks")
		}
		if recursive && !e.rmFree(t, p, dir) {
			why = appendNew(why, "rm -r "+t+" deletes a directory that cannot be recovered")
		}
	}
	if recursive && len(targets) == 0 { // xargs, or a glob that matched nothing: nothing here says what it deletes
		why = appendNew(why, "rm -r with no path here deletes what it is handed")
	}
	return why
}

func inHooks(p string) bool {
	segs := strings.Split(filepath.ToSlash(p), "/")
	for i := 0; i+1 < len(segs); i++ {
		if strings.EqualFold(segs[i], ".git") && strings.EqualFold(segs[i+1], "hooks") {
			return true
		}
	}
	return false
}

// rmFree reports whether rm -r of p, written t, needs no question: a rebuilt directory directly under dir, not reached
// through a symlink, or a path strictly under the temp directory, outside the workspace.
func (e *Engine) rmFree(t, p, dir string) bool {
	real := resolveExisting(p)
	root := e.root()
	if !within(root, real) && !within(real, root) { // neither in the workspace nor around it
		for _, tmp := range []string{os.TempDir(), "/tmp", "/private/tmp", "/var/tmp"} {
			tmp = resolveExisting(filepath.Clean(tmp))
			if real != tmp && within(tmp, real) {
				return true
			}
		}
	}
	rel, err := filepath.Rel(dir, p)
	if err != nil {
		return false
	}
	first, _, _ := strings.Cut(filepath.ToSlash(rel), "/")
	return !within(real, root) && slices.Contains(rebuilt, first) && real == filepath.Join(resolveExisting(filepath.Clean(dir)), rel)
}

// dataLossReaders only read or print their arguments, so a flag's name in them is a mention.
var dataLossReaders = []string{"grep", "egrep", "fgrep", "rg", "ag", "ack", "cat", "sed", "awk", "curl", "wget", "man", "less", "head", "tail"}

// migrationResets are the word runs that drop a database's data: the first word may be a path to the program.
var migrationResets = [][]string{
	{"prisma", "migrate", "reset"},
	{"alembic", "downgrade", "base"},
	{"rails", "db:drop"}, {"rails", "db:reset"}, {"rake", "db:drop"}, {"rake", "db:reset"},
	{"django-admin", "flush"}, {"manage.py", "flush"},
	{"sequelize", "db:migrate:undo:all"},
	{"drizzle-kit", "drop"},
}

func dataLossAsks(name string, args []string) []string {
	if slices.Contains(dataLossReaders, name) {
		return nil
	}
	words := append([]string{name}, args...)
	var why []string
	for _, a := range args {
		flag, _, _ := strings.Cut(a, "=")
		if flag == "--accept-data-loss" || flag == "--force-reset" {
			why = appendNew(why, flag+" accepts losing data")
		}
	}
	has := func(run ...string) bool {
		for i := 0; i+len(run) <= len(words); i++ {
			if filepath.Base(words[i]) == run[0] && slices.Equal(words[i+1:i+len(run)], run[1:]) {
				return true
			}
		}
		return false
	}
	for _, run := range migrationResets {
		if has(run...) {
			why = appendNew(why, strings.Join(run, " ")+" drops a database's data")
		}
	}
	if has("prisma", "db", "push") && slices.Contains(args, "--force") {
		why = appendNew(why, "prisma db push --force drops a database's data")
	}
	if has("knex", "migrate:rollback") && slices.Contains(args, "--all") {
		why = appendNew(why, "knex migrate:rollback --all drops a database's data")
	}
	// A word that expands could be any of those, for a program they name. Other programs' words mean nothing to these rules.
	if slices.Contains(args, unknownWord) && slices.ContainsFunc(words, func(w string) bool { return slices.Contains(dataLossPrograms, filepath.Base(w)) }) {
		why = appendNew(why, unseenIn(name, "an argument expands when it runs"))
	}
	return why
}

// dataLossPrograms are the programs the rules above read.
var dataLossPrograms = []string{"prisma", "alembic", "rails", "rake", "django-admin", "manage.py", "sequelize", "drizzle-kit", "knex"}

// agentNames are other agents' programs, by what they or their packages are called, and the flags and subcommands that
// hand them a task. Run with no arguments, each starts too; --version and the like do not.
var agentNames = map[string]struct {
	canon string
	task  []string
}{
	"claude":       {"claude", []string{"-p", "--print"}},
	"claude-code":  {"claude", []string{"-p", "--print"}},
	"codex":        {"codex", []string{"exec", "e"}},
	"gemini":       {"gemini", []string{"-p", "--prompt"}},
	"gemini-cli":   {"gemini", []string{"-p", "--prompt"}},
	"cursor-agent": {"cursor-agent", []string{"-p", "--print"}},
	"aider":        {"aider", []string{"--message", "--msg", "-m", "-f", "--message-file"}},
	"tack":         {"tack", []string{"run", "acp", "serve", "tui"}},
	"tackd":        {"tack", []string{"run", "acp", "serve", "tui"}},
}

// agentBypasses are the flags, with the value that matters where one does, that take the other agent's own checks off.
var agentBypasses = [][2]string{
	{"--dangerously-skip-permissions"}, {"--dangerously-bypass-approvals-and-sandbox"}, {"--yolo"}, {"--full-auto"},
	{"--yes-always"}, {"--permission-mode", "bypassPermissions"}, {"--approval-mode", "yolo"}, {"-a", "never"},
	{"--sandbox", "danger-full-access"}, {"-c", "approval_policy=never"},
}

func agentAsks(name string, args []string) []string {
	a, ok := agentNames[name]
	if !ok {
		return nil
	}
	start := []string{a.canon + " starts another agent, which runs outside this session's checks"}
	if len(args) == 0 {
		return start
	}
	if slices.Contains(args, unknownWord) {
		return []string{unseenIn(name, "an argument expands when it runs")}
	}
	var words []string
	for _, w := range args {
		k, v, ok := strings.Cut(w, "=")
		if !ok || !strings.HasPrefix(w, "-") {
			words = append(words, w)
			continue
		}
		words = append(words, k, v)
	}
	for i, w := range words {
		if w == "--help" || w == "-h" || w == "--version" {
			return nil
		}
		for _, b := range agentBypasses {
			if w == b[0] && (b[1] == "" || (i+1 < len(words) && words[i+1] == b[1])) {
				return start
			}
		}
		if slices.Contains(a.task, w) {
			return start
		}
	}
	return nil
}

// HooksOff returns why setting name to value skips the owner's hooks, or "".
func HooksOff(name, value string) string {
	if (name == "HUSKY" && value == "0") || (name == "HUSKY_SKIP_HOOKS" && value == "1") {
		return name + "=" + value + " skips the owner's hooks"
	}
	return ""
}

// declared reads an assignment or a declaration's argument: the variable it sets, its value if fixed, and dynamic if what it sets
// expands (export $v, which may set anything).
func declared(a *syntax.Assign) (name, value string, dynamic bool) {
	if a.Name != nil {
		if a.Array != nil || a.Append { // its value is more than this word says
			return a.Name.Value, unknownWord, false
		}
		if a.Value != nil {
			if v, ok := literal(a.Value); ok {
				return a.Name.Value, v, false
			}
			return a.Name.Value, unknownWord, false
		}
		return a.Name.Value, oneValue, false
	}
	if a.Value == nil {
		return "", "", false
	}
	v, ok := literal(a.Value)
	if !ok {
		return "", "", true
	}
	name, value, _ = strings.Cut(v, "=")
	return name, value, false
}

// expansionSets reads an expansion that assigns (${V:=x}, ${V=x}): the variable it sets, and dynamic if that expands (${!v:=x}).
func expansionSets(p *syntax.ParamExp) (name string, dynamic bool) {
	if p.Exp == nil || (p.Exp.Op != syntax.AssignUnset && p.Exp.Op != syntax.AssignUnsetOrNull) {
		return "", false
	}
	if p.Excl || p.Param == nil {
		return "", true
	}
	return p.Param.Value, false
}

// assignAsks is HooksOff for what an assignment sets, and asks if it can't read what that is.
func assignAsks(a *syntax.Assign) []string {
	name, value, dynamic := declared(a)
	switch {
	case dynamic:
		return []string{unseen("a declaration expands when it runs, and may set any variable")}
	case isHookVar(name) && value == unknownWord:
		return []string{unseenIn(name, "its value expands when it runs")}
	}
	if r := HooksOff(name, value); r != "" {
		return []string{r}
	}
	return nil
}

// PathAsks returns why changing path, as a file tool or a redirection would, asks in every mode, or nil. It reads the
// path as given and through its symlinks, relative to the workspace when inside it, in any letter case, since macOS's
// file system ignores case.
func (e *Engine) PathAsks(path string) []string {
	p := path
	if !filepath.IsAbs(p) {
		p = filepath.Join(e.Root, p)
	}
	p = filepath.Clean(p)
	var why []string
	for _, c := range []struct{ root, p string }{{e.Root, p}, {e.root(), resolveExisting(p)}} {
		rel := c.p
		if within(c.root, c.p) {
			if r, err := filepath.Rel(c.root, c.p); err == nil {
				rel = r
			}
		}
		if kind := protectedKind(rel); kind != "" {
			why = appendNew(why, fmt.Sprintf("%s is %s", filepath.Base(p), kind))
		}
	}
	return why
}

const (
	kindCI          = "a CI definition, which runs with the repository's credentials"
	kindInstruction = "an instruction file, which could change a model's standing orders"
)

// instructionFiles and ciFiles are file names, lower case, anywhere in the tree.
var (
	instructionFiles = []string{"claude.md", "claude.local.md", "agents.md", "agents.override.md", "gemini.md", "context.md",
		".cursorrules", ".windsurfrules", ".clinerules", ".aider.conf.yml"}
	ciFiles = []string{".gitlab-ci.yml", "jenkinsfile", "azure-pipelines.yml", "bitbucket-pipelines.yml", ".travis.yml", ".drone.yml"}
)

// protectedKind says what rel, a path, is: one of the kinds above, or "".
func protectedKind(rel string) string {
	segs := strings.Split(strings.ToLower(filepath.ToSlash(rel)), "/")
	base := segs[len(segs)-1]
	switch {
	case slices.Contains(instructionFiles, base):
		return kindInstruction
	case slices.Contains(ciFiles, base):
		return kindCI
	}
	dirs := segs[:len(segs)-1]
	under := func(path ...string) bool {
		for i := 0; i+len(path) <= len(dirs); i++ {
			if slices.Equal(dirs[i:i+len(path)], path) {
				return true
			}
		}
		return false
	}
	switch {
	case under(".agents"), under(".clinerules"), under(".claude", "rules"), under(".windsurf", "rules"), under(".devin", "rules"),
		under("cline", "rules"):
		return kindInstruction
	case under(".github") && base == "copilot-instructions.md":
		return kindInstruction
	case under(".github", "instructions") && strings.HasSuffix(base, ".instructions.md"):
		return kindInstruction
	case under(".github", "workflows"), under(".github", "actions"), under(".buildkite"), under(".circleci"):
		return kindCI
	}
	return ""
}
