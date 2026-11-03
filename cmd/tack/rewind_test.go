package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/townsendmerino/tack/internal/halt"
	"github.com/townsendmerino/tack/internal/proto"
	snap "github.com/townsendmerino/tack/internal/snapshot"
	"github.com/townsendmerino/tack/internal/store"
)

// rewindSession is a session on the log with one checkpoint of a real git workspace, one command that can't be rewound,
// and the tree changed after the checkpoint. It returns the config, the workspace, and the checkpoint's seq.
func rewindSession(t *testing.T) (cfg, ws string, seq int64) {
	t.Helper()
	cfg = writeConfig(t, "http://cassette.invalid")
	state := filepath.Join(filepath.Dir(cfg), "state")
	ws, _ = filepath.EvalSymlinks(t.TempDir())
	for _, args := range [][]string{{"init", "-q"}} {
		c := exec.Command("git", args...)
		c.Dir = ws
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git: %v %s", err, out)
		}
	}
	os.WriteFile(filepath.Join(ws, "keep.txt"), []byte("work\n"), 0o644)
	id, why := snap.New(state, ws, 1<<30).Take(context.Background())
	if why != "" {
		t.Fatal(why)
	}
	st, err := store.Open(state)
	if err != nil {
		t.Fatal(err)
	}
	log, err := st.Create("s_rewind")
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	input, _ := json.Marshal(map[string]string{"command": "rm keep.txt"})
	log.Append(proto.EvToolCall, 0, proto.ToolCall{ID: "b1", Name: "bash", Input: input})
	ev, err := log.Append(proto.EvCheckpoint, 0, proto.Checkpoint{ID: "b1", Workspace: ws, Snapshot: id})
	if err != nil {
		t.Fatal(err)
	}
	input, _ = json.Marshal(map[string]string{"command": "make"})
	log.Append(proto.EvToolCall, 0, proto.ToolCall{ID: "b2", Name: "bash", Input: input})
	log.Append(proto.EvCheckpoint, 0, proto.Checkpoint{ID: "b2", Workspace: ws, Unavailable: "the working tree is over the cap"})
	os.Remove(filepath.Join(ws, "keep.txt"))
	os.WriteFile(filepath.Join(ws, "new\nname.txt"), []byte("later\n"), 0o644)
	return cfg, ws, ev.Seq
}

func rewind(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := dispatch(append([]string{"rewind"}, args...), &out, &errb)
	return code, out.String(), errb.String()
}

func TestRewind(t *testing.T) {
	cfg, ws, seq := rewindSession(t)
	s := strconv.FormatInt(seq, 10)

	// The list says what each point is, and which can't be rewound and why.
	code, out, _ := rewind(t, "-config", cfg, "s_rewind")
	if code != exitOK || !strings.Contains(out, `before "rm keep.txt"`) || !strings.Contains(out, `can't be rewound (the working tree is over the cap): before "make"`) {
		t.Errorf("list: %d %q", code, out)
	}

	// Without -yes it says what would change and changes nothing, on the log or in the tree.
	code, out, _ = rewind(t, "-config", cfg, "s_rewind", s)
	if code != exitOK || !strings.Contains(out, `restore "keep.txt"`) || !strings.Contains(out, `remove  "new\nname.txt"`) || !strings.Contains(out, "-yes") {
		t.Errorf("preview: %d %q", code, out)
	}
	if _, err := os.Stat(filepath.Join(ws, "keep.txt")); err == nil {
		t.Error("the preview restored the file")
	}

	// With it, the tree is back and the log says so.
	code, out, errs := rewind(t, "-config", cfg, "-yes", "s_rewind", s)
	if code != exitOK {
		t.Fatalf("rewind: %d %s", code, errs)
	}
	if b, _ := os.ReadFile(filepath.Join(ws, "keep.txt")); string(b) != "work\n" {
		t.Errorf("keep.txt: %q", b)
	}
	if _, err := os.Stat(filepath.Join(ws, "new\nname.txt")); err == nil {
		t.Error("the later file stayed")
	}
	state := filepath.Join(filepath.Dir(cfg), "state")
	st, _ := store.Open(state)
	events, err := st.Events("s_rewind", 0)
	if err != nil {
		t.Fatal(err)
	}
	last := events[len(events)-1]
	v, err := last.Decode()
	if err != nil {
		t.Fatal(err)
	}
	r, ok := v.(proto.Rewind)
	if !ok || r.To != seq || r.Undo == "" || r.Changed != 2 || r.Workspace != ws {
		t.Errorf("last event %+v", v)
	}

	// The rewind is itself on the list, and going back to it undoes it.
	if !strings.Contains(out, strconv.FormatInt(last.Seq, 10)) {
		t.Errorf("no way back in %q", out)
	}
	if code, out, errs = rewind(t, "-config", cfg, "-yes", "s_rewind", strconv.FormatInt(last.Seq, 10)); code != exitOK {
		t.Fatalf("undo: %d %s %s", code, out, errs)
	}
	if _, err := os.Stat(filepath.Join(ws, "keep.txt")); err == nil {
		t.Error("the undo didn't take keep.txt away again")
	}
	if b, _ := os.ReadFile(filepath.Join(ws, "new\nname.txt")); string(b) != "later\n" {
		t.Errorf("the later file: %q", b)
	}

	// Going to where the tree already is says so.
	if _, out, _ = rewind(t, "-config", cfg, "s_rewind", strconv.FormatInt(last.Seq, 10)); !strings.Contains(out, "already matches") {
		t.Errorf("no change: %q", out)
	}
}

func TestRewindRefuses(t *testing.T) {
	cfg, ws, seq := rewindSession(t)
	for _, c := range []struct {
		args []string
		want string
		code int
	}{
		{[]string{"-config", cfg, "s_rewind", "99"}, "not a checkpoint", exitError},
		{[]string{"-config", cfg, "s_rewind", "x"}, "not a seq", exitError},
		{[]string{"-config", cfg, "s_rewind", strconv.FormatInt(seq+2, 10)}, "can't be rewound: the working tree is over the cap", exitError},
		{[]string{"-config", cfg, "s_none"}, "", exitError},
		{[]string{"-config", cfg}, "usage", exitUsage},
		{[]string{"-config", cfg, "a", "1", "2"}, "usage", exitUsage},
	} {
		code, _, errs := rewind(t, c.args...)
		if code != c.code || !strings.Contains(errs, c.want) {
			t.Errorf("%v: %d %q", c.args, code, errs)
		}
	}
	// A halt stops the writing, not the looking.
	state := filepath.Join(filepath.Dir(cfg), "state")
	if _, err := halt.Halt(state, "test", ""); err != nil {
		t.Fatal(err)
	}
	s := strconv.FormatInt(seq, 10)
	if code, _, errs := rewind(t, "-config", cfg, "-yes", "s_rewind", s); code != exitError || !strings.Contains(errs, "halted") {
		t.Errorf("halted: %d %q", code, errs)
	}
	if _, err := os.Stat(filepath.Join(ws, "keep.txt")); err == nil {
		t.Error("it restored while halted")
	}
	if code, _, _ := rewind(t, "-config", cfg, "s_rewind", s); code != exitOK {
		t.Errorf("preview while halted: %d", code)
	}
}
