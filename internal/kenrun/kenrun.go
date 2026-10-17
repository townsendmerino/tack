// Package kenrun is P0's exit check (DESIGN §16): the question tack run answers about ken, and what a run's output
// must show to pass. cmd/tack's TestRunAnswersAQuestionAboutKenFromACassette replays its cassette and applies Check;
// tools/recordrun applies the same Check to each recording, so a recording the test would reject counts as a failed
// attempt.
package kenrun

import (
	"fmt"
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
