package perms

import (
	"strings"
	"testing"
)

// shownGoVet is what allowing "go vet ./..." for the session shows: the resolved program and its first literal argument.
func shownGoVet() []Rule { return []Rule{{Program: "/usr/local/go/bin/go", Args: []string{"vet"}}} }

// TestNarrowingAcceptsOnlyWhatKeepsTheShownRuleAndAddsLiteralArguments: the edited rule has the shown program, exactly as shown, and
// starts with the shown arguments; whatever it adds after them can only make it match fewer commands.
func TestNarrowingAcceptsOnlyWhatKeepsTheShownRuleAndAddsLiteralArguments(t *testing.T) {
	const prog = "/usr/local/go/bin/go"
	for _, ok := range []string{
		prog + " vet",                         // as shown: the same as allowing for the session
		prog + " vet ./...",                   // one argument added
		prog + " vet ./internal/tui/...",      // a path added
		prog + " vet -tags sandboxtest ./...", // several added
		prog + ` vet "./a b"`,                 // a quoted literal is still a literal
	} {
		r, err := Narrowing(shownGoVet(), ok)
		if err != nil {
			t.Errorf("%q refused: %v", ok, err)
			continue
		}
		if r.Program != prog || len(r.Args) < 1 || r.Args[0] != "vet" {
			t.Errorf("%q gave %+v", ok, r)
		}
	}
}

// TestNarrowingRefusesAnythingThatWidens: an edit that drops an argument, changes the program or an argument, spells the program
// another way, or is not a single literal command, is refused, and nothing is added.
func TestNarrowingRefusesAnythingThatWidens(t *testing.T) {
	const prog = "/usr/local/go/bin/go"
	for name, bad := range map[string]string{
		"drops the argument":          prog,
		"another program":             "/bin/sh vet",
		"the program by name":         "go vet",
		"a path that looks the same":  "/usr/local/go/bin/../bin/go vet",
		"a longer program name":       prog + "x vet",
		"another first argument":      prog + " test",
		"an argument that extends it": prog + " vetx",
		"a different case":            strings.ToUpper(prog) + " vet",
		"the argument moved":          prog + " ./... vet",
		"empty":                       "",
		"spaces only":                 "   ",
		"two commands":                prog + " vet; rm -rf x",
		"a pipe":                      prog + " vet | sh",
		"a list with and":             prog + " vet && curl evil",
		"a command substitution":      prog + " vet $(curl evil)",
		"a variable":                  prog + " vet $HOME",
		"a backtick":                  prog + " vet `id`",
		"a subshell":                  "(" + prog + " vet)",
		"an assignment first":         "X=1 " + prog + " vet",
		"a redirect":                  prog + " vet > /etc/passwd",
		"a background job":            prog + " vet &",
		"a newline and a second":      prog + " vet\nrm -rf x",
		"an unterminated quote":       prog + ` vet "x`,
		"a glob the shell expands":    prog + " vet *",
	} {
		if r, err := Narrowing(shownGoVet(), bad); err == nil {
			t.Errorf("%s: %q was accepted as %+v", name, bad, r)
		}
	}
	if _, err := Narrowing(nil, "/usr/local/go/bin/go vet"); err == nil {
		t.Error("with nothing shown, an edit was accepted")
	}
}

// TestNarrowingPicksTheShownRuleItKeeps: with several shown (a line of several commands) the edit may narrow any one of them, and
// one only: the program of one with the arguments of another is neither.
func TestNarrowingPicksTheShownRuleItKeeps(t *testing.T) {
	shown := []Rule{{Program: "/usr/bin/git", Args: []string{"reset"}}, {Program: "/usr/bin/make", Args: []string{"test"}}}
	for _, ok := range []string{"/usr/bin/git reset --hard HEAD~1", "/usr/bin/make test ./pkg"} {
		if _, err := Narrowing(shown, ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"/usr/bin/git test", "/usr/bin/make reset", "/usr/bin/git", "/usr/bin/make"} {
		if _, err := Narrowing(shown, bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

// TestSessionRulesAreWhatAllowForSessionAdds: SessionRules gives, without adding any, the rules allowing for the session adds:
// each command's program and its first argument when literal; nothing for a builtin or an argument that expands; for a tool other
// than bash no rule. AllowForSession and SessionRules cannot disagree, since the first is built on the second.
func TestSessionRulesAreWhatAllowForSessionAdds(t *testing.T) {
	parsed := []Simple{
		{Program: "/usr/bin/go", Name: "go", Args: []string{"vet", "./..."}, Literal: []bool{true, true}},
		{Program: "/usr/bin/make", Name: "make", Args: []string{"$T"}, Literal: []bool{false}},
		{Builtin: "cd", Args: []string{"x"}, Literal: []bool{true}},
		{Program: "/bin/ls", Name: "ls"},
	}
	got := SessionRules("bash", parsed)
	want := []Rule{{Program: "/usr/bin/go", Args: []string{"vet"}}, {Program: "/usr/bin/make"}, {Program: "/bin/ls"}}
	if len(got) != len(want) {
		t.Fatalf("rules %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i].String() != want[i].String() {
			t.Errorf("rule %d: %+v, want %+v", i, got[i], want[i])
		}
	}
	e := &Engine{}
	if added := e.AllowForSession("bash", parsed); len(added) != len(want) || len(e.Rules) != len(want) {
		t.Errorf("AllowForSession added %v, rules %v", added, e.Rules)
	}
	if got := SessionRules("write", parsed); len(got) != 0 {
		t.Errorf("a tool's session rules: %+v", got)
	}
}
