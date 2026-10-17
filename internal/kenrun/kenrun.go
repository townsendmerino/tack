// Package kenrun is P0's exit check (DESIGN §16): the question tack run answers about ken, and what a run's output
// must show to pass. There are two checks. Check is the scripted model's: cmd/tack's
// TestRunAnswersAQuestionAboutKenFromACassette applies it to the scripted cassette, whose exact calls it pins.
// CheckModel is a real model's: TestRunAnswersFromARealModelCassette applies it to a recorded cassette, and
// tools/recordrun applies the same CheckModel to each recording, so a recording that test would reject is a failed
// attempt.
package kenrun

import (
	"fmt"
	"regexp"
	"strings"
)

// Question is what the run asks.
const Question = "Which file defines applyTokenBudget, and what does it return?"

// Answer is what the answer on stdout must contain: the defining file and line.
const Answer = "mcp/token_budget.go:70"

// Trace is what the tool trace on stderr must contain: the grep that finds the definition and the read of it.
var Trace = []string{`· grep {"pattern":"func applyTokenBudget"}`, "· read ", "ok, 1 lines", "ok, 22 lines"}

// Check returns why a run with this stdout and stderr fails the exit check, one reason per failed assertion; nil
// when it passes.
func Check(stdout, stderr string) []string {
	var why []string
	if !strings.Contains(stdout, Answer) {
		why = append(why, fmt.Sprintf("the answer lacks %q", Answer))
	}
	for _, want := range Trace {
		if !strings.Contains(stderr, want) {
			why = append(why, fmt.Sprintf("the tool trace lacks %q", want))
		}
	}
	return why
}

// A Fact is one thing a correct answer states, matched case-insensitively by any of its patterns. The facts come from
// applyTokenBudget's code in cmd/tack/testdata/ken/mcp/token_budget.go, the workspace every cassette is recorded and
// replayed over; TestFactsMatchTheCode holds them to it.
type Fact struct {
	What     string
	Patterns []*regexp.Regexp
}

// Facts are what a correct answer states: where applyTokenBudget is defined, its three results, and what it does.
var Facts = []Fact{
	{"where it is defined: mcp/token_budget.go", res(`token_budget\.go`)},
	{"it returns the kept results, a prefix of the input", res(`\bkept\b`, `\bprefix\b`, `\bkeeps\b`)},
	{"it returns how many results were dropped", res(`\bdropped\b`, `\bdrops?\b`)},
	{"it returns the approximate token total of the kept results", res(`approx`, `\btotal\b`)},
	{"it keeps results within a token budget", res(`max_?tokens`, `\bmax tokens\b`, `\btoken (budget|limit)\b`,
		`\bwithin\b`, `\bfits?\b`)},
}

// Missing are claims that applyTokenBudget is not there. A correct answer makes none, even in the narration between
// tool calls that tack run also prints.
var Missing = res(`\bnot (directly )?(be )?(defined|declared|present)\b`, `\bisn't (directly )?defined\b`,
	`\b(could not|couldn't|cannot|can't|unable to|failed to) (find|locate) (the |its |a )?(function|definition|applytokenbudget)`, // matched lowercased
	`\bdoes(n't| not) (exist|appear to exist)\b`, `\bno (definition|such function)\b`)

// toolOK is a successful call in tack run's tool trace.
var toolOK = regexp.MustCompile(`(?m)^\s+ok, \d+ lines`)

// CheckModel returns why a real model's run with this stdout (the answer) and stderr (the tool trace) fails the exit
// check, one reason per failed assertion; nil when it passes. The answer must state every Fact, make no Missing claim,
// and come after at least one successful tool call.
func CheckModel(stdout, stderr string) []string {
	var why []string
	for _, f := range Facts {
		if !anyMatch(f.Patterns, stdout) {
			why = append(why, "the answer does not say "+f.What)
		}
	}
	for _, m := range Missing {
		if s := m.FindString(strings.ToLower(stdout)); s != "" {
			why = append(why, fmt.Sprintf("the answer claims the function is missing (%q)", s))
		}
	}
	if !toolOK.MatchString(stderr) {
		why = append(why, "no tool call succeeded")
	}
	return why
}

func anyMatch(ps []*regexp.Regexp, s string) bool {
	s = strings.ToLower(s)
	for _, p := range ps {
		if p.MatchString(s) {
			return true
		}
	}
	return false
}

func res(patterns ...string) []*regexp.Regexp {
	var out []*regexp.Regexp
	for _, p := range patterns {
		out = append(out, regexp.MustCompile(p))
	}
	return out
}
