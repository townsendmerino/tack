package window

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func bash(cmd string) json.RawMessage {
	b, _ := json.Marshal(map[string]string{"command": cmd})
	return b
}

func editOf(path string) json.RawMessage {
	b, _ := json.Marshal(map[string]string{"path": path})
	return b
}

func TestParseSections(t *testing.T) {
	src := "intro\n\n# Title\n\ntext\n\n## Rules ##\n\n```\n# not a heading\n```\n<!-- tack:on bash:git, edit:*.md -->\n\n### Sub\n\nx\n\n## Tests\n<!-- tack:always -->\n"
	got := ParseSections(src)
	want := []struct {
		path       string
		level      int
		start, end int
	}{{"", 0, 1, 2}, {"Title", 1, 3, 6}, {"Title/Rules", 2, 7, 13}, {"Title/Rules/Sub", 3, 14, 17}, {"Title/Tests", 2, 18, 19}}
	if len(got) != len(want) {
		t.Fatalf("%d sections, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		g := got[i]
		if strings.Join(g.HeadingPath, "/") != w.path || g.Level != w.level || g.StartLine != w.start || g.EndLine != w.end {
			t.Errorf("section %d: %v level %d lines %d–%d, want %s level %d lines %d–%d", i, g.HeadingPath, g.Level,
				g.StartLine, g.EndLine, w.path, w.level, w.start, w.end)
		}
	}
	if r := got[2]; strings.Join(r.On, "|") != "bash:git|edit:*.md" || r.Always || !strings.Contains(r.Text, "# not a heading") {
		t.Errorf("Rules: on %q, always %v, text %q", r.On, r.Always, r.Text)
	}
	if !got[4].Always {
		t.Error("Tests carries tack:always")
	}
}

// bigFile is an instruction file over the budget at window 4096 (614 tokens): a title, an introduction, then sections on
// git, testing (with a subsection), releases (marked always) and a long filler section.
func bigFile() string {
	var b strings.Builder
	b.WriteString("# notes\n\nIntro: read this first.\n\n")
	b.WriteString("## Staging and committing\n\nNever git add -A.\n\n")
	b.WriteString("## Testing\n\nRun go test with -v.\n\n### A skip is not a pass\n\nRead for PASS.\n\n")
	b.WriteString("## Releases\n<!-- tack:always -->\nTag from main.\n\n")
	b.WriteString("## Night queue\n<!-- tack:on bash:night -->\nQueue long jobs.\n\n")
	b.WriteString("## Filler\n\n")
	for range 300 {
		b.WriteString("a line of filler text to push the file over its budget\n")
	}
	return b.String()
}

func TestInstructionsWholeUnderBudget(t *testing.T) {
	src := "# notes\n\n## Testing\n\nRun go test.\n"
	in := NewInstructions("CLAUDE.md", src, NewBudget(16384))
	got := in.Initial()
	if !strings.Contains(got, "Run go test.") || !strings.HasPrefix(got, "Project instructions, from CLAUDE.md:") {
		t.Fatalf("a file within the budget is whole:\n%s", got)
	}
	if s := in.ForCall("c1", "bash", bash("go test ./...")); s != "" {
		t.Errorf("a whole file brings nothing in for a call: %q", s)
	}
}

func TestInstructionsInitialIsHeadAlwaysAndOutline(t *testing.T) {
	in := NewInstructions("CLAUDE.md", bigFile(), NewBudget(4096))
	got := in.Initial()
	for _, want := range []string{"Intro: read this first.", "Tag from main.", "Staging and committing (lines 5–8)",
		"Testing (lines 9–12)", "  A skip is not a pass (lines 13–16)"} {
		if !strings.Contains(got, want) {
			t.Errorf("the first message lacks %q:\n%s", want, got)
		}
	}
	for _, not := range []string{"Never git add", "Run go test", "filler text"} {
		if strings.Contains(got, not) {
			t.Errorf("the first message holds %q, a section that waits for its call:\n%s", not, got)
		}
	}
	if in.Used() > in.Budget() || in.Used() == 0 {
		t.Errorf("used %d of %d", in.Used(), in.Budget())
	}
}

func TestInstructionsForCall(t *testing.T) {
	in := NewInstructions("CLAUDE.md", bigFile(), NewBudget(4096))
	in.Initial()
	git := in.ForCall("c1", "bash", bash("cd x && git commit -m y"))
	if !strings.Contains(git, "Never git add -A.") || strings.Contains(git, "Run go test") {
		t.Fatalf("a git command brings in the staging section alone:\n%s", git)
	}
	if again := in.ForCall("c2", "bash", bash("git status")); again != "" {
		t.Errorf("a section in the window is not loaded again: %q", again)
	}
	test := in.ForCall("c3", "edit", editOf("internal/x/x_test.go"))
	if !strings.Contains(test, "Run go test with -v.") || !strings.Contains(test, "Read for PASS.") {
		t.Errorf("an edit of a _test.go file brings in Testing and its subsection:\n%s", test)
	}
	if s := in.ForCall("c4", "bash", bash("go test ./...")); s != "" {
		t.Errorf("go test after the testing sections are in: %q", s)
	}
	if s := in.ForCall("c5", "bash", bash("echo night")); s != "" {
		t.Errorf("a marked section matches its marker's command, not a word: %q", s)
	}
	if s := in.ForCall("c6", "bash", bash("night add job")); !strings.Contains(s, "Queue long jobs.") {
		t.Errorf("bash:night brings in the marked section:\n%s", s)
	}
	if s := in.ForCall("c7", "read", editOf("CLAUDE.md")); s != "" {
		t.Errorf("a read matches no kind: %q", s)
	}
	// A compaction that stubs c1's result takes its section out; the next git call brings it back.
	used := in.Used()
	in.Masked([]string{"c1"})
	if in.Used() >= used {
		t.Errorf("masking c1 returned nothing to the budget: %d then %d", used, in.Used())
	}
	if s := in.ForCall("c8", "bash", bash("git diff")); !strings.Contains(s, "Never git add -A.") {
		t.Errorf("after c1 is stubbed, git brings its section back:\n%s", s)
	}
}

func TestInstructionsBudgetIsCumulative(t *testing.T) {
	var b strings.Builder
	b.WriteString("# notes\n\nIntro.\n\n")
	for i := range 6 {
		fmt.Fprintf(&b, "## Testing part %d\n\n%s\n", i, strings.Repeat("rule text for this testing part. ", 40))
	}
	in := NewInstructions("CLAUDE.md", b.String(), NewBudget(4096)) // 614 tokens; each part is about 430
	in.Initial()
	got := in.ForCall("c1", "bash", bash("go test ./..."))
	if !strings.Contains(got, "## Testing part 0") || strings.Contains(got, "## Testing part 1\n") ||
		!strings.Contains(got, "Not loaded, the instruction budget is spent; read CLAUDE.md lines") || !strings.Contains(got, "(Testing part 5)") {
		t.Fatalf("the budget takes the first part and names the rest with their lines:\n%s", got)
	}
	if in.Used() > in.Budget() {
		t.Errorf("used %d of %d", in.Used(), in.Budget())
	}
}

func TestCallKinds(t *testing.T) {
	got := strings.Join(callKinds("bash", bash(`FOO=1 /usr/bin/git -C x status | grep "go test"; go test ./...`)), "|")
	if got != "bash:git|bash:git -C|bash:grep|bash:go|bash:go test" {
		t.Errorf("kinds %q", got)
	}
	if !kindMatches("edit:*_test.go", "edit:a/b_test.go") || kindMatches("edit:*_test.go", "edit:a/b.go") ||
		!kindMatches("edit:docs/*.md", "edit:docs/a.md") || kindMatches("edit:docs/*.md", "edit:a.md") {
		t.Error("edit kinds match by base name, or by path when the pattern has a slash")
	}
}
