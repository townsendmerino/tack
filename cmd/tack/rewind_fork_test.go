package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/store"
)

// editedSession is a session with one finished turn (events 1 to 3) and a second whose edit of a.txt is checkpointed (events 4
// to 7: user.message, tool.call, checkpoint, tool.finished). It returns the config, the workspace and the checkpoint's seq.
func editedSession(t *testing.T) (cfg, ws string, checkpoint int64) {
	t.Helper()
	cfg = writeConfig(t, "http://cassette.invalid")
	st, err := store.Open(filepath.Join(filepath.Dir(cfg), "state"))
	if err != nil {
		t.Fatal(err)
	}
	ws, _ = filepath.EvalSymlinks(t.TempDir())
	os.WriteFile(filepath.Join(ws, "a.txt"), []byte("v1\n"), 0o644)
	h, _ := st.Blobs.Put([]byte("v0\n"))
	log, err := st.Create("s_edit")
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	say := proto.UserMessage{Content: []proto.ContentBlock{proto.Text("go")}}
	in, _ := json.Marshal(map[string]string{"path": "a.txt"})
	log.Append(proto.EvSessionCreated, 0, proto.SessionCreated{Workspace: ws, Model: "main", Mode: proto.ModeAuto})
	log.Append(proto.EvUserMessage, 0, say)
	log.Append(proto.EvTurnEnd, 0, proto.TurnEnd{StopReason: "end_turn"})
	log.Append(proto.EvUserMessage, 0, say)
	log.Append(proto.EvToolCall, 0, proto.ToolCall{ID: "e1", Name: "edit", Input: in})
	cp, err := log.Append(proto.EvCheckpoint, 0, proto.Checkpoint{ID: "e1", Workspace: ws, Files: []proto.FileImage{{Path: "a.txt", Blob: h}}})
	if err != nil {
		t.Fatal(err)
	}
	log.Append(proto.EvToolFinished, 0, proto.ToolFinished{ID: "e1", Outcome: proto.OutcomeOK, After: store.HandleOf([]byte("v1\n"))})
	return cfg, ws, cp.Seq
}

var forkedAs = regexp.MustCompile(`forked the conversation at seq (\d+) as (s_\w+)`)

// A rewind restores the files and forks the conversation at the last turn boundary before the checkpoint: the child holds the
// finished turn and not the one the edit was in, and the parent gets only the rewind event.
func TestRewindForksTheConversationAtTheTurnBoundary(t *testing.T) {
	cfg, ws, cp := editedSession(t)
	s := strconv.FormatInt(cp, 10)
	code, out, _ := rewind(t, "-config", cfg, "s_edit", s)
	if code != exitOK || strings.Contains(out, "forked") {
		t.Errorf("the preview: %d %q", code, out)
	}
	state := filepath.Join(filepath.Dir(cfg), "state")
	st, _ := store.Open(state)
	if ids, _ := st.Sessions(); len(ids) != 1 {
		t.Fatalf("the preview made a session: %v", ids)
	}
	code, out, errs := rewind(t, "-config", cfg, "-yes", "-no-undo", "s_edit", s)
	if code != exitOK {
		t.Fatalf("rewind: %d %s", code, errs)
	}
	if b, _ := os.ReadFile(filepath.Join(ws, "a.txt")); string(b) != "v0\n" {
		t.Errorf("a.txt: %q", b)
	}
	m := forkedAs.FindStringSubmatch(out)
	if m == nil || m[1] != "3" {
		t.Fatalf("output %q: want a fork at seq 3", out)
	}
	child, err := st.Events(proto.SessionID(m[2]), 0)
	if err != nil || len(child) != 3 || child[2].Type != proto.EvTurnEnd {
		t.Fatalf("child %v, %v", types(child), err)
	}
	v, _ := child[0].Decode()
	if c := v.(proto.SessionCreated); c.ForkOf != "s_edit" || c.ForkAt != 3 || c.Workspace != ws {
		t.Errorf("session.created %+v", c)
	}
	parent, _ := st.Events("s_edit", 0)
	if last := parent[len(parent)-1]; last.Type != proto.EvRewind || len(parent) != 8 {
		t.Errorf("the parent's log: %v", types(parent))
	}
}

func types(evs []proto.Event) []proto.EventType {
	var out []proto.EventType
	for _, e := range evs {
		out = append(out, e.Type)
	}
	return out
}

// A rewind to the first checkpoint of a session with no finished turn forks at 1, the bare start.
func TestRewindOfASessionWithNoFinishedTurnForksTheStart(t *testing.T) {
	cfg, _, _, _ := imageSession(t)
	st, _ := store.Open(filepath.Join(filepath.Dir(cfg), "state"))
	evs, _ := st.Events("s_images", 0)
	var first int64
	for _, p := range rewindPoints(evs) {
		first = p.seq
		break
	}
	code, out, errs := rewind(t, "-config", cfg, "-yes", "-no-undo", "s_images", strconv.FormatInt(first, 10))
	if code != exitOK {
		t.Fatalf("%d %s", code, errs)
	}
	if m := forkedAs.FindStringSubmatch(out); m == nil || m[1] != "1" {
		t.Errorf("output %q", out)
	}
}

// A rewind to a command's snapshot forks too, and a fork that cannot be made leaves the files restored and says so.
func TestRewindToASnapshotForksAndAForkFailureIsReported(t *testing.T) {
	cfg, ws, seq := rewindSession(t)
	code, out, errs := rewind(t, "-config", cfg, "-yes", "s_rewind", strconv.FormatInt(seq, 10))
	if code != exitOK {
		t.Fatalf("%d %s", code, errs)
	}
	if m := forkedAs.FindStringSubmatch(out); m == nil || m[1] != "1" {
		t.Errorf("output %q", out)
	}
	if b, _ := os.ReadFile(filepath.Join(ws, "keep.txt")); string(b) != "work\n" {
		t.Errorf("keep.txt: %q", b)
	}
	// Forking a session whose events are gone from under the rewind: the files are rewound, the error says the fork failed.
	var out2, err2 strings.Builder
	st, _ := store.Open(filepath.Join(filepath.Dir(cfg), "state"))
	if code := forkAfterRewind(st, "s_gone", nil, 5, &out2, &err2); code == exitOK || !strings.Contains(err2.String(), "files are rewound") {
		t.Errorf("a failed fork: %d %q", code, err2.String())
	}
}
