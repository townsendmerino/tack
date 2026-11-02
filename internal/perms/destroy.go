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

// unknownWord stands for an argument that expands at run time: it matches no rule. At the top level the run-time check
// sees its value. In nested code it never does, so it counts as what it might be (scan.nested).
const unknownWord = ""

// scan is where a command is being read.
type scan struct {
	dir   string // where it runs
	depth int    // the programs and code it is inside
	// nested is true in code another process's shell runs (sh -c, env -S, code printed into a shell). internal/shell's
	// exec and open checks see only what tack's own interpreter runs, so nothing checks this code again once it
	// expands. What expands in a place that matters asks here, or never.
	nested bool
}

func (sc scan) in() scan {
	sc.depth++
	return sc
}

// expandsLater is the reason for a word in nested code that a rule would need to read.
func expandsLater(what string) string {
	return what + " expands inside a nested shell, where nothing checks it again"
}

// AlwaysAsks returns why s asks in every mode, one reason per cause, or nil. dir is where s runs, for its relative paths.
// An argument that expands at run time is judged only once it has (internal/shell's exec check passes Literal true).
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

// AlwaysAsksIn returns why command asks in every mode, as Decide reads it before it runs. A user who approved it
// approved these; the run-time check holds what expansion adds against them.
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

// walkDirs visits every node under node in order, with where it runs: dir, until a cd with a fixed target moves it. A
// subshell's or a substitution's cd ends with it.
func walkDirs(node syntax.Node, dir string, visit func(n syntax.Node, dir string)) {
	type frame struct {
		scoped bool
		dir    string
	}
	var stack []frame
	syntax.Walk(node, func(n syntax.Node) bool {
		if n == nil {
			top := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if top.scoped {
				dir = top.dir
			}
			return true
		}
		_, subshell := n.(*syntax.Subshell)
		_, subst := n.(*syntax.CmdSubst)
		stack = append(stack, frame{subshell || subst, dir})
		visit(n, dir)
		if c, ok := n.(*syntax.CallExpr); ok && len(c.Args) > 0 {
			dir = afterCd(c.Args, dir)
		}
		return true
	})
}

// scanAsks is AlwaysAsks for every simple command under node. It doesn't need a program to resolve: a command that fails
// to may still be there when it runs.
func (e *Engine) scanAsks(node syntax.Node, sc scan) []string {
	var why []string
	walkDirs(node, sc.dir, func(n syntax.Node, dir string) {
		sc := sc
		sc.dir = dir
		switch n := n.(type) {
		case *syntax.Assign:
			why = appendNew(why, assignAsks(n, sc.nested)...)
		case *syntax.BinaryCmd:
			why = appendNew(why, e.pipedAsks(n, sc)...)
		case *syntax.Stmt:
			why = appendNew(why, e.stdinAsks(n, sc)...)
		case *syntax.Redirect:
			why = appendNew(why, e.redirectAsks(n, sc)...)
		case *syntax.CallExpr:
			if len(n.Args) > 0 {
				why = appendNew(why, e.callAsks(n.Args, sc)...)
			}
		}
	})
	return why
}

// afterCd is where a simple command leaves the shell: dir, unless it is a cd with a fixed target.
func afterCd(words []*syntax.Word, dir string) string {
	if name, ok := literal(words[0]); !ok || name != "cd" || len(words) < 2 {
		return dir
	}
	for _, w := range words[1:] {
		t, ok := literal(w)
		switch {
		case !ok || t == "-":
			return dir
		case strings.HasPrefix(t, "-") && t != "--":
		case t == "--":
		case filepath.IsAbs(t):
			return filepath.Clean(t)
		default:
			return filepath.Join(dir, t)
		}
	}
	return dir
}

// callAsks reads one simple command by the name it is written with and the program that name resolves to.
func (e *Engine) callAsks(words []*syntax.Word, sc scan) []string {
	name, ok := literal(words[0])
	if !ok {
		if sc.nested {
			return []string{expandsLater("the program " + source(words[0]))}
		}
		return nil
	}
	if safeBuiltins[name] {
		return nil
	}
	argv, lit := args(words[1:])
	for i := range argv {
		if !lit[i] {
			argv[i] = unknownWord
		}
	}
	if sc.nested && (name == "eval" || name == "trap") {
		if len(argv) == 0 {
			return nil
		}
		code := argv
		if name == "trap" {
			code = argv[:1]
		}
		if slices.Contains(code, unknownWord) {
			return []string{expandsLater(name + "'s code")}
		}
		return e.codeAsks(strings.Join(code, " "), sc.in())
	}
	if stringRunners[name] {
		return nil
	}
	why := e.wordsAsk(filepath.Base(name), argv, sc)
	if real, err := e.resolveFrom(name, sc.dir); err == nil && filepath.Base(real) != filepath.Base(name) {
		why = appendNew(why, e.wordsAsk(filepath.Base(real), argv, sc)...)
	}
	return why
}

// redirectAsks is PathAsks for an output redirection whose target is fixed. At the top level the run-time check sees any
// other; in nested code nothing does.
func (e *Engine) redirectAsks(r *syntax.Redirect, sc scan) []string {
	switch r.Op {
	case syntax.RdrOut, syntax.AppOut, syntax.RdrClob, syntax.RdrAll, syntax.AppAll:
	default:
		return nil
	}
	if r.Word == nil {
		return nil
	}
	target, ok := literal(r.Word)
	if !ok {
		if sc.nested {
			return []string{expandsLater("a redirection to " + source(r.Word))}
		}
		return nil
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(sc.dir, target)
	}
	return e.PathAsks(target)
}

// maxNesting bounds wrappers inside wrappers; past it a command asks, since nothing real nests so deep.
const maxNesting = 8

// shellNames are the shells whose -c holds code.
var shellNames = []string{"sh", "bash", "zsh", "dash", "ksh", "ash"}

// wordsAsk is AlwaysAsks for a program named name, looking through what runs another program.
func (e *Engine) wordsAsk(name string, args []string, sc scan) []string {
	if sc.depth > maxNesting {
		return []string{name + " nests too many programs to read"}
	}
	if r, ok := runners[name]; ok {
		return e.runnerAsks(name, r, args, sc)
	}
	if slices.Contains(shellNames, name) {
		return e.shellAsks(args, sc)
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

// runner is a program that runs the program named in its arguments.
type runner struct {
	valued string // the flags that take the next word as a value
	code   string // those of them whose value is code
	skip   int    // how many positional arguments come before the program
}

var runners = map[string]runner{
	"env":        {valued: "-u --unset -C --chdir -S --split-string", code: "-S --split-string"},
	"nice":       {valued: "-n --adjustment"},
	"nohup":      {},
	"time":       {valued: "-f --format -o --output"},
	"timeout":    {valued: "-k --kill-after -s --signal", skip: 1},
	"stdbuf":     {valued: "-i --input -o --output -e --error"},
	"sudo":       {valued: "-u --user -g --group -h --host -p --prompt -C --close-from -r --role -t --type -T --command-timeout -U --other-user -D --chdir -R --chroot"},
	"doas":       {valued: "-u -C"},
	"xargs":      {valued: "-I -n -L -P -d -E -s -a -J -R -S"},
	"caffeinate": {valued: "-t -w"},
	"setsid":     {},
	"exec":       {valued: "-a"},
	"command":    {},
	"builtin":    {},
	"npx":        {valued: "-p --package -c --call", code: "-c --call"},
	"bunx":       {valued: "-p --package"},
	"uvx":        {valued: "--from --with"},
}

func (e *Engine) runnerAsks(name string, r runner, args []string, sc scan) []string {
	valued, code := strings.Fields(r.valued), strings.Fields(r.code)
	var why []string
	readCode := func(c string) {
		if c == unknownWord {
			if sc.nested {
				why = appendNew(why, expandsLater(name+"'s code"))
			}
			return
		}
		why = appendNew(why, e.codeAsks(c, sc.in())...)
	}
	skip := r.skip
	for i := 0; i < len(args); i++ {
		a := args[i]
		flag, value, hasValue := strings.Cut(a, "=")
		switch {
		case a == "--":
			continue
		case hasValue && slices.Contains(code, flag):
			readCode(value)
		case slices.Contains(valued, a):
			if slices.Contains(code, a) && i+1 < len(args) {
				readCode(args[i+1])
			}
			i++
		case strings.HasPrefix(a, "-") && len(a) > 1:
		case name == "env" && hasValue:
			if r := HooksOff(flag, value); r != "" {
				why = appendNew(why, r)
			}
		case skip > 0:
			skip--
		case a == unknownWord:
			if sc.nested {
				why = appendNew(why, expandsLater("the program "+name+" runs"))
			}
			return why // at the top level, the run-time check sees what it became
		default:
			return appendNew(why, e.wordsAsk(filepath.Base(a), args[i+1:], sc.in())...)
		}
	}
	return why
}

// shellAsks reads the code a shell is given with -c. A script file, or code piped in from a program, is not visible to
// the parse: those stay with the sandbox. pipedAsks and stdinAsks read what is visible of the second.
func (e *Engine) shellAsks(args []string, sc scan) []string {
	code := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
		case a == "-o" || a == "-O" || a == "+o" || a == "+O":
			i++
		case strings.HasPrefix(a, "-") && len(a) > 1:
			if !strings.HasPrefix(a, "--") && strings.Contains(a, "c") {
				code = true
			}
		case a == unknownWord:
			if sc.nested && code {
				return []string{expandsLater("a shell's code")}
			}
			return nil // at the top level, the run-time check sees this same call with its value
		case code:
			return e.codeAsks(a, sc.in())
		default:
			return nil // a script file
		}
	}
	return nil
}

// readsStdin reports whether a simple command is a shell that takes its code from stdin: no -c, and no script file.
func readsStdin(words []*syntax.Word) bool {
	if len(words) == 0 {
		return false
	}
	name, ok := literal(words[0])
	if !ok || !slices.Contains(shellNames, filepath.Base(name)) {
		return false
	}
	for _, w := range words[1:] {
		a, ok := literal(w)
		if !ok || !strings.HasPrefix(a, "-") || (!strings.HasPrefix(a, "--") && strings.Contains(a, "c")) {
			return false
		}
	}
	return true
}

// pipedAsks reads code printed straight into a shell (echo "git reset --hard" | sh). What it prints is all that's
// visible of it, and it runs in a real shell, so printed text that expands asks.
func (e *Engine) pipedAsks(b *syntax.BinaryCmd, sc scan) []string {
	if b.Op != syntax.Pipe && b.Op != syntax.PipeAll {
		return nil
	}
	from, ok := b.X.Cmd.(*syntax.CallExpr)
	into, ok2 := b.Y.Cmd.(*syntax.CallExpr)
	if !ok || !ok2 || len(from.Args) < 2 || !readsStdin(into.Args) {
		return nil
	}
	name, ok := literal(from.Args[0])
	if !ok || (name != "echo" && name != "printf") {
		return nil
	}
	printed, lit := args(from.Args[1:])
	if slices.Contains(lit, false) {
		return []string{expandsLater("what " + name + " prints into a shell")}
	}
	return e.codeAsks(printedText(name, printed), sc.in())
}

// stdinAsks reads code given to a shell as a here-document or here-string.
func (e *Engine) stdinAsks(s *syntax.Stmt, sc scan) []string {
	call, ok := s.Cmd.(*syntax.CallExpr)
	if !ok || !readsStdin(call.Args) {
		return nil
	}
	var why []string
	for _, r := range s.Redirs {
		w := r.Hdoc
		if r.Op == syntax.WordHdoc {
			w = r.Word
		}
		if w == nil {
			continue
		}
		if code, ok := literal(w); ok {
			why = appendNew(why, e.codeAsks(code, sc.in())...)
		} else {
			why = appendNew(why, expandsLater("a here-document given to a shell"))
		}
	}
	return why
}

// printedText is what echo or printf writes for fixed arguments.
func printedText(name string, args []string) string {
	unescape := strings.NewReplacer(`\n`, "\n", `\t`, "\t", `\\`, `\`).Replace
	if name == "echo" {
		for len(args) > 0 && (args[0] == "-n" || args[0] == "-e" || args[0] == "-E" || args[0] == "-ne" || args[0] == "-en") {
			args = args[1:]
		}
		return unescape(strings.Join(args, " "))
	}
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	if len(args) == 0 {
		return ""
	}
	format, rest := unescape(args[0]), args[1:]
	var b strings.Builder
	for { // printf reuses its format until the arguments run out
		used := false
		for i := 0; i < len(format); i++ {
			if format[i] != '%' || i+1 == len(format) {
				b.WriteByte(format[i])
				continue
			}
			i++
			for i+1 < len(format) && strings.IndexByte("-+ #0123456789.", format[i]) >= 0 {
				i++
			}
			if format[i] == '%' {
				b.WriteByte('%')
			} else if len(rest) > 0 {
				b.WriteString(rest[0])
				rest, used = rest[1:], true
			}
		}
		if !used || len(rest) == 0 {
			return b.String()
		}
	}
}

// codeAsks reads shell code given as a string, which runs where nothing checks it again.
func (e *Engine) codeAsks(code string, sc scan) []string {
	f, err := syntax.NewParser().Parse(strings.NewReader(code), "")
	if err != nil {
		return []string{"code given as a string does not parse: " + err.Error()}
	}
	sc.nested = true
	return e.scanAsks(f, sc)
}

// findAsks reads the commands find is told to run.
func (e *Engine) findAsks(args []string, sc scan) []string {
	var why []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-exec", "-execdir", "-ok", "-okdir":
			end := i + 1
			for end < len(args) && args[end] != ";" && args[end] != "+" {
				end++
			}
			switch {
			case i+1 >= end:
			case args[i+1] != unknownWord:
				why = appendNew(why, e.wordsAsk(filepath.Base(args[i+1]), args[i+2:end], sc.in())...)
			case sc.nested:
				why = appendNew(why, expandsLater("the program find runs"))
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

// gitGlobalValued are git's own flags that take the next word as a value.
var gitGlobalValued = []string{"-C", "-c", "--git-dir", "--work-tree", "--namespace", "--super-prefix", "--config-env", "--attr-source"}

// noVerifySubcommands are the ones whose --no-verify skips the owner's hooks.
var noVerifySubcommands = []string{"commit", "push", "merge", "am", "rebase", "cherry-pick", "revert", "pull"}

func (e *Engine) gitAsks(args []string, sc scan) []string {
	dir := sc.dir
	var why []string
	var configs []string // each -c and --config-env's key, lower case, with what follows it
	i := 0
	for ; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			break
		}
		if v, ok := strings.CutPrefix(a, "--config-env="); ok {
			configs = append(configs, strings.ToLower(v))
		}
		if !slices.Contains(gitGlobalValued, a) || i+1 >= len(args) {
			continue
		}
		i++
		switch a {
		case "-C":
			if filepath.IsAbs(args[i]) {
				dir = args[i]
			} else {
				dir = filepath.Join(dir, args[i])
			}
		case "-c", "--config-env":
			configs = append(configs, strings.ToLower(args[i]))
			if k, v, ok := strings.Cut(args[i], "="); ok && strings.HasPrefix(strings.ToLower(k), "alias.") && strings.HasPrefix(v, "!") {
				why = appendNew(why, e.codeAsks(v[1:], sc.in())...) // an alias that starts with ! is shell code
			}
		}
	}
	for _, c := range configs {
		if key, _, _ := strings.Cut(c, "="); key == "core.hookspath" {
			why = appendNew(why, "git core.hooksPath skips the owner's hooks")
		}
	}
	if i >= len(args) {
		return why
	}
	if args[i] == unknownWord {
		if sc.nested {
			why = appendNew(why, expandsLater("git's subcommand"))
		}
		return why
	}
	sub, rest := args[i], args[i+1:]
	if sc.nested && gitWordsMatter(sub, rest) {
		why = appendNew(why, expandsLater("an argument of git "+sub))
	}
	if slices.Contains(noVerifySubcommands, sub) && slices.Contains(rest, "--no-verify") {
		why = append(why, "git "+sub+" --no-verify skips the owner's hooks")
	}
	discard := func(what string) { why = append(why, "git "+what+" discards work that cannot be recovered") }
	switch sub {
	case "reset":
		for _, f := range []string{"--hard", "--merge", "--keep"} {
			if slices.Contains(rest, f) {
				discard("reset " + f)
			}
		}
	case "checkout":
		if checkoutDiscards(rest, dir) {
			discard("checkout, used this way,")
		}
	case "switch":
		if slices.ContainsFunc(rest, func(a string) bool {
			return a == "--discard-changes" || a == "--force" || shortFlag(a, 'f', "cC")
		}) {
			discard("switch with --discard-changes or --force")
		}
	case "restore":
		staged := slices.ContainsFunc(rest, func(a string) bool { return a == "--staged" || shortFlag(a, 'S', "s") })
		worktree := slices.ContainsFunc(rest, func(a string) bool { return a == "--worktree" || shortFlag(a, 'W', "s") })
		if !staged || worktree {
			discard("restore on the working tree")
		}
	case "clean":
		dry := slices.ContainsFunc(rest, func(a string) bool { return a == "--dry-run" || shortFlag(a, 'n', "e") })
		force := slices.ContainsFunc(rest, func(a string) bool { return a == "--force" || shortFlag(a, 'f', "e") })
		force = force || slices.ContainsFunc(configs, func(c string) bool { return strings.HasPrefix(c, "clean.requireforce=") })
		if force && !dry {
			discard("clean -f")
		}
	case "branch":
		del := slices.ContainsFunc(rest, func(a string) bool { return a == "--delete" || shortFlag(a, 'd', "") })
		force := slices.ContainsFunc(rest, func(a string) bool { return a == "--force" || shortFlag(a, 'f', "") })
		if (del && force) || slices.ContainsFunc(rest, func(a string) bool { return shortFlag(a, 'D', "") }) {
			discard("branch -D")
		}
	case "stash":
		if len(rest) > 0 && (rest[0] == "drop" || rest[0] == "clear") {
			discard("stash " + rest[0])
		}
	case "commit":
		for j := 0; j < len(rest); j++ {
			a := rest[j]
			if slices.Contains(commitValued, a) {
				j++
				continue
			}
			if shortFlag(a, 'n', "mFcCtuS") {
				why = appendNew(why, "git commit --no-verify skips the owner's hooks")
			}
		}
	case "config":
		if configSetsHooksPath(rest) {
			why = append(why, "git core.hooksPath skips the owner's hooks")
		}
	}
	return why
}

// gitWordsMatter reports whether an argument of sub that expands could be what a rule above looks for. A commit message's
// value can't be.
func gitWordsMatter(sub string, rest []string) bool {
	if !slices.Contains(noVerifySubcommands, sub) && !slices.Contains([]string{"reset", "checkout", "switch", "restore", "clean", "branch", "stash", "config"}, sub) {
		return false
	}
	for j := 0; j < len(rest); j++ {
		if sub == "commit" && slices.Contains(commitValued, rest[j]) {
			j++
			continue
		}
		if rest[j] == unknownWord {
			return true
		}
	}
	return false
}

// commitValued are git commit's flags whose next word is a value, which a message could spell like a flag.
var commitValued = []string{"-m", "-F", "-c", "-C", "-t", "--message", "--file", "--template"}

// checkoutDiscards reports whether git checkout's arguments overwrite the working tree: paths after --, a path that
// exists, ".", or -f. A new branch's name is not a path.
func checkoutDiscards(args []string, dir string) bool {
	creating := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			return i+1 < len(args)
		case a == "--force" || shortFlag(a, 'f', "bB") || strings.HasPrefix(a, "--pathspec-from-file"):
			return true
		case a == "-b" || a == "-B" || a == "--orphan":
			creating = true
			i++
		case strings.HasPrefix(a, "-"), a == unknownWord:
		case a == ".":
			return true
		case !creating:
			if _, err := os.Lstat(filepath.Join(dir, a)); err == nil {
				return true
			}
		}
	}
	return false
}

// configSetsHooksPath reports whether git config's arguments change core.hooksPath. Reading it changes nothing.
func configSetsHooksPath(args []string) bool {
	var words []string
	changes := false
	for _, a := range args {
		switch {
		case a == "--get" || a == "--get-all" || a == "--get-regexp" || a == "--list" || a == "-l":
			return false
		case a == "--unset" || a == "--unset-all" || a == "--add" || a == "--replace-all":
			changes = true
		case !strings.HasPrefix(a, "-"):
			words = append(words, a)
		}
	}
	if len(words) > 0 && slices.Contains([]string{"get", "list"}, words[0]) {
		return false
	}
	if len(words) > 0 && slices.Contains([]string{"set", "unset"}, words[0]) {
		changes = true
		words = words[1:]
	}
	for i, w := range words {
		if strings.EqualFold(w, "core.hooksPath") {
			return changes || i+1 < len(words)
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
		if t == unknownWord {
			if sc.nested { // it could be a flag, a directory or the hooks
				why = appendNew(why, expandsLater("an argument of rm"))
			}
			continue
		}
		p := t
		if !filepath.IsAbs(p) {
			p = filepath.Join(dir, p)
		}
		p = filepath.Clean(p)
		if inHooks(p) {
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
	if !within(root, real) {
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
	return slices.Contains(rebuilt, first) && real == filepath.Join(resolveExisting(filepath.Clean(dir)), rel)
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
	return why
}

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
	start := []string{name + " starts another agent, which runs outside this session's checks"}
	if len(args) == 0 {
		return start
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

// assignAsks is HooksOff for an assignment whose value is fixed. In nested code, it asks for one that expands.
func assignAsks(a *syntax.Assign, nested bool) []string {
	if a.Name == nil || a.Value == nil {
		return nil
	}
	v, ok := literal(a.Value)
	if !ok {
		if nested && (a.Name.Value == "HUSKY" || a.Name.Value == "HUSKY_SKIP_HOOKS") {
			return []string{expandsLater(a.Name.Value)}
		}
		return nil
	}
	if r := HooksOff(a.Name.Value, v); r != "" {
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
