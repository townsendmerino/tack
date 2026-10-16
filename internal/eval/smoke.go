package eval

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/townsendmerino/tack/internal/server"
)

// SmokeTask is one of E0's tool-use smoke tasks (docs/measurements/e0-preregistration-2026-10-16.md, "smoke"): a
// question, the tools whose call passes, and text the call's argument must contain. NoTool tasks pass when the model
// answers without calling anything.
type SmokeTask struct {
	Prompt string
	Tools  []string // acceptable tools
	Arg    string   // the argument to check
	Want   []string // all must appear in that argument (case-insensitive, backslashes ignored)
	NoTool bool
}

// SmokeTasks is the registered set. Changing it after a run voids comparisons with that run.
var SmokeTasks = []SmokeTask{
	{Prompt: "Where is the function applyTokenBudget defined?", Tools: []string{"grep"}, Arg: "pattern", Want: []string{"applyTokenBudget"}},
	{Prompt: "List every Go test file under internal/.", Tools: []string{"glob"}, Arg: "pattern", Want: []string{"_test.go"}},
	{Prompt: "Show me the first 40 lines of README.md.", Tools: []string{"read"}, Arg: "path", Want: []string{"README.md"}},
	{Prompt: "Which files mention the word deadline?", Tools: []string{"grep"}, Arg: "pattern", Want: []string{"deadline"}},
	{Prompt: "What module path does go.mod declare?", Tools: []string{"read"}, Arg: "path", Want: []string{"go.mod"}},
	{Prompt: "Find all TODO comments in the code.", Tools: []string{"grep"}, Arg: "pattern", Want: []string{"TODO"}},
	{Prompt: "List the files in the cmd directory.", Tools: []string{"glob"}, Arg: "pattern", Want: []string{"cmd"}},
	{Prompt: "Open internal/loop/loop.go.", Tools: []string{"read"}, Arg: "path", Want: []string{"internal/loop/loop.go"}},
	{Prompt: "Where is the constant FixedPromptBudget used?", Tools: []string{"grep"}, Arg: "pattern", Want: []string{"FixedPromptBudget"}},
	{Prompt: "Are there any Markdown files under docs?", Tools: []string{"glob"}, Arg: "pattern", Want: []string{"docs", ".md"}},
	{Prompt: "Read the first 20 lines of internal/proto/events.go.", Tools: []string{"read"}, Arg: "path", Want: []string{"internal/proto/events.go"}},
	{Prompt: "Which file defines the type Conversation?", Tools: []string{"grep"}, Arg: "pattern", Want: []string{"Conversation"}},
	{Prompt: "Find every call to os.Exit.", Tools: []string{"grep"}, Arg: "pattern", Want: []string{"os", "Exit"}},
	{Prompt: "What YAML files are in the .github directory?", Tools: []string{"glob"}, Arg: "pattern", Want: []string{".github"}},
	{Prompt: "Show lines 100 to 140 of internal/server/goinfer/client.go.", Tools: []string{"read"}, Arg: "path", Want: []string{"internal/server/goinfer/client.go"}},
	{Prompt: "Where in the code do we handle HTTP status 529?", Tools: []string{"grep"}, Arg: "pattern", Want: []string{"529"}},
	{Prompt: "What is 17 times 3? Answer directly, without using any tool.", NoTool: true},
	{Prompt: "Say hello in one word. Do not use any tool.", NoTool: true},
	{Prompt: "Which test checks the fixed prompt budget? Search for its name.", Tools: []string{"grep"}, Arg: "pattern", Want: []string{"Budget"}},
	{Prompt: "Find the file named DECISIONS.md.", Tools: []string{"glob"}, Arg: "pattern", Want: []string{"DECISIONS.md"}},
}

// SmokeResult is one task's grade.
type SmokeResult struct {
	Task      int    `json:"task"`
	Pass      bool   `json:"pass"`
	Malformed bool   `json:"malformed"`
	Tool      string `json:"tool,omitempty"`
	Input     string `json:"input,omitempty"`
	Why       string `json:"why,omitempty"`
}

// ScoreSmoke grades the first response to task i. Only the first tool call counts.
func ScoreSmoke(i int, resp *server.Response) SmokeResult {
	t := SmokeTasks[i]
	r := SmokeResult{Task: i}
	var call *struct {
		name  string
		input json.RawMessage
		id    string
	}
	for _, b := range resp.Content {
		if b.Type == "tool_use" {
			call = &struct {
				name  string
				input json.RawMessage
				id    string
			}{b.Name, b.Input, b.ID}
			break
		}
	}
	if len(resp.Malformed) > 0 {
		r.Malformed = true
	}
	if call == nil {
		r.Pass = t.NoTool
		if !r.Pass {
			r.Why = "no tool call"
		}
		return r
	}
	r.Tool, r.Input = call.name, string(call.input)
	if t.NoTool {
		r.Why = "called " + call.name + " on a no-tool task"
		return r
	}
	if why := resp.Malformed[call.id]; why != "" {
		r.Why = "malformed: " + why
		return r
	}
	ok := false
	for _, name := range t.Tools {
		ok = ok || name == call.name
	}
	if !ok {
		r.Why = fmt.Sprintf("called %s, want one of %v", call.name, t.Tools)
		return r
	}
	var in map[string]any
	if err := json.Unmarshal(call.input, &in); err != nil {
		r.Why = "input does not decode"
		return r
	}
	v, _ := in[t.Arg].(string)
	norm := strings.ToLower(strings.ReplaceAll(v, `\`, ""))
	for _, w := range t.Want {
		if !strings.Contains(norm, strings.ToLower(w)) {
			r.Why = fmt.Sprintf("%s = %q lacks %q", t.Arg, v, w)
			return r
		}
	}
	r.Pass = true
	return r
}
