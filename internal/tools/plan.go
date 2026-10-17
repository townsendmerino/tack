package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/townsendmerino/tack/internal/server"
)

// BlobReader returns a stored output by its handle (internal/store.Blobs).
type BlobReader interface {
	Get(handle string) ([]byte, error)
}

// readOutputTool pages an output the loop truncated for the model and stored whole (DESIGN §6.3, step 4).
type readOutputTool struct{ blobs BlobReader }

func (readOutputTool) ReadOnly() bool { return true }
func (readOutputTool) Spec() server.Tool {
	return server.Tool{Name: "read_output",
		Description: "Page a truncated tool output by the handle its result gave.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"handle":{"type":"string"},"offset":{"type":"integer","description":"first line, from 1"},"limit":{"type":"integer"}},"required":["handle"]}`)}
}

func (t readOutputTool) Run(_ context.Context, _ *Workspace, input json.RawMessage) (Result, error) {
	var in struct {
		Handle string `json:"handle"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := decode(input, &in); err != nil || in.Handle == "" {
		return errorf("bad input: read_output takes the handle a truncated result gave, and optionally offset and limit"), nil
	}
	data, err := t.blobs.Get(in.Handle)
	if err != nil {
		return errorf("no stored output with handle %q; copy the handle exactly from the truncated result", in.Handle), nil
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	start, n := max(in.Offset, 1), in.Limit
	if n <= 0 {
		n = readMaxLines
	}
	if start > len(lines) {
		return errorf("the output has %d lines; offset %d is past the end", len(lines), start), nil
	}
	end := min(start-1+n, len(lines))
	var b strings.Builder
	for i := start - 1; i < end; i++ {
		fmt.Fprintf(&b, "%6d\t%s\n", i+1, cut(lines[i]))
	}
	if end < len(lines) {
		fmt.Fprintf(&b, "… the output has %d lines; showing %d-%d. Continue with offset %d.\n", len(lines), start, end, end+1)
	}
	return Result{Text: b.String()}, nil
}

// todoTool replaces the session's task list. The list lives in the event log as the last todo call's input, so it
// needs no state here; clients render it from there.
type todoTool struct{}

func (todoTool) ReadOnly() bool { return true }
func (todoTool) Spec() server.Tool {
	return server.Tool{Name: "todo",
		Description: "Replace your task list. Keep one item in_progress at a time.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"items":{"type":"array","items":{"type":"object","properties":{"text":{"type":"string"},"status":{"type":"string","enum":["pending","in_progress","done"]}},"required":["text","status"]}}},"required":["items"]}`)}
}

func (todoTool) Run(_ context.Context, _ *Workspace, input json.RawMessage) (Result, error) {
	var in struct {
		Items []struct {
			Text   string `json:"text"`
			Status string `json:"status"`
		} `json:"items"`
	}
	if err := decode(input, &in); err != nil {
		return errorf("bad input: %v; todo takes items, each with text and status", err), nil
	}
	mark := map[string]string{"pending": "[ ]", "in_progress": "[>]", "done": "[x]"}
	var b strings.Builder
	active := 0
	for i, it := range in.Items {
		m, ok := mark[it.Status]
		if !ok {
			return errorf("item %d has status %q; use pending, in_progress or done", i+1, it.Status), nil
		}
		if strings.TrimSpace(it.Text) == "" {
			return errorf("item %d has no text", i+1), nil
		}
		if it.Status == "in_progress" {
			active++
		}
		fmt.Fprintf(&b, "%s %s\n", m, it.Text)
	}
	if active > 1 {
		return errorf("%d items are in_progress; keep one in_progress and mark the others pending", active), nil
	}
	if len(in.Items) == 0 {
		return Result{Text: "the task list is empty\n"}, nil
	}
	return Result{Text: b.String()}, nil
}

// Asker answers the model's question from the user: the TUI, the app, or nothing in a headless run.
type Asker interface {
	Ask(ctx context.Context, question string) (string, error)
}

// askUserTool asks the user and waits for the answer.
type askUserTool struct{ ask Asker }

func (askUserTool) ReadOnly() bool { return true }
func (askUserTool) Spec() server.Tool {
	return server.Tool{Name: "ask_user",
		Description: "Ask the user a question and wait for the answer. Only when you cannot go on safely without one.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"question":{"type":"string"}},"required":["question"]}`)}
}

func (t askUserTool) Run(ctx context.Context, _ *Workspace, input json.RawMessage) (Result, error) {
	var in struct {
		Question string `json:"question"`
	}
	if err := decode(input, &in); err != nil || strings.TrimSpace(in.Question) == "" {
		return errorf("bad input: ask_user takes a question"), nil
	}
	if t.ask == nil {
		return Result{Text: "no one is attached to answer (a headless run): choose the safest reasonable option " +
			"yourself, and say what you assumed"}, nil
	}
	answer, err := t.ask.Ask(ctx, in.Question)
	if err != nil {
		return Result{}, err
	}
	return Result{Text: answer}, nil
}
