package gate

import (
	"bytes"
	"strings"
	"testing"
)

func TestReconcile(t *testing.T) {
	cases := []struct {
		name  string
		cells []Outcome
		want  Outcome
		exit  int
	}{
		{"all ok", []Outcome{OK, OK}, OK, 0},
		{"na excluded", []Outcome{OK, NA}, OK, 0},
		{"fail beats inconclusive", []Outcome{OK, Inconclusive, Fail}, Fail, 1},
		{"inconclusive is never a pass", []Outcome{OK, Inconclusive}, Inconclusive, 2},
		{"only n/a judged nothing", []Outcome{NA, NA}, Inconclusive, 2},
		{"empty judged nothing", nil, Inconclusive, 2},
	}
	for _, tc := range cases {
		var cells []Cell
		for _, o := range tc.cells {
			cells = append(cells, Cell{Name: "c", Outcome: o})
		}
		r := Reconcile(cells)
		if r.Outcome != tc.want || r.Exit != tc.exit {
			t.Errorf("%s: got %s exit %d, want %s exit %d", tc.name, r.Outcome, r.Exit, tc.want, tc.exit)
		}
	}
}

func TestPrintEndsWithOneVerdictLine(t *testing.T) {
	var buf bytes.Buffer
	Print(&buf, "demo", []Cell{
		{Name: "a", Outcome: OK},
		{Name: "b", Outcome: Fail, Detail: "boom\nbang\n"},
	})
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	last := lines[len(lines)-1]
	if !strings.HasPrefix(last, "VERDICT: FAIL — demo:") {
		t.Fatalf("last line = %q", last)
	}
	if n := strings.Count(buf.String(), "VERDICT:"); n != 1 {
		t.Fatalf("%d VERDICT lines, want 1", n)
	}
	if !strings.Contains(buf.String(), "    bang") {
		t.Fatalf("failing cell's detail not printed:\n%s", buf.String())
	}
}

func TestVerdictWordForOK(t *testing.T) {
	if got := Verdict(OK, "x"); got != "VERDICT: PASS — x" {
		t.Fatalf("got %q", got)
	}
}
