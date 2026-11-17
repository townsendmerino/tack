package perms

import (
	"path/filepath"
	"slices"
	"testing"
)

// weirdArgs are arguments whose text, written without quoting, would read back as something else: nothing, a comment, several words,
// a glob, an expansion, a quote.
var weirdArgs = []string{"", " ", "  ", "#x", "a b", "*", "?", "[a]", "$HOME", "$(id)", "`id`", "a'b", `a"b`, "a\\b", "a\nb", "a\tb", "~", "~/x",
	"a;b", "a&b", "a|b", "a>b", "a<b", "(", ")", "{a,b}", "x=y", "-", "--", "-x", "é", "日本語", "a'", "'", "''", `"`, "!", "a#b", "#"}

// TestARuleReadsBackAsItWasWritten: for every rule, ParseRule(r.String()) is r, so a rule that is written down (the shell child gets
// its rules as text) is the rule that was decided. Before, "go ”" read back as "go" and "go '#x'" as "go", so a rule for one command
// became a rule for the whole program.
func TestARuleReadsBackAsItWasWritten(t *testing.T) {
	progs := []string{"/usr/bin/go", "/opt/My Tools/go", "/usr/bin/a'b", "go", "a=b"}
	for _, prog := range progs {
		for _, a := range weirdArgs {
			for _, r := range []Rule{{Program: prog, Args: []string{a}}, {Program: prog, Args: []string{"vet", a}}, {Program: prog, Args: []string{a, "build"}}} {
				got, err := ParseRule(r.String())
				if err != nil || got.Program != r.Program || !slices.Equal(got.Args, r.Args) {
					t.Errorf("%q written as %q reads back as %+v, %v", r, r.String(), got, err)
				}
			}
		}
	}
	if r := (Rule{Program: "/usr/bin/go", Args: []string{"test", "./..."}}); r.String() != "/usr/bin/go test ./..." {
		t.Errorf("a plain rule is not written plainly: %q", r.String())
	}
}

// TestAllowForSessionAddsOnlyRulesThatReadBack: whatever AllowForSession adds reads back as it is, so what the user is told was added, the
// session's engine and the shell child's engine are one rule; a command whose rule could not be written back adds none.
func TestAllowForSessionAddsOnlyRulesThatReadBack(t *testing.T) {
	for _, a := range weirdArgs {
		e := &Engine{}
		e.AllowForSession("bash", []Simple{{Program: "/usr/bin/go", Name: "go", Args: []string{a, "build"}, Literal: []bool{true, true}}})
		for _, r := range e.Rules {
			got, err := ParseRule(r.String())
			if err != nil || got.Program != r.Program || !slices.Equal(got.Args, r.Args) {
				t.Errorf("first argument %q: added %+v, which reads back as %+v, %v", a, r, got, err)
			}
		}
	}
}

// childEngine is what the shell child builds from the session engine's rules: each written down and read back, a rule that does not
// read back dropped (internal/shell).
func childEngine(e *Engine) *Engine {
	c := &Engine{Root: e.Root, Path: e.Path, Sandboxed: e.Sandboxed}
	for _, r := range e.Rules {
		if got, err := ParseRule(r.String()); err == nil {
			c.Rules = append(c.Rules, got)
		}
	}
	return c
}

// TestAllowingForTheSessionDoesNotWidenInTheShellChild: allowing `go ” build` for the session allows go with an empty first
// argument, in the session and in the child, and not `go build` or `go` alone, which a rule that read back as the bare program
// would have allowed. The same for an argument that is a comment, a space, a word with a space, and a glob.
func TestAllowingForTheSessionDoesNotWidenInTheShellChild(t *testing.T) {
	prog, err := filepath.EvalSymlinks("/bin/sh") // a program that exists, which the engine resolves the way approval did
	if err != nil {
		t.Skip("no /bin/sh")
	}
	for _, a := range []string{"", " ", "#x", "a b", "*", "$HOME", "a'b"} {
		e := &Engine{Sandboxed: true, Root: t.TempDir()}
		written := Simple{Program: prog, Name: "sh", Args: []string{a, "build"}, Literal: []bool{true, true}}
		e.AllowForSession("bash", []Simple{written})
		child := childEngine(e)
		for name, eng := range map[string]*Engine{"session": e, "child": child} {
			if !eng.allowed(written) {
				t.Errorf("first argument %q: the %s does not allow what was approved", a, name)
			}
			for _, wider := range []Simple{
				{Program: prog, Name: "sh"},
				{Program: prog, Name: "sh", Args: []string{"build"}, Literal: []bool{true}},
				{Program: prog, Name: "sh", Args: []string{"vet"}, Literal: []bool{true}},
			} {
				if eng.allowed(wider) {
					t.Errorf("first argument %q: the %s allows %+v, which was not approved", a, name, wider)
				}
			}
		}
	}
}

// TestARuleThatCannotBeWrittenBackIsNeverOfferedOrAdded: an argument with a NUL byte is not something the shell parser reads back, so a
// rule with it does not read back as itself; SessionRules leaves it out and AllowForSession adds nothing, so the command keeps
// asking, rather than the child dropping the rule or reading another.
func TestARuleThatCannotBeWrittenBackIsNeverOfferedOrAdded(t *testing.T) {
	r := Rule{Program: "/usr/bin/go", Args: []string{"a\x00b"}}
	if r.readsBack() {
		t.Skip("the parser reads a NUL back; no argument is known that cannot be written back")
	}
	parsed := []Simple{{Program: "/usr/bin/go", Name: "go", Args: []string{"a\x00b", "x"}, Literal: []bool{true, true}}}
	if got := SessionRules("bash", parsed); len(got) != 0 {
		t.Errorf("offered %+v", got)
	}
	e := &Engine{}
	if added := e.AllowForSession("bash", parsed); len(added) != 0 || len(e.Rules) != 0 {
		t.Errorf("added %v, rules %+v", added, e.Rules)
	}
}
