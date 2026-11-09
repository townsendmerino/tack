package loop

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/proto"
)

// summaryLog builds a log of events from (type, payload) pairs, in order.
func summaryLog(t *testing.T, pairs ...any) []proto.Event {
	t.Helper()
	var evs []proto.Event
	for i := 0; i < len(pairs); i += 2 {
		ev, err := proto.NewEvent(int64(len(evs)+1), time.Unix(0, 0), pairs[i].(proto.EventType), 0, pairs[i+1])
		if err != nil {
			t.Fatal(err)
		}
		evs = append(evs, ev)
	}
	return evs
}

func user(s string) proto.UserMessage {
	return proto.UserMessage{Content: []proto.ContentBlock{proto.Text(s)}}
}

func said(s string) proto.AssistantMessage {
	return proto.AssistantMessage{Content: []proto.ModelBlock{{Type: "text", Text: s}, {Type: "thinking", Thinking: "think about Hidden.Thought in x/hidden.go"}}}
}

func called(id, name string, input any) proto.ToolCall {
	b, _ := json.Marshal(input)
	return proto.ToolCall{ID: id, Name: name, Input: b}
}

func finished(id, outcome, result string) proto.ToolFinished {
	return proto.ToolFinished{ID: id, Outcome: outcome, Result: result}
}

// session is a compacted stretch with every kind of anchor.
func session(t *testing.T) []proto.Event {
	return summaryLog(t,
		proto.EvUserMessage, user("Fix the rename bug in internal/tools/edit.go; it started at 9f95557ad."),
		proto.EvAssistantMessage, said("I'll read edit.go and look at window.ParseSections."),
		proto.EvToolCall, called("c1", "read", map[string]any{"path": "internal/tools/edit.go"}),
		proto.EvToolFinished, finished("c1", proto.OutcomeOK, "package tools\n\nfunc renameFile() {}\n"),
		proto.EvToolCall, called("c2", "bash", map[string]any{"command": "go test ./internal/tools/ -run TestRenameKeepsMode"}),
		proto.EvToolFinished, finished("c2", proto.OutcomeError, "--- FAIL: TestRenameKeepsMode (0.00s)\n    edit_test.go:41: mode 0644, want 0755\nFAIL\nok  \tother"),
		proto.EvUserMessage, user("Keep the fix inside renameFile."),
	)
}

func TestAnchorsAreCopiedExactlyInOrder(t *testing.T) {
	a := AnchorsOf(session(t))
	for name, c := range map[string]struct{ got, want []string }{
		"paths":   {a.Paths, []string{"internal/tools/edit.go", "edit.go", "./internal/tools/", "edit_test.go"}},
		"symbols": {a.Symbols, []string{"window.ParseSections", "TestRenameKeepsMode"}},
		"commits": {a.Commits, []string{"9f95557ad"}},
		"errors":  {a.Errors, []string{"--- FAIL: TestRenameKeepsMode (0.00s)", "FAIL"}},
	} {
		if !slices.Equal(c.got, c.want) {
			t.Errorf("%s %q, want %q", name, c.got, c.want)
		}
	}
}

// TestAnchorsSkipWhatIsNotAnAnchor: thinking is not read, a decimal number is not a commit, and prose is not a symbol
// or a path.
func TestAnchorsSkipWhatIsNotAnAnchor(t *testing.T) {
	a := AnchorsOf(summaryLog(t,
		proto.EvAssistantMessage, said("There are 12345678 lines. The Plan is fine, e.g. later."),
		proto.EvToolFinished, finished("c1", proto.OutcomeOK, "deadbeef12cafe and 0123456 and abcdefabc"),
	))
	if len(a.Paths)+len(a.Symbols) != 0 || !slices.Equal(a.Commits, []string{"deadbeef12cafe"}) || len(a.Errors) != 0 {
		t.Errorf("anchors %+v", a)
	}
}

// TestAFailedCallsFirstLineIsAnError: a failed call's result names its error even when no line says "error".
func TestAFailedCallsFirstLineIsAnError(t *testing.T) {
	a := AnchorsOf(summaryLog(t, proto.EvToolFinished, finished("c1", proto.OutcomeError, "no such file: x\nmore")))
	if !slices.Equal(a.Errors, []string{"no such file: x"}) {
		t.Errorf("errors %q", a.Errors)
	}
}

// TestAnchorsAreCapped: each kind keeps its first anchorMax, and an error line is cut.
func TestAnchorsAreCapped(t *testing.T) {
	var b strings.Builder
	for i := range anchorMax + 5 {
		fmt.Fprintf(&b, "pkg/f%d.go error number %d %s\n", i, i, strings.Repeat("x", anchorErrorBytes))
	}
	a := AnchorsOf(summaryLog(t, proto.EvToolFinished, finished("c1", proto.OutcomeOK, b.String())))
	if len(a.Paths) != anchorMax || a.Paths[0] != "pkg/f0.go" || len(a.Errors) != anchorMax || len(a.Errors[0]) > anchorErrorBytes+len("…") {
		t.Errorf("%d paths (first %q), %d errors (first %d bytes)", len(a.Paths), a.Paths[0], len(a.Errors), len(a.Errors[0]))
	}
}

// TestTheFallbackKeepsEveryUserMessage: the fallback summary holds every user message word for word, the task first,
// and the anchors after them.
func TestTheFallbackKeepsEveryUserMessage(t *testing.T) {
	s := FallbackSummary(session(t))
	task := strings.Index(s, "## Task\n\nFix the rename bug in internal/tools/edit.go; it started at 9f95557ad.")
	later := strings.Index(s, "## Later messages\n\nKeep the fix inside renameFile.")
	anchors := strings.Index(s, "## Anchors (exact)\n\nPaths: internal/tools/edit.go, edit.go")
	if task != 0 || later <= task || anchors <= later || !strings.Contains(s, "- --- FAIL: TestRenameKeepsMode (0.00s)") {
		t.Errorf("fallback summary:\n%s", s)
	}
	if FallbackSummary(nil) != "" {
		t.Errorf("an empty stretch: %q", FallbackSummary(nil))
	}
}

func TestSummaryHeadingsAreChecked(t *testing.T) {
	full := "## Goal\nfix it\n## Constraints\n## Done\n## In progress\n## Decisions\n## Files\n## Next steps\n"
	for name, c := range map[string]struct {
		text string
		want bool
	}{
		"all, in order":     {full, true},
		"case and spacing":  {strings.ReplaceAll(full, "## In progress", "## in Progress  "), true},
		"one missing":       {strings.Replace(full, "## Decisions\n", "", 1), false},
		"out of order":      {strings.Replace(strings.Replace(full, "## Goal\n", "", 1), "## Next steps\n", "## Next steps\n## Goal\n", 1), false},
		"a deeper heading":  {strings.Replace(full, "## Files", "### Files", 1), false},
		"empty":             {"", false},
		"the fallback text": {FallbackSummary(session(t)), false},
	} {
		if got := HasSummaryHeadings(c.text); got != c.want {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
}
