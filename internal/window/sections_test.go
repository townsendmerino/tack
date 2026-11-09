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

// TestMarkersOutsideCodeOnly: a marker quoted in a fenced block, in a long fence, in inline code or in frontmatter
// sets nothing; one in prose does.
func TestMarkersOutsideCodeOnly(t *testing.T) {
	src := "---\nx: <!-- tack:always -->\n---\n# A\n\nUse `<!-- tack:on bash:git -->` to mark one.\n\n" + "````\n" + "```\n<!-- tack:always -->\n" + "```\n" +
		"````\n\n## B\n<!-- tack:on bash:make -->\n"
	got := ParseSections(src)
	if len(got) != 3 || got[0].Always || got[1].Always || len(got[1].On) != 0 || len(got[2].On) != 1 || got[2].On[0] != "bash:make" {
		t.Fatalf("sections %+v", got)
	}
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
	in := NewInstructions("CLAUDE.md", src, 16384)
	got := in.Initial()
	if !strings.Contains(got, "Run go test.") || !strings.HasPrefix(got, "Project instructions, from CLAUDE.md:") {
		t.Fatalf("a file within the budget is whole:\n%s", got)
	}
	if s := in.ForCall("c1", "bash", bash("go test ./...")); s != "" {
		t.Errorf("a whole file brings nothing in for a call: %q", s)
	}
}

func TestInstructionsInitialIsHeadAlwaysAndOutline(t *testing.T) {
	in := NewInstructions("CLAUDE.md", bigFile(), 4096)
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
	in := NewInstructions("CLAUDE.md", bigFile(), 4096)
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
		fmt.Fprintf(&b, "## Testing part %d\n\n%s\n", i, strings.Repeat("rule text for this testing part. ", 25))
	}
	in := NewInstructions("CLAUDE.md", b.String(), 4096) // 614 tokens; each part is about 270
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
	if got != "bash:git -C x status|bash:git status|bash:grep go test|bash:go test ./..." {
		t.Errorf("kinds %q", got)
	}
	if !kindMatches("edit:*_test.go", "edit:a/b_test.go") || kindMatches("edit:*_test.go", "edit:a/b.go") ||
		!kindMatches("edit:docs/*.md", "edit:docs/a.md") || kindMatches("edit:docs/*.md", "edit:a.md") {
		t.Error("edit kinds match by base name, or by path when the pattern has a slash")
	}
}

// filler pushes a file over a 4096 window's budget.
var filler = "## Filler\n\n" + strings.Repeat("a line of filler text to push the file over its budget\n", 300)

// TestForCallSubsectionsAndTitle: a matching section brings its subsections except one with its own tack:on marker;
// a matching level-1 title brings in only itself, never the sections under it.
func TestForCallSubsectionsAndTitle(t *testing.T) {
	src := "# notes\n\nIntro.\n\n## Testing\n\nRun go test.\n\n### Benchmarks\n<!-- tack:on bash:bench -->\nNight only.\n\n### Flakes\n\nRetry once.\n\n" + filler
	in := NewInstructions("CLAUDE.md", src, 4096)
	in.Initial()
	got := in.ForCall("c1", "bash", bash("go test ./..."))
	if !strings.Contains(got, "Run go test.") || !strings.Contains(got, "Retry once.") || strings.Contains(got, "Night only.") {
		t.Errorf("Testing brings Flakes, not the marked Benchmarks:\n%s", got)
	}
	title := "# Testing guide\n\nIntro.\n\n## Releases\n\nTag from main.\n\n" + filler
	in = NewInstructions("CLAUDE.md", title, 4096)
	in.Initial()
	if got := in.ForCall("c1", "bash", bash("go test ./...")); strings.Contains(got, "Tag from main.") || strings.Contains(got, "filler text") {
		t.Errorf("a matching title pulled in the sections under it:\n%s", got)
	}
}

// TestForCallNamesWhatDoesNotFitOnce: sections past the budget are named once, without an empty rules header, and a
// later matching call says nothing more; every note is charged in full, framing included.
func TestForCallNamesWhatDoesNotFitOnce(t *testing.T) {
	src := "# notes\n\nIntro.\n\n## Testing\n\n" + strings.Repeat("a long testing rule that will not fit. ", 120) + "\n\n" + filler
	in := NewInstructions("CLAUDE.md", src, 4096)
	initial := SectionTokens(in.Initial())
	first := in.ForCall("c1", "bash", bash("go test ./..."))
	if !strings.Contains(first, "Not loaded, the instruction budget is spent") || strings.Contains(first, "[Project rules from") {
		t.Fatalf("a call whose sections all miss names them, with no empty header:\n%q", first)
	}
	if again := in.ForCall("c2", "bash", bash("go test ./x")); again != "" {
		t.Errorf("a section already named is named again: %q", again)
	}
	if in.Used() != initial+SectionTokens(first) {
		t.Errorf("used %d, want the first message's %d and the note's %d", in.Used(), initial, SectionTokens(first))
	}
	in.Masked([]string{"c1"})
	if in.Used() != initial {
		t.Errorf("after c1 is stubbed: used %d, want %d", in.Used(), initial)
	}
	if s := in.ForCall("c3", "bash", bash("go test ./...")); !strings.Contains(s, "Not loaded") {
		t.Errorf("after c1 is stubbed the section is named again: %q", s)
	}
}

// TestForCallChargesTheFraming: a note's whole text is charged, header included.
func TestForCallChargesTheFraming(t *testing.T) {
	in := NewInstructions("CLAUDE.md", bigFile(), 4096)
	initial := SectionTokens(in.Initial())
	note := in.ForCall("c1", "bash", bash("git status"))
	if note == "" || in.Used() != initial+SectionTokens(note) {
		t.Errorf("used %d, want %d + %d", in.Used(), initial, SectionTokens(note))
	}
}

// TestInitialKeepsTheOutlineBeforeAlways: an always section too big to fit gives way before the outline does, and keeps
// its outline entry, in file order.
func TestInitialKeepsTheOutlineBeforeAlways(t *testing.T) {
	src := "# notes\n\nIntro.\n\n## Alpha\n\na\n\n## Big\n<!-- tack:always -->\n" + strings.Repeat("always text that is too long. ", 150) +
		"\n\n## Omega\n\no\n\n" + filler
	got := NewInstructions("CLAUDE.md", src, 4096).Initial()
	a, b, o := strings.Index(got, "Alpha (lines"), strings.Index(got, "Big (lines"), strings.Index(got, "Omega (lines")
	if a < 0 || b < a || o < b || strings.Contains(got, "more headings") || strings.Contains(got, "always text") {
		t.Errorf("the outline stays whole and in order, the big always section only named:\n%s", got)
	}
}

func TestKindMatchesCleansPaths(t *testing.T) {
	if !kindMatches("edit:docs/*.md", "edit:./docs/a.md") || !kindMatches("edit:./docs/*.md", "edit:docs/a.md") {
		t.Error("edit kinds are cleaned before matching")
	}
}

// TestFirstSectionSkipsFrontmatterAndUnderlines: frontmatter, and a setext title's underline, are not the opening's
// body; the opening runs on to the first section with text.
func TestFirstSectionSkipsFrontmatterAndUnderlines(t *testing.T) {
	front := "---\ntitle: x\n---\n# notes\n\nIntro here.\n\n## Alpha\n\na\n\n" + filler
	if got := NewInstructions("CLAUDE.md", front, 4096).Initial(); !strings.Contains(got, "Intro here.") || !strings.Contains(got, "Alpha (lines") {
		t.Errorf("frontmatter taken for the opening:\n%s", got)
	}
	setext := "Title\n=====\n\n## Intro\n\nHello there.\n\n## Beta\n\nb\n\n" + filler
	if got := NewInstructions("CLAUDE.md", setext, 4096).Initial(); !strings.Contains(got, "Hello there.") || !strings.Contains(got, "Beta (lines") {
		t.Errorf("a setext underline taken for body:\n%s", got)
	}
}

// TestForCallSkipsAMarkedSubsectionsSubtree: a subsection with its own marker is left out with everything under it.
func TestForCallSkipsAMarkedSubsectionsSubtree(t *testing.T) {
	src := "# notes\n\nIntro.\n\n## Testing\n\nRun go test.\n\n### Benchmarks\n<!-- tack:on bash:bench -->\nNight only.\n\n" +
		"#### Test profiling\n\nUse pprof.\n\n### Flakes\n\nRetry once.\n\n" + filler
	in := NewInstructions("CLAUDE.md", src, 4096)
	in.Initial()
	got := in.ForCall("c1", "bash", bash("go test ./..."))
	if !strings.Contains(got, "Run go test.") || !strings.Contains(got, "Retry once.") || strings.Contains(got, "Use pprof.") {
		t.Errorf("the marked subsection's own subsection came along:\n%s", got)
	}
}

// TestInitialTrimsTheOutlineFromTheEnd: when the outline must shrink after always sections gave way, entries go from
// the end in file order, a given-way always entry among them.
func TestInitialTrimsTheOutlineFromTheEnd(t *testing.T) {
	var b strings.Builder
	b.WriteString("# notes\n\n" + strings.Repeat("intro words. ", 40) + "\n\n")
	for i := range 40 {
		fmt.Fprintf(&b, "## Section number %02d with a long heading\n\nx\n\n", i)
	}
	b.WriteString("## Zed always\n<!-- tack:always -->\n" + strings.Repeat("always text. ", 200) + "\n")
	got := NewInstructions("CLAUDE.md", b.String(), 4096).Initial()
	if !strings.Contains(got, "more headings]") || strings.Contains(got, "Zed always (lines") || !strings.Contains(got, "Section number 00") {
		t.Errorf("the outline was trimmed out of file order:\n%s", got)
	}
}

// TestNotesWithEmptyOrReusedIDs: an empty id never touches the first message's sections, and a reused id frees its
// oldest note on a compaction and its newest on Drop.
func TestNotesWithEmptyOrReusedIDs(t *testing.T) {
	in := NewInstructions("CLAUDE.md", bigFile(), 4096)
	initial := SectionTokens(in.Initial())
	if in.ForCall("", "bash", bash("git status")) == "" {
		t.Fatal("git brings staging in")
	}
	in.Masked([]string{""})
	if in.Used() != initial {
		t.Errorf("after the empty-id note is stubbed: used %d, want %d", in.Used(), initial)
	}
	if s := in.ForCall("x", "bash", bash("git status")); !strings.Contains(s, "Never git add -A.") || strings.Contains(s, "Tag from main.") {
		t.Errorf("the first message's sections left with the empty id:\n%s", s)
	}
	in = NewInstructions("CLAUDE.md", bigFile(), 4096)
	in.Initial()
	in.ForCall("c", "bash", bash("git status"))
	in.ForCall("c", "bash", bash("go test ./..."))
	in.Masked([]string{"c"})
	if s := in.ForCall("d", "bash", bash("go test ./...")); s != "" {
		t.Errorf("a compaction of the older c freed the newer c's testing section: %q", s)
	}
	if s := in.ForCall("e", "bash", bash("git diff")); !strings.Contains(s, "Never git add -A.") {
		t.Errorf("a compaction of the older c should free staging:\n%s", s)
	}
	in.Drop("e")
	if s := in.ForCall("f", "bash", bash("git diff")); !strings.Contains(s, "Never git add -A.") {
		t.Errorf("Drop of e should free staging again:\n%s", s)
	}
}

// TestMarkersInMultiBacktickSpans: a marker quoted in a double-backtick span sets nothing.
func TestMarkersInMultiBacktickSpans(t *testing.T) {
	got := ParseSections("# A\n\nWrite `` `<!-- tack:always -->` `` to mark one.\n")
	if got[0].Always {
		t.Error("a marker in a double-backtick span was read")
	}
	if !ParseSections("# A\n\n<!-- tack:always --> and `code`\n")[0].Always {
		t.Error("a marker in prose beside a code span is read")
	}
}

// TestWholeFileChargesItsFraming: a whole file's charge is the text it gives, framing included.
func TestWholeFileChargesItsFraming(t *testing.T) {
	in := NewInstructions("CLAUDE.md", "# notes\n\n## Testing\n\nRun go test.\n", 16384)
	if got := in.Initial(); in.Used() != SectionTokens(got) || !in.Whole() {
		t.Errorf("used %d, the text's %d, whole %v", in.Used(), SectionTokens(got), in.Whole())
	}
}

// TestMarkedSectionGovernsItsSubtree: under a section with its own tack:on marker, a subsection is the marker's to
// bring: its heading's default words do not match on their own.
func TestMarkedSectionGovernsItsSubtree(t *testing.T) {
	src := "# notes\n\nIntro.\n\n## Night queue\n<!-- tack:on bash:night -->\nQueue long jobs.\n\n### Testing at night\n\nNo go test by night.\n\n" + filler
	in := NewInstructions("CLAUDE.md", src, 4096)
	in.Initial()
	if s := in.ForCall("c1", "bash", bash("go test ./...")); s != "" {
		t.Errorf("go test pulled in a subsection of a marked section:\n%s", s)
	}
	if s := in.ForCall("c2", "bash", bash("night add x")); !strings.Contains(s, "Queue long jobs.") || !strings.Contains(s, "No go test by night.") {
		t.Errorf("the marker brings its section and subsection:\n%s", s)
	}
}

// TestCutOpeningLeavesRoom: an opening too long for the budget is cut to half of it, counts as in, and leaves room
// for the sections a call brings.
func TestCutOpeningLeavesRoom(t *testing.T) {
	src := "# notes\n\n" + strings.Repeat("an opening line that goes on and on.\n", 200) + "\n## Testing\n\nRun go test.\n\n" + filler
	in := NewInstructions("CLAUDE.md", src, 4096)
	got := in.Initial()
	if !strings.Contains(got, "cut to fit the window; read CLAUDE.md lines 1–") || in.Used() > in.Budget()*2/3 {
		t.Fatalf("used %d of %d:\n%.300s", in.Used(), in.Budget(), got)
	}
	if s := in.ForCall("c1", "bash", bash("go test ./...")); !strings.Contains(s, "Run go test.") {
		t.Errorf("no room left after the cut opening:\n%s", s)
	}
}

// TestWholeCountsItsFraming: a file whose text fits but whose framed text does not is not loaded whole.
func TestWholeCountsItsFraming(t *testing.T) {
	budget := 16384 * SectionShare / 100
	var b strings.Builder
	b.WriteString("# n\n\n")
	for SectionTokens(b.String()+"x") <= budget-2 {
		b.WriteString("x")
	}
	in := NewInstructions("CLAUDE.md", b.String(), 16384)
	in.Initial()
	if in.Used() > in.Budget() {
		t.Errorf("used %d of %d (whole %v)", in.Used(), in.Budget(), in.Whole())
	}
}

// TestAlwaysBringsItsSubsections: an always section brings its unmarked subsections with it, and an opening of a bare
// title stops before a marked section.
func TestAlwaysBringsItsSubsections(t *testing.T) {
	src := "# notes\n\nIntro.\n\n## Releases\n<!-- tack:always -->\nTag from main.\n\n### Hotfixes\n\nBranch from the tag.\n\n" + filler
	if got := NewInstructions("CLAUDE.md", src, 4096).Initial(); !strings.Contains(got, "Branch from the tag.") {
		t.Errorf("the always section's subsection was left out:\n%s", got)
	}
	bare := "# notes\n\n## Night queue\n<!-- tack:on bash:night -->\nQueue long jobs.\n\n## Other\n\nx\n\n" + filler
	if got := NewInstructions("CLAUDE.md", bare, 4096).Initial(); strings.Contains(got, "Queue long jobs.") {
		t.Errorf("a bare title's opening took in a marked section:\n%s", got)
	}
}

// TestCallKindsSkipFlags: a flag before a subcommand does not hide it, a marker of several words matches the words it
// names, and ** in an edit pattern crosses directories.
func TestCallKindsSkipFlags(t *testing.T) {
	src := "# notes\n\nIntro.\n\n## Testing\n\nRun go test.\n\n## Amending\n<!-- tack:on bash:git commit --amend -->\nNever amend a pushed commit.\n\n" + filler
	in := NewInstructions("CLAUDE.md", src, 4096)
	in.Initial()
	if s := in.ForCall("c1", "bash", bash("go -C pkg test ./...")); !strings.Contains(s, "Run go test.") {
		t.Errorf("go -C pkg test:\n%s", s)
	}
	if s := in.ForCall("c2", "bash", bash("git commit -m x")); strings.Contains(s, "Never amend") {
		t.Errorf("git commit without --amend matched the amend marker:\n%s", s)
	}
	if s := in.ForCall("c3", "bash", bash("git -c x=y commit --amend --no-edit")); !strings.Contains(s, "Never amend") {
		t.Errorf("git commit --amend:\n%s", s)
	}
	if !kindMatches("edit:internal/**/*_test.go", "edit:internal/window/sections_test.go") || !kindMatches("edit:internal/**/*_test.go", "edit:internal/a_test.go") ||
		kindMatches("edit:internal/**/*_test.go", "edit:cmd/a_test.go") || kindMatches("edit:internal/*_test.go", "edit:internal/window/a_test.go") {
		t.Error("** crosses directories; * stays in one")
	}
}

// TestCallKindsReadQuotedWords: a quoted word is read as its text, and a word that is not plain text stands as a
// placeholder, so the words after either still count.
func TestCallKindsReadQuotedWords(t *testing.T) {
	src := "# notes\n\nIntro.\n\n## Amending\n<!-- tack:on bash:git commit --amend -->\nNever amend a pushed commit.\n\n" + filler
	in := NewInstructions("CLAUDE.md", src, 4096)
	in.Initial()
	if s := in.ForCall("c1", "bash", bash(`git -C "some dir" commit -m "msg" --amend`)); !strings.Contains(s, "Never amend") {
		t.Errorf("quoted words hid --amend:\n%s", s)
	}
	if got := strings.Join(callKinds("bash", bash(`git commit -m "$MSG" --amend`)), "|"); !strings.Contains(got, "bash:git commit -m ? --amend") {
		t.Errorf("kinds %q", got)
	}
}

// TestMarkedTitleBringsItsSubtree: a level-1 section with a tack:on marker brings its subsections when it matches,
// and as the file's first section it waits for its call instead of opening the first message.
func TestMarkedTitleBringsItsSubtree(t *testing.T) {
	src := "# Night queue\n<!-- tack:on bash:night -->\nQueue long jobs.\n\n## Fitting\n\nFit-check first.\n\n# Other\n\nx\n\n" + filler
	in := NewInstructions("CLAUDE.md", src, 4096)
	if got := in.Initial(); strings.Contains(got, "Queue long jobs.") {
		t.Errorf("a marked first section opened the message:\n%s", got)
	}
	if s := in.ForCall("c1", "bash", bash("night add x")); !strings.Contains(s, "Queue long jobs.") || !strings.Contains(s, "Fit-check first.") {
		t.Errorf("the marked title should bring its subsection:\n%s", s)
	}
}
