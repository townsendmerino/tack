package main

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/townsendmerino/tack/internal/kenrun"
	"github.com/townsendmerino/tack/internal/server/cassette"
)

// realCassette is a real model's recording of the exit question, made by tools/recordrun and copied here once an
// attempt passes kenrun.CheckModel.
var realCassette = filepath.Join("testdata", "run-ken-7b.json")

// replayKen replays a cassette of the exit question over testdata/ken, as the exit tests do.
func replayKen(t *testing.T, cass string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := dispatch([]string{"run", "--config", writeConfig(t, "http://cassette.invalid"), "--workspace",
		filepath.Join("testdata", "ken"), "--replay", cass, kenrun.Question}, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// TestRunAnswersFromARealModelCassette is P0's exit check on a real model's recording. The answer must state
// applyTokenBudget's facts and make no claim that it is missing, after a successful tool call (kenrun.CheckModel), not
// repeat the scripted model's calls. The replay stays strict: a one-character change to the recorded system prompt
// fails it.
func TestRunAnswersFromARealModelCassette(t *testing.T) {
	if _, err := os.Stat(realCassette); errors.Is(err, fs.ErrNotExist) {
		t.Skip("no real-model cassette adopted yet: tools/recordrun records one, and an attempt that passes is copied to " + realCassette)
	}
	code, stdout, stderr := replayKen(t, realCassette)
	if code != exitOK {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if why := kenrun.CheckModel(stdout, stderr); why != nil {
		t.Fatalf("%s\nstdout:\n%s\nstderr:\n%s", strings.Join(why, "; "), stdout, stderr)
	}
	assertSystemPromptStrict(t, realCassette)
}

// TestReplayRejectsAChangedSystemPrompt shows the strictness the real-model test relies on, on the scripted cassette,
// which is always present.
func TestReplayRejectsAChangedSystemPrompt(t *testing.T) {
	assertSystemPromptStrict(t, filepath.Join("testdata", "run-ken.json"))
}

// assertSystemPromptStrict changes one character of the system prompt recorded in cass and requires the replay to
// fail: tack's request no longer matches the recording.
func assertSystemPromptStrict(t *testing.T, cass string) {
	t.Helper()
	c, err := cassette.Load(cass)
	if err != nil {
		t.Fatal(err)
	}
	changed := false
	for i, it := range c.Interactions {
		if b := it.Request.Body; bytes.Contains(b, []byte("You are tack,")) {
			c.Interactions[i].Request.Body = bytes.Replace(b, []byte("You are tack,"), []byte("You are tacK,"), 1)
			changed = true
			break
		}
	}
	if !changed {
		t.Fatalf("%s records no request carrying the system prompt", cass)
	}
	p := filepath.Join(t.TempDir(), "changed.json")
	if err := c.Save(p); err != nil {
		t.Fatal(err)
	}
	if code, stdout, stderr := replayKen(t, p); code == exitOK {
		t.Fatalf("a one-character change to the recorded system prompt still replayed\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
}
