package kenrun

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFactsMatchTheCode holds Facts to applyTokenBudget as the cassettes' workspace defines it: if the testdata's ken
// changes, the facts a correct answer states change with it.
func TestFactsMatchTheCode(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "cmd", "tack", "testdata", "ken", "mcp", "token_budget.go"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, want := range []string{
		"func applyTokenBudget(results []search.Result, maxTokens int) (kept []search.Result, dropped, approxTotal int) {",
		"// Returns the kept prefix, how many were dropped, and the approximate token\n// total of the kept set.",
		"// applyTokenBudget greedily keeps the ranked results whose cumulative estimated\n// token cost stays within maxTokens",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("mcp/token_budget.go no longer has %q: update Facts from the code", want)
		}
	}
}

// correct is a hand-written right answer, in a real model's words rather than the scripted model's.
const correct = "applyTokenBudget is defined in mcp/token_budget.go. It greedily keeps the ranked search results whose " +
	"estimated token cost stays within maxTokens, always keeping the top result, and returns three values: the kept " +
	"results (a prefix of the input slice), the number of results dropped, and the approximate token total of the kept set."

// correctTrace is a real model's way there: a grep that misses, then a glob and a read that succeed.
const correctTrace = `· grep {"pattern":"applyTokenBudget","path":"internal"}
  error, 0 lines
· glob {"pattern":"**/*.go"}
  ok, 1 lines
· read {"path":"mcp/token_budget.go","offset":60,"limit":30}
  ok, 30 lines
`

func TestCheckModelAcceptsACorrectAnswer(t *testing.T) {
	if why := CheckModel(correct, correctTrace); why != nil {
		t.Fatalf("a correct answer was rejected: %v", why)
	}
}

// TestCheckModelRejectsTheNightAnswer: the 2026-10-16 night attempt, verbatim, named the file but concluded the
// function is not defined there and never said what it returns.
func TestCheckModelRejectsTheNightAnswer(t *testing.T) {
	stdout, _ := os.ReadFile(filepath.Join("testdata", "night-2026-10-16.stdout"))
	stderr, _ := os.ReadFile(filepath.Join("testdata", "night-2026-10-16.stderr"))
	why := CheckModel(string(stdout), string(stderr))
	got := strings.Join(why, "; ")
	for _, want := range []string{"claims the function is missing", "dropped", "approximate token total"} {
		if !strings.Contains(got, want) {
			t.Errorf("the night answer's rejection lacks %q: %s", want, got)
		}
	}
}

// TestCheckModelRejectsEachGap: a correct answer with any one fact removed, a missing claim added, or no successful
// call is rejected for that reason.
func TestCheckModelRejectsEachGap(t *testing.T) {
	cases := map[string]struct{ stdout, stderr, want string }{
		"no file":        {strings.Replace(correct, "mcp/token_budget.go", "the mcp package", 1), correctTrace, "where it is defined"},
		"no kept":        {strings.NewReplacer("keeps", "takes", "kept", "chosen", "prefix", "part").Replace(correct), correctTrace, "kept results"},
		"no dropped":     {strings.Replace(correct, "dropped", "left out", 1), correctTrace, "dropped"},
		"no total":       {strings.NewReplacer("approximate token total", "token count", "total", "count").Replace(correct), correctTrace, "approximate token total"},
		"no budget":      {strings.Replace(correct, "stays within maxTokens", "is small", 1), correctTrace, "token budget"},
		"claims missing": {correct + " But it is not defined anywhere else.", correctTrace, "claims the function is missing"},
		"no tool worked": {correct, "· grep {\"pattern\":\"x\",\"path\":\"ken\"}\n  error, 0 lines\n", "no tool call succeeded"},
	}
	for name, tc := range cases {
		why := CheckModel(tc.stdout, tc.stderr)
		if !strings.Contains(strings.Join(why, "; "), tc.want) {
			t.Errorf("%s: rejection %v lacks %q", name, why, tc.want)
		}
	}
}

// TestCheckModelRejectsTheSecondNightAnswer: the 2026-10-17 night's attempt 2, verbatim. It names the file and says
// what kept and dropped are, but calls approxTotal "an approximation of the total number of results": a total, but not
// of tokens, beside the quoted signature. It is rejected for that fact alone.
func TestCheckModelRejectsTheSecondNightAnswer(t *testing.T) {
	stdout, _ := os.ReadFile(filepath.Join("testdata", "night-2026-10-17-attempt2.stdout"))
	stderr, _ := os.ReadFile(filepath.Join("testdata", "night-2026-10-17-attempt2.stderr"))
	why := CheckModel(string(stdout), string(stderr))
	if len(why) != 1 || !strings.Contains(why[0], "approximate token total") {
		t.Fatalf("attempt 2's rejection: %v", why)
	}
}

// TestTokenTotalPhrasings: the fact passes in other words that state it, and fails on near misses.
func TestTokenTotalPhrasings(t *testing.T) {
	fact := Facts[3]
	for _, s := range []string{
		"and the approximate token total of the kept set",
		"and the approximate token total of what it kept",
		"it returns the kept results' approximate token total",
		"the third value is the sum of the estimated tokens across the kept results",
		"approxTotal, the total number of tokens in the kept prefix",
	} {
		if !anyMatch(fact.Patterns, s) {
			t.Errorf("rejected a statement of the fact: %q", s)
		}
	}
	for _, s := range []string{
		"`approxTotal int`: An approximation of the total number of results.",
		"(kept []search.Result, dropped, approxTotal int)",
		"it returns an approximate total",
		"the token count",
		"the total tokens of all the results",
	} {
		if anyMatch(fact.Patterns, s) {
			t.Errorf("accepted a near miss: %q", s)
		}
	}
}
