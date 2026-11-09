package window

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func hashOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:16]
}

// knowledgeRepo is a workspace with loop.go, and pages on it: one pinned with its hash, one with a hash that does not
// match, one with no hash, one pinned outside the workspace, and one with no pins.
func knowledgeRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	const code = "package loop\n\nfunc Run() {}\n"
	os.MkdirAll(filepath.Join(root, "internal", "loop"), 0o755)
	os.WriteFile(filepath.Join(root, "internal", "loop", "loop.go"), []byte(code), 0o644)
	os.WriteFile(filepath.Join(root, "other.go"), []byte("package x\n"), 0o644)
	kd := filepath.Join(root, ".agents", "knowledge")
	os.MkdirAll(filepath.Join(kd, "sub"), 0o755)
	os.WriteFile(filepath.Join(kd, "loop.md"), []byte("---\ntitle: The agent loop\npins:\n  - ./internal/loop/loop.go sha256:"+
		hashOf(code)+"\n---\nRun ends a turn on done.\n"), 0o644)
	os.WriteFile(filepath.Join(kd, "stale.md"), []byte("---\npins: internal/loop/loop.go sha256:000000000000aaaa\n---\n# Old notes\n\nRun used to retry.\n"), 0o644)
	os.WriteFile(filepath.Join(kd, "sub", "nohash.md"), []byte("---\npins: other.go\n---\nOther is small.\n"), 0o644)
	os.WriteFile(filepath.Join(kd, "escape.md"), []byte("---\npins: ../../etc/passwd sha256:"+hashOf("x")+", /etc/hosts\n---\nnothing\n"), 0o644)
	os.WriteFile(filepath.Join(kd, "free.md"), []byte("Notes with no pins.\n"), 0o644)
	return root
}

func TestParsePage(t *testing.T) {
	pg := ParsePage("a.md", "---\ntitle: T\npins:\n  - a.go sha256:ABCDEF0123456789\n  - b.go sha256:short\n---\nbody\n")
	if pg.Title != "T" || len(pg.Pins) != 2 || pg.Pins[0] != (Pin{"a.go", "abcdef0123456789"}) || pg.Pins[1] != (Pin{"b.go", ""}) || pg.Body != "body\n" {
		t.Errorf("%+v", pg)
	}
	if pg := ParsePage("dir/x.md", "no frontmatter\n"); pg.Title != "x" || len(pg.Pins) != 0 {
		t.Errorf("%+v", pg)
	}
	if pg := ParsePage("x.md", "---\npins: ../a.go sha256:"+hashOf("a")+", /b.go\n---\n# Head\n"); pg.Title != "Head" || len(pg.Pins) != 0 {
		t.Errorf("a pin outside the workspace is dropped: %+v", pg)
	}
}

func TestKnowledgeIndexAndLoads(t *testing.T) {
	root := knowledgeRepo(t)
	pages, err := LoadPages(root)
	if err != nil || len(pages) != 5 {
		t.Fatalf("%d pages, %v", len(pages), err)
	}
	b := NewBudget(16384)
	k := NewKnowledge(root, pages, 16384, b)
	idx := k.Index()
	for _, want := range []string{"- The agent loop (.agents/knowledge/loop.md; pinned to internal/loop/loop.go)",
		"- Old notes (.agents/knowledge/stale.md; pinned to internal/loop/loop.go; possibly out of date)", "- free (.agents/knowledge/free.md)"} {
		if !strings.Contains(idx, want) {
			t.Errorf("index lacks %q:\n%s", want, idx)
		}
	}
	if strings.Contains(idx, "Run ends a turn") || b.Used == 0 {
		t.Errorf("the index carries no page body and counts against the budget (used %d):\n%s", b.Used, idx)
	}
	got := k.ForCall("c1", "read", editOf("internal/loop/loop.go"))
	if !strings.Contains(got, "[Knowledge page .agents/knowledge/loop.md, The agent loop:]\n\nRun ends a turn on done.") {
		t.Errorf("a read of the pinned file loads its fresh page:\n%s", got)
	}
	if !strings.Contains(got, "stale.md is possibly out of date") || strings.Contains(got, "Run used to retry") {
		t.Errorf("a page whose hash does not match is named, not loaded:\n%s", got)
	}
	if again := k.ForCall("c2", "edit", editOf("internal/loop/loop.go")); again != "" {
		t.Errorf("a page in the window is not loaded again: %q", again)
	}
	if s := k.ForCall("c3", "read", editOf("other.go")); !strings.Contains(s, "nohash.md is possibly out of date") {
		t.Errorf("a pin with no hash never matches:\n%s", s)
	}
	if s := k.ForCall("c4", "bash", bash("cat internal/loop/loop.go")); s != "" {
		t.Errorf("bash is not a read of a file here: %q", s)
	}
	used := b.Used
	k.Masked([]string{"c1"})
	if b.Used >= used {
		t.Errorf("masking c1 returned nothing: %d then %d", used, b.Used)
	}
	if s := k.ForCall("c5", "read", editOf("internal/loop/loop.go")); !strings.Contains(s, "Run ends a turn") {
		t.Errorf("after c1 is stubbed its page comes back:\n%s", s)
	}
}

// TestKnowledgeStaleAfterAnEdit: a page fresh when the session starts is out of date once its file changes.
func TestKnowledgeStaleAfterAnEdit(t *testing.T) {
	root := knowledgeRepo(t)
	pages, _ := LoadPages(root)
	os.WriteFile(filepath.Join(root, "internal", "loop", "loop.go"), []byte("package loop\n\nfunc Run2() {}\n"), 0o644)
	k := NewKnowledge(root, pages, 16384, NewBudget(16384))
	if s := k.ForCall("c1", "read", editOf("internal/loop/loop.go")); strings.Contains(s, "Run ends a turn") || !strings.Contains(s, "loop.md is possibly out of date") {
		t.Errorf("a changed file makes its page stale:\n%s", s)
	}
}

func TestLoadPagesWithNoDirectory(t *testing.T) {
	if pages, err := LoadPages(t.TempDir()); err != nil || len(pages) != 0 {
		t.Fatalf("%v %v", pages, err)
	}
}

// TestKnowledgeIndexStaysUnderItsCap: an index of many pages is cut from the end to IndexShare of the window, and says so.
func TestKnowledgeIndexStaysUnderItsCap(t *testing.T) {
	var pages []Page
	for i := range 200 {
		pages = append(pages, Page{Path: KnowledgeDir + "/p" + strings.Repeat("x", 30) + string(rune('a'+i%26)) + ".md", Title: "a page title"})
	}
	k := NewKnowledge(t.TempDir(), pages, 16384, NewBudget(16384))
	idx := k.Index()
	if SectionTokens(idx) > 16384*IndexShare/100 || !strings.Contains(idx, "more pages]") {
		t.Fatalf("index of %d tokens against a cap of %d:\n%s", SectionTokens(idx), 16384*IndexShare/100, idx)
	}
}

// TestKnowledgeReusedIDs: a compaction of a reused id frees the pages of its oldest call, and Drop those of its newest.
func TestKnowledgeReusedIDs(t *testing.T) {
	root := knowledgeRepo(t)
	pages, _ := LoadPages(root)
	k := NewKnowledge(root, pages, 16384, NewBudget(16384))
	k.ForCall("c", "read", editOf("internal/loop/loop.go"))
	k.ForCall("c", "read", editOf("other.go"))
	k.Masked([]string{"c"})
	if s := k.ForCall("d", "read", editOf("other.go")); s != "" {
		t.Errorf("the newer c's page was freed by a compaction of the older: %q", s)
	}
	if s := k.ForCall("e", "read", editOf("internal/loop/loop.go")); !strings.Contains(s, "Run ends a turn") {
		t.Errorf("the older c's page should be free:\n%s", s)
	}
	k.Drop("e")
	if s := k.ForCall("f", "read", editOf("internal/loop/loop.go")); !strings.Contains(s, "Run ends a turn") {
		t.Errorf("Drop of e should free its page:\n%s", s)
	}
}

// TestKnowledgeLoadsOnAnEdit: a page fresh when the session began loads on an edit of its file, although the edit has
// already changed the file by the time the loop asks.
func TestKnowledgeLoadsOnAnEdit(t *testing.T) {
	root := knowledgeRepo(t)
	pages, _ := LoadPages(root)
	k := NewKnowledge(root, pages, 16384, NewBudget(16384))
	os.WriteFile(filepath.Join(root, "internal", "loop", "loop.go"), []byte("package loop\n\nfunc Run() { edited() }\n"), 0o644)
	if s := k.ForCall("c1", "edit", editOf("internal/loop/loop.go")); !strings.Contains(s, "Run ends a turn on done.") {
		t.Errorf("an edit of the pinned file did not load its page:\n%s", s)
	}
}

// bigPageRepo is knowledgeRepo with big.go and a page pinned to it, hashed, of about 400 tokens with its framing.
func bigPageRepo(t *testing.T) string {
	t.Helper()
	root := knowledgeRepo(t)
	os.WriteFile(filepath.Join(root, "big.go"), []byte("package big\n"), 0o644)
	os.WriteFile(filepath.Join(root, ".agents", "knowledge", "big.md"), []byte("---\ntitle: Big\npins: big.go sha256:"+
		hashOf("package big\n")+"\n---\n"+strings.Repeat("a long note about big. ", 50)+"\n"), 0o644)
	return root
}

// TestKnowledgeSharesTheInstructionBudget: the instruction sections a go test brings in (about 300 tokens of a 4096
// window's 614) leave the page too little room, so it is named; once a compaction frees the sections' share, the next
// read of its file loads it.
func TestKnowledgeSharesTheInstructionBudget(t *testing.T) {
	root := bigPageRepo(t)
	pages, _ := LoadPages(root)
	b := NewBudget(4096)
	src := "# notes\n\nIntro.\n\n## Testing\n\n" + strings.Repeat("testing rule words. ", 45) + "\n\n" + filler
	in := NewInstructions("CLAUDE.md", src, b)
	in.Initial()
	if in.ForCall("c0", "bash", bash("go test ./...")) == "" {
		t.Fatal("go test brings the testing section in")
	}
	k := NewKnowledge(root, pages, 4096, b)
	if s := k.ForCall("c1", "read", editOf("big.go")); strings.Contains(s, "a long note") || !strings.Contains(s, "big.md is not loaded") {
		t.Fatalf("a page past the shared budget is named (used %d of %d):\n%s", b.Used, b.Limit, s)
	}
	in.Masked([]string{"c0"})
	if s := k.ForCall("c2", "read", editOf("big.go")); !strings.Contains(s, "a long note") {
		t.Errorf("after the sections' share is freed, the page loads (used %d of %d):\n%s", b.Used, b.Limit, s)
	}
	if b.Used > b.Limit {
		t.Errorf("used %d of %d", b.Used, b.Limit)
	}
}

// TestKnowledgeIndexAndInstructionsStayInTheBudget: the index takes no more than the budget has left, and an
// instruction file is whole only if it fits what is left.
func TestKnowledgeIndexAndInstructionsStayInTheBudget(t *testing.T) {
	root := knowledgeRepo(t)
	pages, _ := LoadPages(root)
	b := NewBudget(16384)
	b.Used = b.Limit - 10
	if idx := NewKnowledge(root, pages, 16384, b).Index(); b.Used > b.Limit {
		t.Errorf("the index took the budget past its limit: used %d of %d\n%s", b.Used, b.Limit, idx)
	}
	b = NewBudget(16384)
	b.Used = b.Limit - 5
	if NewInstructions("CLAUDE.md", "# notes\n\n## Testing\n\nRun go test with -v before every push.\n", b).Whole() {
		t.Error("a file over what is left is loaded whole")
	}
}

// TestKnowledgeIndexWithNoRoomIsEmpty: when not one page line fits, there is no index at all, not a header over
// "[… N more pages]".
func TestKnowledgeIndexWithNoRoomIsEmpty(t *testing.T) {
	root := knowledgeRepo(t)
	pages, _ := LoadPages(root)
	b := NewBudget(16384)
	b.Used = b.Limit - 55 // room for the header and its "more pages" line, not for a page line too
	if idx := NewKnowledge(root, pages, 16384, b).Index(); idx != "" {
		t.Errorf("index %q", idx)
	}
}

// TestKnowledgeNeverPassesTheBudget: a notice that does not fit what is left is not given, so the budget is never
// passed; it is given at a later call once it fits.
func TestKnowledgeNeverPassesTheBudget(t *testing.T) {
	root := knowledgeRepo(t)
	pages, _ := LoadPages(root)
	b := NewBudget(16384)
	k := NewKnowledge(root, pages, 16384, b)
	b.Used = b.Limit - 3
	k.ForCall("c1", "read", editOf("internal/loop/loop.go"))
	if b.Used > b.Limit {
		t.Fatalf("used %d of %d", b.Used, b.Limit)
	}
	b.Used = 0
	if s := k.ForCall("c2", "read", editOf("internal/loop/loop.go")); !strings.Contains(s, "Run ends a turn") || !strings.Contains(s, "stale.md is possibly out of date") {
		t.Errorf("with room, the page and the stale notice come:\n%s", s)
	}
}

// TestKnowledgeDropKeepsAnEarlierNotice: a page named for want of room, then loaded by a call whose note is dropped,
// is still named by the first notice, so it is not named again.
func TestKnowledgeDropKeepsAnEarlierNotice(t *testing.T) {
	root := bigPageRepo(t)
	pages, _ := LoadPages(root)
	b := NewBudget(4096)
	k := NewKnowledge(root, pages, 4096, b)
	b.Used = 300
	if s := k.ForCall("c1", "read", editOf("big.go")); !strings.Contains(s, "big.md is not loaded") {
		t.Fatalf("c1 names the page:\n%s", s)
	}
	b.Used -= 300
	if s := k.ForCall("c2", "read", editOf("big.go")); !strings.Contains(s, "a long note") {
		t.Fatalf("c2 loads it:\n%s", s)
	}
	k.Drop("c2")
	b.Used += 300
	if s := k.ForCall("c3", "read", editOf("big.go")); s != "" {
		t.Errorf("the page, still named by c1's notice, was named again:\n%s", s)
	}
}
