package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestJudgeIsTheRealModelExitTest: an attempt is judged by kenrun.CheckModel, the real-model cassette test's own
// assertions. The 2026-10-16 night attempt, verbatim, named mcp/token_budget.go while concluding applyTokenBudget is
// not defined there, and the old substring check adopted it; the judge rejects it. A correct answer in a real model's
// words, reached by a grep that missed and a read that worked, passes.
func TestJudgeIsTheRealModelExitTest(t *testing.T) {
	night, _ := os.ReadFile(filepath.Join("..", "..", "internal", "kenrun", "testdata", "night-2026-10-16.stdout"))
	nightTrace, _ := os.ReadFile(filepath.Join("..", "..", "internal", "kenrun", "testdata", "night-2026-10-16.stderr"))
	if why := judge("recording", string(night), string(nightTrace), nil); !strings.Contains(strings.Join(why, ";"), "recording: the answer claims the function is missing") {
		t.Errorf("the night's wrong answer was not rejected as missing: %v", why)
	}

	good := "It is defined in mcp/token_budget.go. applyTokenBudget keeps ranked results while their estimated token " +
		"cost stays within maxTokens, and returns the kept prefix, how many results were dropped, and the approximate " +
		"token total of what it kept."
	goodTrace := "· grep {\"pattern\":\"applyTokenBudget\",\"path\":\"ken\"}\n  error, 0 lines\n" +
		"· read {\"path\":\"mcp/token_budget.go\",\"offset\":60,\"limit\":30}\n  ok, 30 lines\n"
	if why := judge("replay", good, goodTrace, nil); why != nil {
		t.Errorf("a correct answer failed: %v", why)
	}
	if why := judge("replay", good, goodTrace, errors.New("exit status 1")); len(why) != 1 || !strings.HasPrefix(why[0], "replay: exit") {
		t.Errorf("a failing exit passed: %v", why)
	}
}
