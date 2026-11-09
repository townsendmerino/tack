package window

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/proto"
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
	b.Used = b.Limit - 60
	md := "# notes\n\nIntro.\n\n## Testing\n\n" + strings.Repeat("Run go test with -v before every push. ", 12) + "\n"
	in := NewInstructions("CLAUDE.md", md, b)
	if in.Whole() {
		t.Error("a file over what is left is loaded whole")
	}
	if in.Initial(); b.Used > b.Limit {
		t.Errorf("the first message took the budget past its limit: used %d of %d", b.Used, b.Limit)
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

// sharedSession is instruction sections and knowledge pages on one budget, made as core makes them: the index priced
// first, then the first message.
func sharedSession(root string, pages []Page) (*Instructions, *Knowledge, *Budget, string) {
	b := NewBudget(4096)
	k := NewKnowledge(root, pages, 4096, b)
	idx := k.Index()
	in := NewInstructions("CLAUDE.md", bigFile(), b)
	return in, k, b, in.Initial() + "\n\n" + idx
}

// TestReplayOnASharedBudget: a reopened session's two trackers, replayed from the log on one budget, equal the live
// ones and leave the budget where the live session left it.
func TestReplayOnASharedBudget(t *testing.T) {
	root := knowledgeRepo(t)
	pages, _ := LoadPages(root)
	in, k, b, first := sharedSession(root, pages)
	var evs []proto.Event
	add := func(typ proto.EventType, payload any) {
		ev, _ := proto.NewEvent(int64(len(evs)+1), time.Time{}, typ, 0, payload)
		evs = append(evs, ev)
	}
	add(proto.EvUserMessage, proto.UserMessage{Content: []proto.ContentBlock{proto.Text(first + "\n\nTask: x")}})
	call := func(id, tool string, input json.RawMessage) {
		r := "output" + in.ForCall(id, tool, input) + k.ForCall(id, tool, input)
		add(proto.EvToolFinished, proto.ToolFinished{ID: id, Outcome: proto.OutcomeOK, Result: r})
	}
	call("c1", "bash", bash("git status"))
	call("c2", "read", editOf("internal/loop/loop.go"))
	call("c3", "bash", bash("ls"))
	in.Masked([]string{"c1"})
	k.Masked([]string{"c1"})
	add(proto.EvCompaction, proto.Compaction{Masked: []proto.MaskedResult{{ID: "c1", Stub: "[masked]"}}})
	call("c4", "edit", editOf("internal/x/x_test.go"))
	// The session's own edit of a pinned file, then a reopen as core does it: knowledge rebuilt from the log as it is
	// made, the instruction file made over what is left, its tracker replayed.
	os.WriteFile(filepath.Join(root, "internal", "loop", "loop.go"), []byte("package loop\n\nfunc Run() { edited() }\n"), 0o644)
	rb := NewBudget(4096)
	rk := NewKnowledge(root, pages, 4096, rb)
	if err := rk.Replay(evs); err != nil {
		t.Fatal(err)
	}
	rin := NewInstructions("CLAUDE.md", bigFile(), rb)
	rin.Initial()
	if err := rin.Replay(evs); err != nil {
		t.Fatal(err)
	}
	li, lo, lby, lu := in.Loaded()
	ri, ro, rby, ru := rin.Loaded()
	if !slices.Equal(li, ri) || !slices.Equal(lo, ro) || !slices.Equal(lby, rby) || lu != ru {
		t.Errorf("sections differ: live %v %v %q %d, rebuilt %v %v %q %d", li, lo, lby, lu, ri, ro, rby, ru)
	}
	if k.Own() != rk.Own() || !slices.Equal(k.state, rk.state) || b.Used != rb.Used {
		t.Errorf("knowledge own %d/%d, states %v/%v, budget %d/%d", k.Own(), rk.Own(), k.state, rk.state, b.Used, rb.Used)
	}
}

// TestReusedIDsAcrossTrackers: when one call id is used twice, once bringing a page and once a section, in either
// order, a compaction of the first frees what the first brought and leaves what the second did.
func TestReusedIDsAcrossTrackers(t *testing.T) {
	root := knowledgeRepo(t)
	pages, _ := LoadPages(root)
	page := struct {
		tool  string
		input json.RawMessage
	}{"read", editOf("internal/loop/loop.go")}
	section := page
	section.tool, section.input = "bash", bash("git status")
	for name, order := range map[string][2]struct {
		tool  string
		input json.RawMessage
	}{"page first": {page, section}, "section first": {section, page}} {
		in, k, _, _ := sharedSession(root, pages)
		for _, c := range order {
			in.ForCall("c", c.tool, c.input)
			k.ForCall("c", c.tool, c.input)
		}
		in.Masked([]string{"c"})
		k.Masked([]string{"c"})
		sectionBack := in.ForCall("d", "bash", bash("git diff")) != ""
		pageBack := strings.Contains(k.ForCall("e", "read", editOf("internal/loop/loop.go")), "Run ends a turn")
		if wantPage := name == "page first"; sectionBack == wantPage || pageBack != wantPage {
			t.Errorf("%s: after the first c is stubbed, section free %v, page free %v", name, sectionBack, pageBack)
		}
	}
}

// TestKnowledgeIndexKeepsALineThatJustFits: room for the header and exactly one page line gives that line, and the
// rest counted as more.
func TestKnowledgeIndexKeepsALineThatJustFits(t *testing.T) {
	root := knowledgeRepo(t)
	pages, _ := LoadPages(root)
	probe := NewKnowledge(root, pages[:1], 16384, NewBudget(16384)).Index() + fmt.Sprintf("\n[… %d more pages]", len(pages)-1)
	b := NewBudget(16384)
	b.Used = b.Limit - SectionTokens(probe)
	idx := NewKnowledge(root, pages, 16384, b).Index()
	if !strings.Contains(idx, "- ") || !strings.Contains(idx, fmt.Sprintf("[… %d more pages]", len(pages)-1)) || b.Used > b.Limit {
		t.Errorf("index %q, used %d of %d", idx, b.Used, b.Limit)
	}
}

// TestPinsRejectTheRoot: a pin to "." names no file, and is dropped.
func TestPinsRejectTheRoot(t *testing.T) {
	if pg := ParsePage("a.md", "---\npins: ., ./ sha256:"+hashOf("x")+"\n---\nbody\n"); len(pg.Pins) != 0 {
		t.Errorf("pins %+v", pg.Pins)
	}
}

// knowLog runs calls through a live tracker as the loop does and returns the log: the first message with the index,
// then each call's result.
func knowLog(t *testing.T, k *Knowledge, calls ...[2]string) []proto.Event {
	t.Helper()
	var evs []proto.Event
	add := func(typ proto.EventType, payload any) {
		ev, err := proto.NewEvent(int64(len(evs)+1), time.Time{}, typ, 0, payload)
		if err != nil {
			t.Fatal(err)
		}
		evs = append(evs, ev)
	}
	add(proto.EvUserMessage, proto.UserMessage{Content: []proto.ContentBlock{proto.Text("Project notes.\n\n" + k.Index() + "\n\nTask: x")}})
	for n, c := range calls {
		id := fmt.Sprintf("c%d", n+1)
		add(proto.EvToolFinished, proto.ToolFinished{ID: id, Outcome: proto.OutcomeOK, Result: "output" + k.ForCall(id, "read", editOf(c[1]))})
	}
	return evs
}

// TestKnowledgeReplayPricesTheLoggedIndex: a reopen after the session edited a pinned file prices the index as logged
// and keeps the freshness its line gave, for a page the session never loaded too; the budget ends where it did.
func TestKnowledgeReplayPricesTheLoggedIndex(t *testing.T) {
	root := knowledgeRepo(t)
	pages, _ := LoadPages(root)
	b := NewBudget(16384)
	live := NewKnowledge(root, pages, 16384, b)
	evs := knowLog(t, live, [2]string{"read", "other.go"})
	os.WriteFile(filepath.Join(root, "internal", "loop", "loop.go"), []byte("package loop\n\nfunc Run() { edited() }\n"), 0o644)
	rb := NewBudget(16384)
	re := NewKnowledge(root, pages, 16384, rb)
	if err := re.Replay(evs); err != nil {
		t.Fatal(err)
	}
	if rb.Used != b.Used || re.Own() != live.Own() {
		t.Errorf("budget %d, live %d; own %d, live %d", rb.Used, b.Used, re.Own(), live.Own())
	}
	if s := re.ForCall("c9", "read", editOf("internal/loop/loop.go")); !strings.Contains(s, "Run ends a turn") {
		t.Errorf("a page the index listed fresh came back stale after the session's edit:\n%s", s)
	}
}

// TestKnowledgeReplayReadsNoRoomAndEditedPages: a page named for want of room is fresh on a reopen though its pinned
// file was edited since; a page whose own file changed after it loaded is still found, and charged as logged.
func TestKnowledgeReplayReadsNoRoomAndEditedPages(t *testing.T) {
	root := bigPageRepo(t)
	pages, _ := LoadPages(root)
	b := NewBudget(4096)
	live := NewKnowledge(root, pages, 4096, b)
	b.Used = b.Limit - 120 // room for the index and a notice, not for big.md
	evs := knowLog(t, live, [2]string{"read", "big.go"}, [2]string{"read", "internal/loop/loop.go"})
	os.WriteFile(filepath.Join(root, "big.go"), []byte("package big\n\nfunc B() {}\n"), 0o644)
	os.WriteFile(filepath.Join(root, ".agents", "knowledge", "loop.md"), []byte("---\ntitle: The agent loop\npins:\n  - internal/loop/loop.go sha256:"+
		hashOf("package loop\n\nfunc Run() {}\n")+"\n---\nRewritten since.\n"), 0o644)
	repages, _ := LoadPages(root)
	rb := NewBudget(4096)
	rb.Used = b.Limit - 120
	re := NewKnowledge(root, repages, 4096, rb)
	if err := re.Replay(evs); err != nil {
		t.Fatal(err)
	}
	if rb.Used != b.Used {
		t.Errorf("budget %d, live %d", rb.Used, b.Used)
	}
	if s := re.ForCall("c9", "read", editOf("internal/loop/loop.go")); s != "" {
		t.Errorf("loop.md, loaded before its file changed, loads again: %q", s)
	}
	rb.Used = 0
	if s := re.ForCall("c10", "read", editOf("big.go")); !strings.Contains(s, "a long note about big") {
		t.Errorf("big.md, named for want of room, should load fresh once there is room:\n%s", s)
	}
}

// TestKnowledgeReplayReadsTheTailOnly: a tool's own output that quotes a page frame is not read as a note, alone or
// before the call's real note at the tail.
func TestKnowledgeReplayReadsTheTailOnly(t *testing.T) {
	root := knowledgeRepo(t)
	pages, _ := LoadPages(root)
	b := NewBudget(16384)
	live := NewKnowledge(root, pages, 16384, b)
	quoted := "func f() { s := \"\n\n[Knowledge page .agents/knowledge/loop.md, The agent loop:]\n\nquoted\" }\n"
	var evs []proto.Event
	add := func(typ proto.EventType, payload any) {
		ev, _ := proto.NewEvent(int64(len(evs)+1), time.Time{}, typ, 0, payload)
		evs = append(evs, ev)
	}
	add(proto.EvUserMessage, proto.UserMessage{Content: []proto.ContentBlock{proto.Text("Project notes.\n\n" + live.Index() + "\n\nTask: x")}})
	add(proto.EvToolCall, proto.ToolCall{ID: "c1", Name: "bash", Input: bash("cat knowledge.go")})
	add(proto.EvToolFinished, proto.ToolFinished{ID: "c1", Outcome: proto.OutcomeOK, Result: quoted + live.ForCall("c1", "bash", bash("cat knowledge.go"))})
	add(proto.EvToolCall, proto.ToolCall{ID: "c2", Name: "read", Input: editOf("other.go")})
	add(proto.EvToolFinished, proto.ToolFinished{ID: "c2", Outcome: proto.OutcomeOK, Result: quoted + live.ForCall("c2", "read", editOf("other.go"))})
	rb := NewBudget(16384)
	re := NewKnowledge(root, pages, 16384, rb)
	if err := re.Replay(evs); err != nil {
		t.Fatal(err)
	}
	if rb.Used != b.Used || !slices.Equal(re.state, live.state) {
		t.Errorf("budget %d, live %d; states %v, live %v", rb.Used, b.Used, re.state, live.state)
	}
}
