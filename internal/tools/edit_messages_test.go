package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// editRun runs edit (messages on or off) after reading path, as the model would.
func editRun(t *testing.T, ws *Workspace, messages bool, read string, input string) Result {
	t.Helper()
	set := New(Options{Write: true, EditMessages: messages})
	if read != "" {
		r, _ := set.Get("read")
		r.Run(context.Background(), ws, json.RawMessage(`{"path":"`+read+`"}`))
	}
	e, _ := set.Get("edit")
	res, err := e.Run(context.Background(), ws, json.RawMessage(input))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestEditMessageNamesTheNearestPath(t *testing.T) {
	for _, c := range []struct{ asked, want string }{
		{"tack/internal/a/a.go", `"internal/a/a.go" exists`}, // a repository name taken as a directory
		{"pkg/a/a.go", `"internal/a/a.go" exists`},           // the same name, the longest shared tail
	} {
		ws := workspace(t)
		r := editRun(t, ws, true, "", `{"path":"`+c.asked+`","old_string":"x","new_string":"y"}`)
		if !r.IsError || !strings.Contains(r.Text, c.want) {
			t.Errorf("%s: %q", c.asked, r.Text)
		}
	}
}

func TestEditMessageForAnUnchangedEdit(t *testing.T) {
	r := editRun(t, workspace(t), true, "", `{"path":"internal/a/a.go","old_string":"package a","new_string":"package a"}`)
	if !r.IsError || r.Text != "error: this edit changes nothing; if the change is already made, verify it with a build or test." {
		t.Fatalf("%q", r.Text)
	}
}

func TestEditMessageNamesTheSessionsOwnChange(t *testing.T) {
	ws := workspace(t)
	editRun(t, ws, true, "internal/a/a.go", `{"path":"internal/a/a.go","old_string":"func applyBudget() {}","new_string":"func applyBudget() int { return 0 }"}`)
	r := editRun(t, ws, true, "", `{"path":"internal/a/a.go","old_string":"func applyBudget() {}","new_string":"func applyBudget() {} // x"}`)
	if !r.IsError || !strings.Contains(r.Text, "your earlier edit of this file at line 4 replaced it") {
		t.Fatalf("%q", r.Text)
	}
	set := New(Options{Write: true, EditMessages: true})
	w, _ := set.Get("write")
	w.Run(context.Background(), ws, json.RawMessage(`{"path":"internal/a/a.go","content":"package a\n"}`))
	r = editRun(t, ws, true, "", `{"path":"internal/a/a.go","old_string":"func applyBudget() int { return 0 }","new_string":"x"}`)
	if !r.IsError || !strings.Contains(r.Text, "your earlier write of this file replaced it") {
		t.Fatalf("after a write: %q", r.Text)
	}
}

func TestEditMessageNamesAnotherFile(t *testing.T) {
	r := editRun(t, workspace(t), true, "internal/a/a.go", `{"path":"internal/a/a.go","old_string":"func main() {}","new_string":"func main() { run() }"}`)
	if !r.IsError || !strings.Contains(r.Text, "it is in cmd/tack/main.go:3") {
		t.Fatalf("%q", r.Text)
	}
}

// TestEditMessageShowsTheBestMatchingLines: text found nowhere shows the twelve consecutive lines most like it, with
// line numbers, wherever in the file they are; text sharing no word shows none.
func TestEditMessageShowsTheBestMatchingLines(t *testing.T) {
	ws := workspace(t)
	r := editRun(t, ws, true, "long.txt", `{"path":"long.txt","old_string":"line 190\nline 191 was here\nline 192","new_string":"x"}`)
	got := strings.SplitN(r.Text, "\n", 2)
	if !r.IsError || len(got) < 2 || !strings.Contains(got[1], "   190\tline 190\n   191\tline 191\n   192\tline 192\n") ||
		strings.Count(got[1], "\tline ") != bestLinesShown || strings.Contains(got[1], "\tline 1\n") {
		t.Fatalf("%q", r.Text)
	}
	r = editRun(t, ws, true, "long.txt", `{"path":"long.txt","old_string":"zzz qqq","new_string":"x"}`)
	if !strings.Contains(r.Text, "no line of it shares a word") {
		t.Fatalf("no shared word: %q", r.Text)
	}
}

// TestEditMessagesOffAreUnchanged: with the setting off, every one of these failures reads exactly as before.
func TestEditMessagesOffAreUnchanged(t *testing.T) {
	ws := workspace(t)
	for in, want := range map[string]string{
		`{"path":"tack/internal/a/a.go","old_string":"x","new_string":"y"}`:                   `error: no such file "tack/internal/a/a.go"; paths are relative to the repo root, and write creates a new file`,
		`{"path":"internal/a/a.go","old_string":"package a","new_string":"package a"}`:        "error: old_string and new_string are the same; nothing to change",
		`{"path":"internal/a/a.go","old_string":"func main() {}","new_string":"func m() {}"}`: `error: this text isn't in "internal/a/a.go"; the edit may already have been made. Read the file again before editing: old_string must match it exactly, whitespace included`,
	} {
		if r := editRun(t, ws, false, "internal/a/a.go", in); r.Text != want {
			t.Errorf("%s:\n got %q\nwant %q", in, r.Text, want)
		}
	}
}
