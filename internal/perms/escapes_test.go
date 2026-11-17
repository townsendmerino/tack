package perms

import (
	"strings"
	"testing"

	"mvdan.cc/sh/v3/syntax"

	"github.com/townsendmerino/tack/internal/proto"
)

// decide is the verdict for a command in ask mode inside the sandbox, with the engine's rules (go test, git status).
func decide(e *Engine, cmd string) Verdict { return e.Decide(proto.ModeAsk, "bash", false, bash(cmd)) }

// TestAWordTheShellDecodesDifferentlyIsNeverCovered: bash decodes backslash escapes, $'...' (ANSI-C) strings, a carriage return and
// brace expansion before it runs a word, and the analyzer read these words raw. With the rule `go test`, a flag that runs a program
// (-exec) written any of these ways was seen as another word, so the command was covered and ran without a question. Each of
// these must ask, whatever the rule says: when the parse cannot see exactly what will run, it asks.
func TestAWordTheShellDecodesDifferentlyIsNeverCovered(t *testing.T) {
	e := engine(t, true)
	for _, cmd := range []string{
		`go test \-exec id`,      // an unquoted backslash: bash passes -exec
		`go test -e\xec id`,      // a backslash inside the flag
		`go test $'\x2dexec' id`, // ANSI-C hex
		`go test $'\055exec' id`, // ANSI-C octal
		`go test $'-exec' id`,    // ANSI-C with nothing to decode is still a different kind of word
		`go test $'-exec' id`,    // ANSI-C unicode
		`go test "\-exec" id`,    // a backslash in double quotes: bash keeps it here, but a \" or \$ would not, so none is trusted
		`go test -e{x,x}ec id`,   // brace expansion: bash passes -exec -exec
		`go test {-exec,x} id`,   // ... or a word of its own
		"go test -exec\rid",      // a carriage return, which the parser takes for a space and bash for a character
		"go test 'a\rb'",         // a raw CR inside single quotes, which the parser drops
		"go test a\rb",           // an unquoted CR, which the parser splits on
		"go\rtest ./...",         // in the program position
		`go test $"-exec" id`,    // a locale-translated string
		`go test "a\"b"`,         // an escaped quote inside double quotes
		`go test "a\$b"`,         // an escaped dollar
		`go test "a\\b"`,         // an escaped backslash
		"go test ./...\r&& id",   // a CR before the next command
	} {
		if v := decide(e, cmd); v.Action != Ask {
			t.Errorf("%q: %s (%v); it must ask", cmd, v.Action, v.Reasons)
		}
	}
	// The rule still covers what it covered: plain words, single-quoted text with a backslash in it (bash keeps that raw), and
	// double-quoted text without one.
	for _, cmd := range []string{"go test ./...", `go test -run 'a\nb'`, `go test -run "a b"`, `go test -run 'x\'`} {
		if v := decide(e, cmd); v.Action != Allow {
			t.Errorf("%q: %s (%v); it was covered before and is plain", cmd, v.Action, v.Reasons)
		}
	}
}

// TestAWordIsTextOnlyWhenBashReadsItAsThatText: literal gives the text of a word fixed before the shell runs it and nothing for a
// word whose text bash would decode.
func TestAWordIsTextOnlyWhenBashReadsItAsThatText(t *testing.T) {
	for src, want := range map[string]struct {
		text string
		ok   bool
	}{
		"plain":   {"plain", true},
		`'a\nb'`:  {`a\nb`, true},
		`"a b"`:   {"a b", true},
		`'x\'`:    {`x\`, true},
		`\-exec`:  {"", false},
		`a\b`:     {"", false},
		`$'a\nb'`: {"", false},
		`$'x'`:    {"", false},
		`$"x"`:    {"", false},
		`"a\"b"`:  {"", false},
		`"a\\b"`:  {"", false},
		"'a\rb'":  {"", false},
		"a{b,c}":  {"", false},
		"{a..c}":  {"", false},
		"a{b}":    {"a{b}", true}, // no comma or range: bash leaves it alone
		"a{":      {"a{", true},
		"~":       {"", false},
		"*":       {"", false},
		`"$x"`:    {"", false},
		"$(id)":   {"", false},
	} {
		f, err := parseWordForTest(src)
		if err != nil {
			t.Fatalf("%q: %v", src, err)
		}
		got, ok := literal(f)
		if ok != want.ok || (ok && got != want.text) {
			t.Errorf("%q: %q, %v; want %q, %v", src, got, ok, want.text, want.ok)
		}
	}
}

// TestTwoWordsBashReadsDifferentlyAreNotOneRule: allowing for the session a command whose first argument is `$'a\nb'` (bash: a, a
// newline, b) must not give the rule of `'a\nb'` (a, a backslash, n, b), and an argument the parser reads raw but bash decodes offers
// no rule at all, so the command keeps asking rather than getting a rule for the whole program.
func TestTwoWordsBashReadsDifferentlyAreNotOneRule(t *testing.T) {
	e := engine(t, true)
	for _, first := range []string{`$'a\nb'`, `\-x`, "'a\rb'", `$'\x61'`, `-e{x,y}`, `$"x"`} {
		v := decide(e, "tool "+first+" build")
		if v.Action != Ask {
			t.Fatalf("%s: %s; the test needs it to ask first", first, v.Action)
		}
		eng := engine(t, true)
		eng.AllowForSession("bash", v.Parsed)
		for _, r := range eng.Rules[2:] { // after go test and git status
			t.Errorf("first argument %s: allowing for the session added the rule %q", first, r)
		}
		if w := decide(eng, "tool "+first+" build"); w.Action != Ask {
			t.Errorf("first argument %s: after allowing for the session it is %s", first, w.Action)
		}
		if w := decide(eng, "tool build"); w.Action != Ask {
			t.Errorf("first argument %s: the bare program is now %s", first, w.Action)
		}
	}
	// A plain single-quoted backslash is raw in bash and keeps its own rule, not the ANSI-C one's.
	eng := engine(t, true)
	v := decide(eng, `tool 'a\nb' build`)
	eng.AllowForSession("bash", v.Parsed)
	if w := decide(eng, `tool 'a\nb' build`); w.Action != Allow {
		t.Errorf("the plain single-quoted word's own rule does not cover it: %s", w.Action)
	}
	if w := decide(eng, `tool $'a\nb' build`); w.Action != Ask {
		t.Errorf("the ANSI-C word is covered by the single-quoted word's rule: %s", w.Action)
	}
	_ = strings.TrimSpace
}

// parseWordForTest parses src as the one word of a command and returns it.
func parseWordForTest(src string) (*syntax.Word, error) {
	f, err := syntax.NewParser().Parse(strings.NewReader("x "+src), "")
	if err != nil {
		return nil, err
	}
	call := f.Stmts[0].Cmd.(*syntax.CallExpr)
	return call.Args[1], nil
}
