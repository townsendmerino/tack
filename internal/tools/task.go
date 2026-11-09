package tools

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/townsendmerino/tack/internal/server"
)

// task (DESIGN §6.3, "Subagents"; §6.5's table: the loop's, read-only, no permission): a child session with its own
// context, read-only tools and its own budget answers a question, and its answer is the result. On one local model the
// child runs in the parent's turn, which waits for it: it buys a clean context, not speed.

// Subagent runs a child session on prompt and returns its answer.
type Subagent func(ctx context.Context, prompt string) (SubagentAnswer, error)

// SubagentAnswer is a child session's answer. Tokens is what the server computed for its requests, counted toward the
// parent's budgets (Result.Tokens); Session names the child's own log.
type SubagentAnswer struct {
	Text    string
	Tokens  int
	Session string
}

type taskTool struct{ run Subagent }

// ReadOnly: the child has the read-only tools alone, so a task changes nothing.
func (taskTool) ReadOnly() bool { return true }

func (taskTool) Spec() server.Tool {
	return server.Tool{Name: "task",
		Description: "Hand a self-contained question to a helper with a fresh context and read-only tools; get its answer.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"prompt":{"type":"string"}},"required":["prompt"]}`)}
}

func (t taskTool) Run(ctx context.Context, _ *Workspace, input json.RawMessage) (Result, error) {
	var in struct {
		Prompt string `json:"prompt"`
	}
	if err := json.Unmarshal(input, &in); err != nil || strings.TrimSpace(in.Prompt) == "" {
		return errorf("task needs a prompt: the question, with everything the helper needs to answer it"), nil
	}
	a, err := t.run(ctx, in.Prompt)
	if ctx.Err() != nil {
		return Result{Tokens: a.Tokens}, ctx.Err()
	}
	if err != nil {
		r := errorf("the helper could not answer: %v", err)
		r.Tokens = a.Tokens
		return r, nil
	}
	text := strings.TrimSpace(a.Text)
	if text == "" {
		text = "(the helper gave no answer)"
	}
	if a.Session != "" {
		text += "\n\n[subagent session " + a.Session + "]"
	}
	return Result{Text: text, Tokens: a.Tokens}, nil
}
