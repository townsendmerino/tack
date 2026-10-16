package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/townsendmerino/tack/internal/eval/mine"
)

// TestMineChooseNeedsTheStubCheck: choose reads each candidate's latest record, refuses one that passed stage 2
// without the stub check, and leaves out one the stub check rejected.
func TestMineChooseNeedsTheStubCheck(t *testing.T) {
	out := t.TempDir()
	rec := func(commit string, ok bool, stub *mine.Stub) mine.Candidate {
		return mine.Candidate{Repo: "r", Commit: commit, Verified: &mine.Verified{OK: ok, Stub: stub}}
	}
	write := func(cs ...mine.Candidate) {
		var b strings.Builder
		for _, c := range cs {
			j, _ := json.Marshal(c)
			b.Write(append(j, '\n'))
		}
		os.WriteFile(filepath.Join(out, "verified.jsonl"), []byte(b.String()), 0o644)
	}
	a, b := strings.Repeat("a", 40), strings.Repeat("b", 40)
	write(rec(a, true, nil), rec(b, true, &mine.Stub{Funcs: []string{"f"}}))
	if err := mineChoose(out, io.Discard); err == nil || !strings.Contains(err.Error(), "without the stub check") {
		t.Fatalf("an unchecked record was chosen from: %v", err)
	}
	// a's later record carries its stub check, which rejected it.
	write(rec(a, true, nil), rec(b, true, &mine.Stub{Funcs: []string{"f"}}), rec(a, false, &mine.Stub{Funcs: []string{"f"}, Rejected: mine.RejectPassed}))
	if err := mineChoose(out, io.Discard); err != nil {
		t.Fatal(err)
	}
	pilot, _ := readCandidates(filepath.Join(out, "pilot.jsonl"))
	if len(pilot) != 1 || pilot[0].Commit != b {
		t.Fatalf("pilot %v, want only %s", pilot, b[:8])
	}
}
