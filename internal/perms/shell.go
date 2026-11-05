package perms

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Simple is one simple command of a shell command line, as approval sees it: its program resolved to an absolute
// path, and its arguments. Args holds an argument's literal text, or ok=false in Literal where it expands at run time.
type Simple struct {
	Program string   // absolute, symlinks resolved; "" for a builtin
	Name    string   // the program as written, which a multi-call binary goes by
	Builtin string   // the builtin's name, for a builtin
	Args    []string // the arguments after the program, as written
	Literal []bool   // Literal[i] is false when Args[i] expands at run time
}

func (s Simple) String() string {
	name := s.Program
	if s.Builtin != "" {
		name = s.Builtin
	}
	return strings.TrimSpace(name + " " + strings.Join(s.Args, " "))
}

// safeBuiltins run inside tack's shell, change nothing outside it, and need no rule.
var safeBuiltins = map[string]bool{
	"cd": true, "pwd": true, "echo": true, "printf": true, "true": true, "false": true, "test": true, "[": true,
	"export": true, "unset": true, "set": true, "shift": true, "exit": true, "return": true, "read": true,
	"local": true, "declare": true, "wait": true, "type": true, ":": true, "break": true, "continue": true,
}

// stringRunners run code given as a string, which no rule can cover.
var stringRunners = map[string]bool{"eval": true, "source": true, ".": true, "trap": true}

// runsPrograms are, by program name, the flags that make it run another program it is given: go test -exec, go build
// -toolexec, find -exec, git -c core.fsmonitor=…. A rule for the program never covers them: a command using one asks,
// and so does one with an argument that expands at run time, which could become one of them.
var runsPrograms = map[string][]string{
	"go":   {"-exec", "-toolexec", "-vettool"},
	"git":  {"-c", "--config-env", "--exec-path", "--upload-pack", "--receive-pack"},
	"find": {"-exec", "-execdir", "-ok", "-okdir"},
}

// runFlag returns the flag in arg that makes prog run another program, or "". Go flags take one dash or two, and any
// flag may carry its value after "=".
func runFlag(prog, arg string) string {
	for _, f := range runsPrograms[filepath.Base(prog)] {
		forms := []string{f}
		if filepath.Base(prog) == "go" {
			forms = append(forms, "-"+f)
		}
		for _, form := range forms {
			if arg == form || strings.HasPrefix(arg, form+"=") {
				return f
			}
		}
	}
	return ""
}

// changesPrograms are environment variables that change what a covered program runs or loads: go's flags, toolchain
// and cache program; git's config, ssh, pager, editor and helpers; the shells' startup files; the dynamic loader's
// preloads. Setting one is never covered by a rule (go test with GOFLAGS=-toolexec=… is go test -toolexec), and a
// program runs with them only as approved (internal/shell).
var changesPrograms = map[string]bool{
	"GOFLAGS": true, "GOENV": true, "GOTOOLCHAIN": true, "GOCACHEPROG": true, "CC": true, "CXX": true,
	"GIT_CONFIG": true, "GIT_CONFIG_COUNT": true, "GIT_CONFIG_GLOBAL": true, "GIT_CONFIG_SYSTEM": true,
	"GIT_CONFIG_PARAMETERS": true, "GIT_SSH": true, "GIT_SSH_COMMAND": true, "GIT_EXTERNAL_DIFF": true,
	"GIT_EXEC_PATH": true, "GIT_ASKPASS": true, "SSH_ASKPASS": true, "GIT_EDITOR": true, "GIT_PAGER": true,
	"PAGER": true, "EDITOR": true, "VISUAL": true, "BASH_ENV": true, "ENV": true,
	"LD_PRELOAD": true, "LD_LIBRARY_PATH": true, "LD_AUDIT": true,
}

// changesProgramsPrefix are the families of such variables: git's numbered config pairs, and macOS's dynamic loader.
var changesProgramsPrefix = []string{"GIT_CONFIG_KEY_", "GIT_CONFIG_VALUE_", "DYLD_"}

// ChangesPrograms reports whether the environment variable name changes what a covered program runs or loads.
func ChangesPrograms(name string) bool {
	if changesPrograms[name] {
		return true
	}
	for _, p := range changesProgramsPrefix {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

func setsVariable(name string) string {
	return fmt.Sprintf("setting %s changes what programs run, which no rule covers", name)
}

// assignment checks one assignment, in any of its forms (a prefix, a plain assignment, export, declare, local).
func assignment(a *syntax.Assign) []string {
	if a.Name == nil { // a declaration's argument evaluated at run time: an option, or a name from an expansion
		if a.Value != nil {
			if v, ok := literal(a.Value); ok && strings.HasPrefix(v, "-") {
				return nil
			}
			return []string{fmt.Sprintf("a declaration of %s expands at run time and could set a variable that "+
				"changes what programs run", source(a.Value))}
		}
		return nil
	}
	if ChangesPrograms(a.Name.Value) {
		return []string{setsVariable(a.Name.Value)}
	}
	return nil
}

// wrappers run the program named by their first argument that is not a flag; approval looks through them.
var wrappers = map[string]bool{"exec": true, "command": true, "builtin": true, "nohup": true, "time": true}

// checkShell parses a command line and returns each simple command it holds, and why it needs the user, one reason per
// cause: a simple command no rule allows, one that uses a flag that runs another program, a program or redirection that
// expands at run time, an output redirection outside the workspace, or code run from a string. Every simple command
// counts, inside pipelines, lists, subshells and substitutions alike (DESIGN §6.6, "What is approved is what runs").
//
// The only check here of where a command writes is its redirections. What the programs themselves write (go build
// -o, a test writing a file) is not checked here; the sandbox enforces it.
//
// always is what asks in every mode (AlwaysAsks, PathAsks), whatever the rules say.
func (e *Engine) checkShell(command string) (cmds []Simple, why, always []string) {
	f, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil {
		reason := []string{"the command does not parse: " + err.Error()}
		return nil, reason, reason
	}
	for _, name := range sortedKeys(e.SessionEnv) {
		if v := e.SessionEnv[name]; ChangesPrograms(name) && v != os.Getenv(name) {
			why = append(why, fmt.Sprintf("%s=%s was set by an earlier command in this session and changes what programs "+
				"run, which no rule covers", name, v))
		}
	}
	walkDirs(f, e.startDir(), func(n syntax.Node, dir string, _ bool) {
		switch n := n.(type) {
		case *syntax.Assign:
			why = append(why, assignment(n)...)
		case *syntax.WordIter:
			if n.Name != nil && ChangesPrograms(n.Name.Value) {
				why = append(why, setsVariable(n.Name.Value))
			}
		case *syntax.CallExpr:
			if len(n.Args) == 0 { // assignments only
				return
			}
			s, reasons := e.simple(n.Args, dir)
			if s != nil {
				cmds = append(cmds, *s)
			}
			why = append(why, reasons...)
		case *syntax.Redirect:
			why = append(why, e.redirect(n, dir)...)
		}
	})
	always = e.scanAsks(f, scan{dir: e.startDir()})
	// What an earlier command exported: it reaches every program this one runs, unless unset first. Builtins run none, so unset gets out.
	for _, name := range sortedKeys(e.SessionEnv) {
		if v := e.SessionEnv[name]; v != os.Getenv(name) && runsProgramWith(f, name) {
			if r := HooksOff(name, v); r != "" {
				always = appendNew(always, r)
			}
		}
	}
	return cmds, why, always
}

// simple resolves one simple command's program from dir, where it runs, looking through wrappers, and checks it against
// the rules.
func (e *Engine) simple(words []*syntax.Word, dir string) (*Simple, []string) {
	for len(words) > 0 {
		name, ok := literal(words[0])
		if !ok {
			return nil, []string{fmt.Sprintf("the program %s expands at run time", source(words[0]))}
		}
		switch {
		case safeBuiltins[name]:
			s := Simple{Builtin: name}
			s.Args, s.Literal = args(words[1:])
			return &s, namedVariables(name, s)
		case stringRunners[name]:
			return nil, []string{fmt.Sprintf("%s runs code from a string, which no rule covers", name)}
		case wrappers[name]:
			words = words[1:]
			for len(words) > 0 { // skip the wrapper's own flags
				if a, ok := literal(words[0]); ok && strings.HasPrefix(a, "-") {
					words = words[1:]
					continue
				}
				break
			}
			continue
		}
		prog, err := e.resolveFrom(name, dir)
		if err != nil {
			return nil, []string{err.Error()}
		}
		s := Simple{Program: prog, Name: name}
		s.Args, s.Literal = args(words[1:])
		if filepath.Base(prog) == "env" {
			for i, a := range s.Args {
				name, _, isAssign := strings.Cut(a, "=")
				switch {
				case !s.Literal[i]:
					return &s, []string{fmt.Sprintf("env's argument %s expands at run time and could set a variable that "+
						"changes what programs run", a)}
				case isAssign && ChangesPrograms(name):
					return &s, []string{setsVariable(name)}
				}
			}
		}
		if _, ok := runsPrograms[filepath.Base(prog)]; ok {
			var why []string
			for i, a := range s.Args {
				switch {
				case !s.Literal[i]:
					why = append(why, fmt.Sprintf("%s's argument %s expands at run time and could be a flag that runs "+
						"another program (%s), which no rule covers", filepath.Base(prog), a,
						strings.Join(runsPrograms[filepath.Base(prog)], ", ")))
				case runFlag(prog, a) != "":
					why = append(why, fmt.Sprintf("%s %s runs another program, which no rule covers",
						filepath.Base(prog), runFlag(prog, a)))
				}
			}
			if len(why) > 0 {
				return &s, why
			}
		}
		if !e.allowed(s) {
			return &s, []string{"no rule allows " + s.String()}
		}
		return &s, nil
	}
	return nil, nil
}

// redirect checks an output redirection's target, read from dir: it must be literal and inside the workspace, or /dev/null. It
// sees the shell's own redirections only; where the programs write is the sandbox's to enforce.
func (e *Engine) redirect(r *syntax.Redirect, dir string) []string {
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
		return []string{fmt.Sprintf("a redirect to %s, which expands at run time", source(r.Word))}
	}
	if target == "/dev/null" {
		return nil
	}
	p := target
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	if !within(e.root(), resolveExisting(filepath.Clean(p))) {
		return []string{"a redirection writes outside the repository: " + target + " (this checks the shell's " +
			"redirections only; the sandbox enforces where programs write)"}
	}
	return nil
}

// resolve returns a program's absolute path with symlinks resolved: a name is looked up on the engine's PATH, a path
// is taken relative to the workspace root.
func (e *Engine) resolve(name string) (string, error) {
	var p string
	switch {
	case strings.HasPrefix(name, "~"):
		return "", fmt.Errorf("the program %s expands at run time", name)
	case strings.Contains(name, "/"):
		p = name
		if !filepath.IsAbs(p) {
			p = filepath.Join(e.Root, p)
		}
	default:
		var err error
		if p, err = lookPath(name, e.Path); err != nil {
			return "", fmt.Errorf("%s is not a program on PATH", name)
		}
	}
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", fmt.Errorf("%s is not a program tack can find", name)
	}
	return real, nil
}

// runsProgramWith reports whether code may run a program, resolved or not, with the session's value of a variable: any simple command but
// a builtin that needs no rule, before an unset of it that runs in order in this shell.
func runsProgramWith(f *syntax.File, name string) bool {
	decided, runs := false, false
	var path []syntax.Node
	syntax.Walk(f, func(n syntax.Node) bool {
		if n == nil {
			path = path[:len(path)-1]
			return true
		}
		if _, ok := n.(*syntax.FuncDecl); ok {
			return false // its body runs where it's called, and calling it counts
		}
		if c, ok := n.(*syntax.CallExpr); ok && !decided && len(c.Args) > 0 {
			words := c.Args
			lookup := false // command -v looks a program up, and runs nothing
			for len(words) > 1 {
				w, ok := literal(words[0])
				if !ok || (w != "builtin" && w != "command" && !strings.HasPrefix(w, "-")) {
					break
				}
				lookup = lookup || w == "-v" || w == "-V"
				words = words[1:]
			}
			w, ok := literal(words[0])
			switch {
			case lookup:
			case !ok || !safeBuiltins[w] && w != "unset":
				decided, runs = true, true
			case w == "unset" && unsets(words[1:], name) && inThisShell(path, c):
				decided = true
			}
		}
		path = append(path, n)
		return true
	})
	return runs
}

// inThisShell reports whether a command runs in order, and in this shell rather than a copy of it.
func inThisShell(path []syntax.Node, c *syntax.CallExpr) bool {
	if !cdPlaced(path, c) {
		return false
	}
	return !slices.ContainsFunc(path, func(n syntax.Node) bool {
		switch n.(type) {
		case *syntax.Subshell, *syntax.CmdSubst:
			return true
		}
		return false
	})
}

// unsets reports whether unset's arguments plainly clear a variable. An argument that expands may be -f, which clears a function instead, and a
// flag other than -v may make it fail.
func unsets(args []*syntax.Word, name string) bool {
	found := false
	for _, w := range args {
		a, ok := literal(w)
		if !ok || (strings.HasPrefix(a, "-") && a != "-v") {
			return false
		}
		found = found || a == name
	}
	return found
}

// resolveFrom is resolve for a command running in dir: a relative path means what it means there.
func (e *Engine) resolveFrom(name, dir string) (string, error) {
	if strings.Contains(name, "/") && !filepath.IsAbs(name) && !strings.HasPrefix(name, "~") {
		name = filepath.Join(dir, name)
	}
	return e.resolve(name)
}

// allowed reports whether a rule covers s: the same resolved program, and s's first arguments equal to the rule's,
// each literal.
func (e *Engine) allowed(s Simple) bool {
	for _, r := range e.Rules {
		prog, err := e.resolve(r.Program)
		if err != nil || prog != s.Program || len(s.Args) < len(r.Args) {
			continue
		}
		match := true
		for i, a := range r.Args {
			if !s.Literal[i] || s.Args[i] != a {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// literal returns a word's text when it is fixed before the shell runs it: plain text and quoted text without
// expansions. A word with a parameter, command or arithmetic expansion, an unquoted glob, or a leading tilde is not.
func literal(w *syntax.Word) (string, bool) {
	var b strings.Builder
	for i, part := range w.Parts {
		switch p := part.(type) {
		case *syntax.Lit:
			if strings.ContainsAny(p.Value, "*?[") || (i == 0 && strings.HasPrefix(p.Value, "~")) {
				return "", false
			}
			b.WriteString(p.Value)
		case *syntax.SglQuoted:
			b.WriteString(p.Value)
		case *syntax.DblQuoted:
			for _, q := range p.Parts {
				lit, ok := q.(*syntax.Lit)
				if !ok {
					return "", false
				}
				b.WriteString(lit.Value)
			}
		default:
			return "", false
		}
	}
	return b.String(), true
}

func args(words []*syntax.Word) ([]string, []bool) {
	out, lit := make([]string, len(words)), make([]bool, len(words))
	for i, w := range words {
		if s, ok := literal(w); ok {
			out[i], lit[i] = s, true
		} else {
			out[i] = source(w)
		}
	}
	return out, lit
}

// source prints a word as written.
func source(w *syntax.Word) string {
	var b strings.Builder
	syntax.NewPrinter().Print(&b, w)
	return b.String()
}

// lookPath finds name on a PATH given as a string, as exec.LookPath does on the process's own.
func lookPath(name, path string) (string, error) {
	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			continue
		}
		p := filepath.Join(dir, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return "", exec.ErrNotFound
}

// resolveExisting resolves symlinks in the longest existing prefix of p and joins the rest.
func resolveExisting(p string) string {
	dir, rest := p, []string(nil)
	for {
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(append([]string{real}, rest...)...)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return p
		}
		rest = append([]string{filepath.Base(dir)}, rest...)
		dir = parent
	}
}

func within(root, p string) bool {
	return p == root || strings.HasPrefix(p, root+string(filepath.Separator))
}

// Analyze returns a command line's simple commands as approval sees them, without judging them.
func (e *Engine) Analyze(command string) []Simple {
	cmds, _, _ := e.checkShell(command)
	return cmds
}

// HasPipe reports whether command, as it parses, has a pipe in it, or doesn't parse. A | in a quoted word or a || is none.
func HasPipe(command string) bool {
	f, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil {
		return true
	}
	found := false
	syntax.Walk(f, func(n syntax.Node) bool {
		if b, ok := n.(*syntax.BinaryCmd); ok && (b.Op == syntax.Pipe || b.Op == syntax.PipeAll) {
			found = true
		}
		return !found
	})
	return found
}

// Covers reports whether a rule allows s without asking: the engine must run its commands inside the sandbox, a rule
// must match, and no argument may be, or expand into, a flag that runs another program.
func (e *Engine) Covers(s Simple) bool {
	if !e.Sandboxed || s.Program == "" {
		return false
	}
	if _, ok := runsPrograms[filepath.Base(s.Program)]; ok {
		for i, a := range s.Args {
			if !s.Literal[i] || runFlag(s.Program, a) != "" {
				return false
			}
		}
	}
	return e.allowed(s)
}

// RunFlag returns the flag in arg that makes prog (an absolute path) run another program, or "".
func RunFlag(prog, arg string) string { return runFlag(prog, arg) }

// Outside reports whether p, absolute, lies outside root once the symlinks of its existing prefix are resolved.
func Outside(root, p string) bool {
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	return !within(root, resolveExisting(filepath.Clean(p)))
}

// IsSecret is the default read deny-list (DESIGN §6.6): .env files, keys and certificates, and the credential
// directories a workspace should never contain but might link to.
func IsSecret(p string) bool {
	base := filepath.Base(p)
	if base == ".env" || strings.HasPrefix(base, ".env.") {
		return true
	}
	for _, ext := range []string{".pem", ".key", ".p12", ".pfx"} {
		if strings.HasSuffix(base, ext) {
			return true
		}
	}
	for seg := range strings.SplitSeq(filepath.ToSlash(p), "/") {
		if seg == ".ssh" || seg == ".aws" || seg == ".gnupg" {
			return true
		}
	}
	return false
}

// namedVariables checks the builtins that set a variable named by an argument: read's names, and printf -v's.
func namedVariables(builtin string, s Simple) []string {
	var names []string
	switch builtin {
	case "read":
		names = s.Args
	case "printf":
		for i := 0; i+1 < len(s.Args); i++ {
			if s.Args[i] == "-v" {
				names = append(names, s.Args[i+1])
			}
		}
	default:
		return nil
	}
	var why []string
	for i, n := range names {
		if builtin == "read" && !s.Literal[i] {
			why = append(why, fmt.Sprintf("read's argument %s expands at run time and could name a variable that "+
				"changes what programs run", n))
		} else if ChangesPrograms(n) {
			why = append(why, setsVariable(n))
		}
	}
	return why
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
