package eval

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestSmokeGate(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, lines ...string) string {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
		return p
	}
	const arm = "qwen2.5-coder-7b-q4km-cuda"
	sum := func(a, b, malformed int) string {
		return `{"box":"nobara","arm":"` + arm + `","phase":"smoke_summary","pass":[` + strconv.Itoa(a) + `,` + strconv.Itoa(b) + `],"malformed":` + strconv.Itoa(malformed) + `}`
	}
	other := `{"arm":"seed-coder-8b-q4km-cuda","phase":"smoke_summary","pass":[20,20],"malformed":0}`
	for _, tc := range []struct {
		name, path, want string // want "" means the gate passes
	}{
		{"17 on both runs", write("a", other, sum(17, 18, 0)), ""},
		{"20 on both runs", write("b", sum(20, 20, 0)), ""},
		{"16 on one run", write("c", sum(19, 16, 0)), "did not meet"},
		{"one malformed", write("d", sum(20, 20, 1)), "did not meet"},
		{"only another arm passed", write("e", other), "no smoke summary for arm"},
		{"the last summary counts", write("f", sum(20, 20, 0), sum(15, 20, 0)), "did not meet"},
		{"no results file", filepath.Join(dir, "missing"), "no E0 results"},
	} {
		score, err := SmokeGate(tc.path, arm)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: refused: %v", tc.name, err)
		case tc.want == "" && !strings.HasPrefix(score, "smoke "):
			t.Errorf("%s: score %q", tc.name, score)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: err %v, want one containing %q", tc.name, err, tc.want)
		}
	}
}
