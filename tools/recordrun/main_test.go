package main

import (
	"errors"
	"strings"
	"testing"
)

// TestJudgeIsTheExitTest: an attempt passes only on the exit test's own assertions. The 2026-10-16 night attempt
// named mcp/token_budget.go while concluding that applyTokenBudget is not defined there, and the old substring check
// adopted it; the exit test rejects it, so the judge must too.
func TestJudgeIsTheExitTest(t *testing.T) {
	night := `It appears that the function ` + "`applyTokenBudget`" + ` is not defined in the ` + "`mcp`" + ` package based on the
search results. The file ` + "`mcp/token_budget.go`" + ` contains the definition of ` + "`applyTokenBudget`" + `.`
	nightTrace := `· grep {"pattern":"applyTokenBudget.*?=.*$","path":"ken"}
  error, 0 lines
· glob {"pattern":"**/*.go"}
  ok, 1 lines
· read {"path":"mcp/token_budget.go","offset":1,"limit":20}
  ok, 20 lines
`
	if why := judge("recording", night, nightTrace, nil); len(why) == 0 || !strings.Contains(strings.Join(why, ";"), "mcp/token_budget.go:70") {
		t.Errorf("the night's wrong answer passed: %v", why)
	}

	good := "`applyTokenBudget` is defined at mcp/token_budget.go:70. It returns the kept prefix of the results."
	goodTrace := `· grep {"pattern":"func applyTokenBudget"}
  ok, 1 lines
· read {"path":"mcp/token_budget.go","offset":62,"limit":22}
  ok, 22 lines
`
	if why := judge("replay", good, goodTrace, nil); why != nil {
		t.Errorf("the scripted run failed: %v", why)
	}
	if why := judge("replay", good, goodTrace, errors.New("exit status 1")); len(why) != 1 || !strings.HasPrefix(why[0], "replay: exit") {
		t.Errorf("a failing exit passed: %v", why)
	}
}
