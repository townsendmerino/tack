package pilot

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/proto"
)

func writeTackEvents(t *testing.T, path string, evs []any) {
	t.Helper()
	var b strings.Builder
	ts := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	for i, p := range evs {
		var typ proto.EventType
		for k, spec := range proto.EventTypes {
			if spec.Payload == reflect.TypeOf(p) {
				typ = k
				break
			}
		}
		ev, err := proto.NewEvent(int64(i+1), ts.Add(time.Duration(i)*time.Second), typ, 0, p)
		if err != nil {
			t.Fatalf("NewEvent(%d): %v", i, err)
		}
		line, err := json.Marshal(ev)
		if err != nil {
			t.Fatalf("Marshal(%d): %v", i, err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func approxEq(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

func TestScoreContext_RedAndExact(t *testing.T) {
	var lines []string
	for i := 1; i <= 20; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	base := map[string]string{
		"pkg/foo.go": strings.Join(lines, "\n") + "\n",
		"pkg/bar.go": "bar 1\nbar 2\nbar 3\n",
	}
	gold := []LineSpan{{File: "pkg/foo.go", Start: 10, End: 14}}

	redPath := filepath.Join(t.TempDir(), "events.jsonl")
	writeTackEvents(t, redPath, []any{
		proto.RequestSent{Request: 1},
		proto.ToolCall{ID: "c1", Name: "read", Input: json.RawMessage(`{"path":"pkg/bar.go","offset":1,"limit":2}`)},
		proto.ToolFinished{ID: "c1", Outcome: proto.OutcomeOK, Result: "     1\tbar 1\n     2\tbar 2\n"},
	})
	red, err := ScoreContext(redPath, base, gold, false)
	if err != nil {
		t.Fatalf("ScoreContext(red): %v", err)
	}
	if red.FinalCov != 0 || red.FileCov != 0 || red.Precision != 0 || red.FirstGoldReq != 0 || red.AUCCov != 0 {
		t.Fatalf("red metrics = %+v, want all 0", red)
	}

	exactPath := filepath.Join(t.TempDir(), "events.jsonl")
	writeTackEvents(t, exactPath, []any{
		proto.RequestSent{Request: 1},
		proto.ToolCall{ID: "c1", Name: "read", Input: json.RawMessage(`{"path":"pkg/foo.go","offset":10,"limit":5}`)},
		proto.ToolFinished{ID: "c1", Outcome: proto.OutcomeOK, Result: "    10\tline 10\n    11\tline 11\n    12\tline 12\n    13\tline 13\n    14\tline 14\n"},
	})
	exact, err := ScoreContext(exactPath, base, gold, false)
	if err != nil {
		t.Fatalf("ScoreContext(exact): %v", err)
	}
	if exact.GoldLines != 5 || exact.SeenLines != 5 || !approxEq(exact.FinalCov, 1) || !approxEq(exact.FileCov, 1) ||
		!approxEq(exact.CovReq1, 1) || !approxEq(exact.Cov4k, 1) || !approxEq(exact.Precision, 1) ||
		exact.FirstGoldReq != 1 || !approxEq(exact.AUCCov, 1) {
		t.Fatalf("exact metrics = %+v, want GoldLines=5 SeenLines=5 and 1 across coverage/precision/first_gold_req/auc", exact)
	}
}

func TestScoreContext_AllToolsAndMetrics(t *testing.T) {
	var aLines, bLines, cLines []string
	for i := 1; i <= 10; i++ {
		aLines = append(aLines, fmt.Sprintf("a%d", i))
	}
	for i := 1; i <= 15; i++ {
		bLines = append(bLines, fmt.Sprintf("b%d", i))
	}
	for i := 1; i <= 250; i++ {
		cLines = append(cLines, fmt.Sprintf("c%d", i))
	}
	base := map[string]string{
		"pkg/a.go": strings.Join(aLines, "\n") + "\n",
		"pkg/b.go": strings.Join(bLines, "\n") + "\n",
		"pkg/c.go": strings.Join(cLines, "\n") + "\n",
	}
	gold := []LineSpan{
		{File: "pkg/a.go", Start: 4, End: 7},
		{File: "pkg/b.go", Start: 10, End: 13},
	}

	evPath := filepath.Join(t.TempDir(), "events.jsonl")
	writeTackEvents(t, evPath, []any{
		// Request 1: search sees pkg/a.go:4-5; symbol outline (name=="") adds 0 code lines.
		proto.RequestSent{Request: 1},
		proto.ToolCall{ID: "c1", Name: "search", Input: json.RawMessage(`{"query":"a4"}`)},
		proto.ToolFinished{ID: "c1", Outcome: proto.OutcomeOK, Result: "pkg/a.go:4-5\na4\na5\n\n"},
		proto.ToolCall{ID: "c2", Name: "symbol", Input: json.RawMessage(`{"path":"pkg/a.go"}`)},
		proto.ToolFinished{ID: "c2", Outcome: proto.OutcomeOK, Result: "pkg/a.go:1-10 func A\n"},

		// Request 2: symbol definition + callers sees pkg/a.go:4-6 and pkg/b.go:10; grep sees pkg/b.go:11.
		proto.RequestSent{Request: 2},
		proto.ToolCall{ID: "c3", Name: "symbol", Input: json.RawMessage(`{"name":"A","with":"callers"}`)},
		proto.ToolFinished{
			ID:      "c3",
			Outcome: proto.OutcomeOK,
			Result:  "pkg/a.go:4-7 func A\n     4\ta4\n     5\ta5\n     6\ta6\ncallers (1):\n  pkg/b.go:10 in B: A()\n",
		},
		proto.ToolCall{ID: "c4", Name: "grep", Input: json.RawMessage(`{"pattern":"b11"}`)},
		proto.ToolFinished{ID: "c4", Outcome: proto.OutcomeOK, Result: "pkg/b.go:11:b11\n"},

		// Request 3: edit pkg/b.go at line 2 replacing 1 line with 3 lines (shifts subsequent lines by +2);
		// then read pkg/b.go at current lines 14..15 (which map back to base lines 12..13);
		// bash "go test ./..." adds 0 spans; bash "sed -n '7,7p' pkg/a.go" sees pkg/a.go:7;
		// edit pkg/a.go at lines 4..5 uses 2 gold lines.
		proto.RequestSent{Request: 3},
		proto.ToolCall{ID: "c5", Name: "edit", Input: json.RawMessage(`{"path":"pkg/b.go","old_string":"b2\n","new_string":"b2x\nb2y\nb2z\n"}`)},
		proto.ToolFinished{ID: "c5", Outcome: proto.OutcomeOK, Result: "edited pkg/b.go at line 2"},
		proto.ToolCall{ID: "c6", Name: "read", Input: json.RawMessage(`{"path":"pkg/b.go","offset":14,"limit":2}`)},
		proto.ToolFinished{ID: "c6", Outcome: proto.OutcomeOK, Result: "    14\tb12\n    15\tb13\n"},
		proto.ToolCall{ID: "c7", Name: "bash", Input: json.RawMessage(`{"command":"go test ./..."}`)},
		proto.ToolFinished{ID: "c7", Outcome: proto.OutcomeOK, Result: "ok  \texample/pkg\t0.002s\n"},
		proto.ToolCall{ID: "c8", Name: "bash", Input: json.RawMessage(`{"command":"sed -n '7,7p' pkg/a.go"}`)},
		proto.ToolFinished{ID: "c8", Outcome: proto.OutcomeOK, Result: "a7\n"},
		proto.ToolCall{ID: "c9", Name: "edit", Input: json.RawMessage(`{"path":"pkg/a.go","old_string":"a4\na5\n","new_string":"a45\n"}`)},
		proto.ToolFinished{ID: "c9", Outcome: proto.OutcomeOK, Result: "edited pkg/a.go at line 4"},
	})

	m, err := ScoreContext(evPath, base, gold, false)
	if err != nil {
		t.Fatalf("ScoreContext: %v", err)
	}
	if m.Requests != 3 || m.GoldLines != 8 || m.GoldFiles != 2 || m.SeenLines != 9 || m.UsedLines != 3 {
		t.Fatalf("counts = %+v, want req=3 gold=8/2 seen=9 used=3", m)
	}
	if !approxEq(m.CovReq1, 2.0/8.0) {
		t.Errorf("CovReq1 = %v, want %v", m.CovReq1, 2.0/8.0)
	}
	if !approxEq(m.FinalCov, 1.0) || !approxEq(m.FileCov, 1.0) || !approxEq(m.Cov4k, 1.0) {
		t.Errorf("coverage = final %v file %v 4k %v, want 1.0", m.FinalCov, m.FileCov, m.Cov4k)
	}
	if !approxEq(m.Precision, 8.0/9.0) {
		t.Errorf("Precision = %v, want %v", m.Precision, 8.0/9.0)
	}
	if m.FirstGoldReq != 1 {
		t.Errorf("FirstGoldReq = %d, want 1", m.FirstGoldReq)
	}
	wantAUC := (2.0/8.0 + 5.0/8.0 + 1.0) / 3.0
	if !approxEq(m.AUCCov, wantAUC) {
		t.Errorf("AUCCov = %v, want %v", m.AUCCov, wantAUC)
	}
	if !approxEq(m.Utilization, 2.0/8.0) {
		t.Errorf("Utilization = %v, want %v", m.Utilization, 2.0/8.0)
	}
	if !approxEq(m.EditLoc, 2.0/3.0) {
		t.Errorf("EditLoc = %v, want %v", m.EditLoc, 2.0/3.0)
	}

	// Verify read's default 200-line cap when offset==0 && limit==0 on a 250-line file.
	capPath := filepath.Join(t.TempDir(), "cap.jsonl")
	writeTackEvents(t, capPath, []any{
		proto.RequestSent{Request: 1},
		proto.ToolCall{ID: "r1", Name: "read", Input: json.RawMessage(`{"path":"pkg/c.go"}`)},
		proto.ToolFinished{ID: "r1", Outcome: proto.OutcomeOK, Result: "     1\tc1\n"},
	})
	capM, err := ScoreContext(capPath, base, []LineSpan{{File: "pkg/c.go", Start: 201, End: 250}}, false)
	if err != nil {
		t.Fatalf("ScoreContext(cap): %v", err)
	}
	if capM.SeenLines != 200 || capM.FinalCov != 0 {
		t.Fatalf("200-line cap metrics = %+v, want SeenLines=200 FinalCov=0", capM)
	}
}

func TestFixGold_WidensToEnclosingSymbol(t *testing.T) {
	tmp := t.TempDir()
	work := filepath.Join(tmp, "work")
	mirror := filepath.Join(tmp, "demo.git")

	runGit := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}

	if err := os.MkdirAll(filepath.Join(work, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(work, "init", "-q")

	baseGo := strings.Join([]string{
		"package pkg",
		"",
		"var defaultStep = 1",
		"",
		"func AddStep(x int) int {",
		"\tstep := defaultStep",
		"\tif step < 0 {",
		"\t\tstep = 0",
		"\t}",
		"\treturn x + step",
		"}",
		"",
	}, "\n")
	baseTest := "package pkg\n\nimport \"testing\"\n\nfunc TestAddStep(t *testing.T) {}\n"
	if err := os.WriteFile(filepath.Join(work, "pkg", "calc.go"), []byte(baseGo), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "pkg", "calc_test.go"), []byte(baseTest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "CHANGELOG.md"), []byte("# Changelog\n- v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(work, "add", ".")
	runGit(work, "commit", "-q", "-m", "base")
	baseSHA := runGit(work, "rev-parse", "HEAD")

	fixGo := strings.Replace(baseGo, "var defaultStep = 1", "var defaultStep = 2", 1)
	fixGo = strings.Replace(fixGo, "\tif step < 0 {", "\tif step <= 0 {", 1)
	fixTest := baseTest + "// test updated\n"
	if err := os.WriteFile(filepath.Join(work, "pkg", "calc.go"), []byte(fixGo), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "pkg", "calc_test.go"), []byte(fixTest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "CHANGELOG.md"), []byte("# Changelog\n- v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(work, "add", ".")
	runGit(work, "commit", "-q", "-m", "fix")
	fixSHA := runGit(work, "rev-parse", "HEAD")

	if err := os.MkdirAll(mirror, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(tmp, "clone", "--bare", "-q", work, mirror)

	raw, widened, err := FixGold(context.Background(), tmp, "demo", baseSHA, fixSHA, nil)
	if err != nil {
		t.Fatalf("FixGold: %v", err)
	}
	wantRaw := []LineSpan{
		{File: "pkg/calc.go", Start: 3, End: 3},
		{File: "pkg/calc.go", Start: 7, End: 7},
	}
	if !reflect.DeepEqual(raw, wantRaw) {
		t.Fatalf("raw = %+v, want %+v", raw, wantRaw)
	}
	wantWidened := []LineSpan{
		{File: "pkg/calc.go", Start: 3, End: 3},
		{File: "pkg/calc.go", Start: 5, End: 11},
	}
	if !reflect.DeepEqual(widened, wantWidened) {
		t.Fatalf("widened = %+v, want %+v", widened, wantWidened)
	}

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := FixGold(canceledCtx, tmp, "demo", baseSHA, fixSHA, nil); err == nil {
		t.Fatal("FixGold with canceled context succeeded, want error from SymbolAt")
	}
}

// A whole-file write over a base file adds no seen lines, uses only the base lines it deletes or changes, and keeps
// the base line of every line it leaves unchanged, so later reads are credited by content, not by position.
func TestScoreContext_WriteAlignsByContent(t *testing.T) {
	base := map[string]string{"pkg/w.go": "l1\nl2\nl3\nl4\nl5\n"}
	gold := []LineSpan{{File: "pkg/w.go", Start: 1, End: 2}}
	path := filepath.Join(t.TempDir(), "write.jsonl")
	writeTackEvents(t, path, []any{
		proto.RequestSent{Request: 1},
		// Inserts new0 at the top and changes l5: base line 5 is the only one used.
		proto.ToolCall{ID: "w", Name: "write", Input: json.RawMessage(`{"path":"pkg/w.go","content":"new0\nl1\nl2\nl3\nl4\nl5x\n"}`)},
		proto.ToolFinished{ID: "w", Outcome: proto.OutcomeOK, Result: "wrote pkg/w.go"},
		// Current line 1 is the inserted new0, which has no base line.
		proto.ToolCall{ID: "r1", Name: "read", Input: json.RawMessage(`{"path":"pkg/w.go","offset":1,"limit":1}`)},
		proto.ToolFinished{ID: "r1", Outcome: proto.OutcomeOK, Result: "     1\tnew0\n"},
		proto.RequestSent{Request: 2},
		// Current lines 2-3 are base lines 1-2, the gold.
		proto.ToolCall{ID: "r2", Name: "read", Input: json.RawMessage(`{"path":"pkg/w.go","offset":2,"limit":2}`)},
		proto.ToolFinished{ID: "r2", Outcome: proto.OutcomeOK, Result: "     2\tl1\n     3\tl2\n"},
	})
	m, err := ScoreContext(path, base, gold, false)
	if err != nil {
		t.Fatalf("ScoreContext: %v", err)
	}
	if m.CovReq1 != 0 || m.FinalCov != 1 || m.FirstGoldReq != 2 || m.SeenLines != 2 || m.UsedLines != 1 || m.EditLoc != 0 {
		t.Fatalf("metrics = %+v, want CovReq1=0 FinalCov=1 FirstGoldReq=2 SeenLines=2 UsedLines=1 EditLoc=0", m)
	}
}

func TestScoreContext_Cov4kFailedOutcomesWriteAndEdits(t *testing.T) {
	base := map[string]string{
		"pkg/a.go": "a1\na2\na3\na4\n",
		"pkg/b.go": "b1\nb2\nb3\nb4\n",
		"pkg/c.go": "c1\ndup\nc3\nc4\nc5\ndup\nc7\n",
	}
	gold := []LineSpan{
		{File: "pkg/a.go", Start: 1, End: 4},
		{File: "pkg/b.go", Start: 2, End: 3},
		{File: "pkg/c.go", Start: 2, End: 2},
		{File: "pkg/c.go", Start: 6, End: 6},
	}

	// 1. Cov4k budget binding + failed tool outcome + -include-notes on failed edit.
	cov4kPath := filepath.Join(t.TempDir(), "cov4k.jsonl")
	pad2000 := strings.Repeat("x", 8000)  // 2000 tokens
	pad2500 := strings.Repeat("y", 10000) // 2500 tokens
	writeTackEvents(t, cov4kPath, []any{
		proto.RequestSent{Request: 1},
		// Call 1 (OK): 2000 tokens <= 4096 -> included in Cov4k (sees pkg/a.go:1-2).
		proto.ToolCall{ID: "c1", Name: "read", Input: json.RawMessage(`{"path":"pkg/a.go","offset":1,"limit":2}`)},
		proto.ToolFinished{ID: "c1", Outcome: proto.OutcomeOK, Result: pad2000},

		proto.RequestSent{Request: 2},
		// Call 2 (Error without note): must add 0 spans and 0 tokens (so cumTokens stays 2000).
		proto.ToolCall{ID: "c2", Name: "read", Input: json.RawMessage(`{"path":"pkg/a.go","offset":3,"limit":2}`)},
		proto.ToolFinished{ID: "c2", Outcome: proto.OutcomeError, Result: pad2500},
		// Call 3 (Error with [tack: ...] note): ignored when includeNotes==false, counted when includeNotes==true.
		proto.ToolCall{ID: "c3", Name: "edit", Input: json.RawMessage(`{"path":"pkg/a.go","old_string":"missing","new_string":"x"}`)},
		proto.ToolFinished{
			ID:      "c3",
			Outcome: proto.OutcomeError,
			Result:  "edit: old_string not found\n\n[tack: the current text of Foo in pkg/a.go, lines 3-3, is:]\n     3\ta3\n",
		},
		// Call 4 (OK): adds 2500 tokens -> cumTokens crosses 4096, so pkg/a.go:4-4 is in FinalCov but not Cov4k.
		proto.ToolCall{ID: "c4", Name: "read", Input: json.RawMessage(`{"path":"pkg/a.go","offset":4,"limit":1}`)},
		proto.ToolFinished{ID: "c4", Outcome: proto.OutcomeOK, Result: pad2500},
	})

	aGold := []LineSpan{{File: "pkg/a.go", Start: 1, End: 4}}
	mNoNotes, err := ScoreContext(cov4kPath, base, aGold, false)
	if err != nil {
		t.Fatalf("ScoreContext(noNotes): %v", err)
	}
	if !approxEq(mNoNotes.Cov4k, 2.0/4.0) || !approxEq(mNoNotes.FinalCov, 3.0/4.0) {
		t.Fatalf("noNotes Cov4k=%v FinalCov=%v, want Cov4k=0.5 FinalCov=0.75", mNoNotes.Cov4k, mNoNotes.FinalCov)
	}
	if mNoNotes.ToolOutputTokens != 4500 {
		t.Fatalf("noNotes ToolOutputTokens=%d, want 4500 (failed calls excluded)", mNoNotes.ToolOutputTokens)
	}

	mNotes, err := ScoreContext(cov4kPath, base, aGold, true)
	if err != nil {
		t.Fatalf("ScoreContext(withNotes): %v", err)
	}
	if !approxEq(mNotes.Cov4k, 3.0/4.0) || !approxEq(mNotes.FinalCov, 1.0) {
		t.Fatalf("withNotes Cov4k=%v FinalCov=%v, want Cov4k=0.75 FinalCov=1.0", mNotes.Cov4k, mNotes.FinalCov)
	}
	if mNotes.ToolOutputTokens <= mNoNotes.ToolOutputTokens {
		t.Fatalf("withNotes ToolOutputTokens=%d, want > %d", mNotes.ToolOutputTokens, mNoNotes.ToolOutputTokens)
	}

	// 2. write (new file + existing base file), replace_all, edit by lines/symbol, bash cat/head/tail, and empty gold.
	editPath := filepath.Join(t.TempDir(), "edits.jsonl")
	writeTackEvents(t, editPath, []any{
		proto.RequestSent{Request: 1},
		// write to a brand-new file: adds 0 base spans.
		proto.ToolCall{ID: "w1", Name: "write", Input: json.RawMessage(`{"path":"pkg/new.go","content":"package pkg\n"}`)},
		proto.ToolFinished{ID: "w1", Outcome: proto.OutcomeOK, Result: "wrote pkg/new.go"},
		// write to existing base file pkg/b.go (4 base lines -> 2 new lines, none kept): uses pkg/b.go:1-4, sees nothing.
		proto.ToolCall{ID: "w2", Name: "write", Input: json.RawMessage(`{"path":"pkg/b.go","content":"b1_new\nb2_new\n"}`)},
		proto.ToolFinished{ID: "w2", Outcome: proto.OutcomeOK, Result: "wrote pkg/b.go"},
		// a later read of pkg/b.go shows only the new lines, which have no base lines.
		proto.ToolCall{ID: "r1", Name: "read", Input: json.RawMessage(`{"path":"pkg/b.go","offset":1,"limit":2}`)},
		proto.ToolFinished{ID: "r1", Outcome: proto.OutcomeOK, Result: "     1\tb1_new\n     2\tb2_new\n"},
		// edit with replace_all:true on pkg/c.go replaces both "dup\n" occurrences at lines 2 and 6.
		proto.ToolCall{ID: "e1", Name: "edit", Input: json.RawMessage(`{"path":"pkg/c.go","old_string":"dup\n","new_string":"fixed\n","replace_all":true}`)},
		proto.ToolFinished{ID: "e1", Outcome: proto.OutcomeOK, Result: "edited pkg/c.go (2 replacements)"},
		// edit by start_line..end_line on pkg/c.go at lines 3..3.
		proto.ToolCall{ID: "e2", Name: "edit", Input: json.RawMessage(`{"path":"pkg/c.go","start_line":3,"end_line":3,"new_string":"c3_new\n"}`)},
		proto.ToolFinished{ID: "e2", Outcome: proto.OutcomeOK, Result: "edited pkg/c.go at line 3"},
		// edit by symbol on pkg/c.go reported via "edited ... at lines 4-5".
		proto.ToolCall{ID: "e3", Name: "edit", Input: json.RawMessage(`{"path":"pkg/c.go","symbol":"Foo","new_string":"c4_new\nc5_new\n"}`)},
		proto.ToolFinished{ID: "e3", Outcome: proto.OutcomeOK, Result: "edited pkg/c.go at lines 4-5"},
		// bash cat/head/tail on pkg/a.go.
		proto.ToolCall{ID: "b1", Name: "bash", Input: json.RawMessage(`{"command":"head -n 2 pkg/a.go && tail -n 1 pkg/a.go && cat pkg/a.go"}`)},
		proto.ToolFinished{ID: "b1", Outcome: proto.OutcomeOK, Result: "a1\na2\na4\na1\na2\na3\na4\n"},
	})

	em, err := ScoreContext(editPath, base, gold, false)
	if err != nil {
		t.Fatalf("ScoreContext(edits): %v", err)
	}
	// Gold has 4 (pkg/a.go:1-4) + 2 (pkg/b.go:2-3) + 2 (pkg/c.go:2,6) = 8 gold lines; 6 are seen (pkg/b.go's gold only
	// through its write, which shows nothing).
	// Used lines: pkg/b.go:1-4 (4 lines, 2 in gold) + pkg/c.go:2,3,4,5,6 (5 lines, 2 in gold) = 9 used lines, 4 in gold.
	// Utilization counts the 2 used gold lines that were also seen (pkg/c.go:2,6) among the 6 seen.
	if em.GoldLines != 8 || !approxEq(em.FinalCov, 6.0/8.0) || em.UsedLines != 9 {
		t.Fatalf("edits metrics = %+v, want GoldLines=8 FinalCov=0.75 UsedLines=9", em)
	}
	if !approxEq(em.Utilization, 2.0/6.0) || !approxEq(em.EditLoc, 4.0/9.0) {
		t.Fatalf("edits Utilization=%v EditLoc=%v, want %v and %v", em.Utilization, em.EditLoc, 2.0/6.0, 4.0/9.0)
	}

	// Empty gold returns vacuous coverage 1.0 and Precision 0 without dividing by zero.
	emptyM, err := ScoreContext(editPath, base, nil, false)
	if err != nil {
		t.Fatalf("ScoreContext(emptyGold): %v", err)
	}
	if emptyM.GoldLines != 0 || emptyM.FinalCov != 1 || emptyM.Precision != 0 || emptyM.SeenLines == 0 {
		t.Fatalf("emptyGold metrics = %+v, want GoldLines=0 FinalCov=1 Precision=0 SeenLines>0", emptyM)
	}
}
