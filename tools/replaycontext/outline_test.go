package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/townsendmerino/tack/internal/eval/pilot"
)

// §13.2's gate 0 counts a run only for a whole-file read (no offset or limit) that succeeded on a gold file longer than
// the cut whose gold span starts past it.
func TestOutlineHit(t *testing.T) {
	base := map[string]string{"pull/pull.go": strings.Repeat("x\n", 450), "short.go": strings.Repeat("x\n", 120)}
	write := func(input, outcome string) string {
		p := filepath.Join(t.TempDir(), "events.jsonl")
		os.WriteFile(p, []byte(`{"type":"tool.call","payload":{"id":"c1","name":"read","input":`+input+`}}
{"type":"tool.finished","payload":{"id":"c1","outcome":"`+outcome+`","result":"..."}}
`), 0o644)
		return p
	}
	late := []pilot.LineSpan{{File: "pull/pull.go", Start: 369, End: 410}}
	early := []pilot.LineSpan{{File: "pull/pull.go", Start: 50, End: 90}}
	for name, c := range map[string]struct {
		events string
		gold   []pilot.LineSpan
		want   bool
	}{
		"whole-file read, gold past the cut": {write(`{"path":"pull/pull.go"}`, "ok"), late, true},
		"a ./ path is the same file":         {write(`{"path":"./pull/pull.go"}`, "ok"), late, true},
		"a range read":                       {write(`{"path":"pull/pull.go","offset":360,"limit":60}`, "ok"), late, false},
		"gold before the cut":                {write(`{"path":"pull/pull.go"}`, "ok"), early, false},
		"a failed read":                      {write(`{"path":"pull/pull.go"}`, "error"), late, false},
		"a file under the cut":               {write(`{"path":"short.go"}`, "ok"), []pilot.LineSpan{{File: "short.go", Start: 201, End: 210}}, false},
	} {
		got, err := outlineHit(c.events, base, c.gold)
		if err != nil || got != c.want {
			t.Errorf("%s: %v, %v; want %v", name, got, err, c.want)
		}
	}
}
