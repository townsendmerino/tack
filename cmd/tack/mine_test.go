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
	if err := mineChoose(out, "", io.Discard); err == nil || !strings.Contains(err.Error(), "without the stub check") {
		t.Fatalf("an unchecked record was chosen from: %v", err)
	}
	// a's later record carries its stub check, which rejected it.
	write(rec(a, true, nil), rec(b, true, &mine.Stub{Funcs: []string{"f"}}), rec(a, false, &mine.Stub{Funcs: []string{"f"}, Rejected: mine.RejectPassed}))
	if err := mineChoose(out, "", io.Discard); err != nil {
		t.Fatal(err)
	}
	pilot, _ := readCandidates(filepath.Join(out, "pilot.jsonl"))
	if len(pilot) != 1 || pilot[0].Commit != b {
		t.Fatalf("pilot %v, want only %s", pilot, b[:8])
	}
}

// TestMineChooseHonoursTheCommittedPilot: with -exclude, every committed pilot task, removed or not, is left out of
// E1's uncut order, and a chosen pilot that differs from the committed one writes nothing.
func TestMineChooseHonoursTheCommittedPilot(t *testing.T) {
	out := t.TempDir()
	writeJSONL := func(p string, cs ...mine.Candidate) {
		var b strings.Builder
		for _, c := range cs {
			j, _ := json.Marshal(c)
			b.Write(append(j, '\n'))
		}
		os.WriteFile(p, []byte(b.String()), 0o644)
	}
	ok := func(commit string) mine.Candidate {
		return mine.Candidate{Repo: "r", Commit: commit, Verified: &mine.Verified{OK: true, Stub: &mine.Stub{Funcs: []string{"f"}}}}
	}
	var verified []mine.Candidate
	for i := 0; i < 14; i++ {
		verified = append(verified, ok(strings.Repeat(string(rune('a'+i)), 40)))
	}
	writeJSONL(filepath.Join(out, "verified.jsonl"), verified...)
	// The committed pilot is what this data chooses (4 per repo, one repo), plus one removed task.
	chosen, _ := mine.Choose(verified, 10, 4, nil)
	removed := ok(strings.Repeat("z", 40))
	removed.Removed = "stub check"
	committed := filepath.Join(t.TempDir(), "pilot.jsonl")
	writeJSONL(committed, append(append([]mine.Candidate(nil), chosen...), removed)...)
	if err := mineChoose(out, committed, io.Discard); err != nil {
		t.Fatal(err)
	}
	e1, _ := readCandidates(filepath.Join(out, "e1.jsonl"))
	if len(e1) != len(verified)-len(chosen) {
		t.Fatalf("e1 order has %d, want all %d not in the pilot, uncut", len(e1), len(verified)-len(chosen))
	}
	// A committed pilot the data no longer chooses refuses.
	os.Remove(filepath.Join(out, "e1.jsonl"))
	writeJSONL(committed, chosen[1:]...)
	if err := mineChoose(out, committed, io.Discard); err == nil || !strings.Contains(err.Error(), "not the committed pilot") {
		t.Fatalf("a different pilot was accepted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "e1.jsonl")); err == nil {
		t.Fatal("e1.jsonl was written after the refusal")
	}
}
