package perms

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Simple is one simple command of a shell command line, as approval sees it: its program resolved to an absolute
// path, and its arguments. Args holds an argument's literal text, or ok=false in Literal where it expands at run time.
type Simple struct {
	Program string   // absolute, symlinks resolved; "" for a builtin
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

// wrappers run the program named by their first argument that is not a flag; approval looks through them.
var wrappers = map[string]bool{"exec": true, "command": true, "builtin": true, "nohup": true, "time": true}

// checkShell parses a command line and returns each simple command it holds, and why it needs the user, one reason per
// cause: a simple command no rule allows, one that uses a flag that runs another program, a program or redirection that
// expands at run time, an output redirection outside the workspace, or code run from a string. Every simple command
// counts, inside pipelines, lists, subshells and substitutions alike (DESIGN §6.6, "What is approved is what runs").
//
// The only check here of where a command writes is its redirections. What the programs themselves write (go build
// -o, a test writing a file) is not checked here; the sandbox enforces it.
func (e *Engine) checkShell(command string) ([]Simple, []string) {
	f, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil {
		return nil, []string{"the command does not parse: " + err.Error()}
	}
	var cmds []Simple
	var why []string
	syntax.Walk(f, func(n syntax.Node) bool {
		switch n := n.(type) {
		case *syntax.CallExpr:
			if len(n.Args) == 0 { // assignments only
				return true
			}
			s, reasons := e.simple(n.Args)
			if s != nil {
				cmds = append(cmds, *s)
			}
			why = append(why, reasons...)
		case *syntax.Redirect:
			why = append(why, e.redirect(n)...)
		}
		return true
	})
	return cmds, why
}

// simple resolves one simple command's program, looking through wrappers, and checks it against the rules.
func (e *Engine) simple(words []*syntax.Word) (*Simple, []string) {
	for len(words) > 0 {
		name, ok := literal(words[0])
		if !ok {
			return nil, []string{fmt.Sprintf("the program %s expands at run time", source(words[0]))}
		}
		switch {
		case safeBuiltins[name]:
			s := Simple{Builtin: name}
			s.Args, s.Literal = args(words[1:])
			return &s, nil
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
		prog, err := e.resolve(name)
		if err != nil {
			return nil, []string{err.Error()}
		}
		s := Simple{Program: prog}
		s.Args, s.Literal = args(words[1:])
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

// redirect checks an output redirection's target: it must be literal and inside the workspace, or /dev/null. It sees
// the shell's own redirections only; where the programs write is the sandbox's to enforce.
func (e *Engine) redirect(r *syntax.Redirect) []string {
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
		p = filepath.Join(e.Root, p)
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
