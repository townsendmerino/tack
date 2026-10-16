package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestQuestionMatchesTheExitTest: the recording must ask exactly what cmd/tack's exit test replays, or the strict
// replay fails on the user message.
func TestQuestionMatchesTheExitTest(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "cmd", "tack", "run_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `const kenQuestion = "`+question+`"`) {
		t.Fatalf("cmd/tack/run_test.go's kenQuestion is not %q", question)
	}
}
