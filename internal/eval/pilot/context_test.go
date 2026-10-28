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
		out, err := cmd.CombinedOutput()
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
}
