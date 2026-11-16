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
	"syscall"
	"testing"
	"time"

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
	log.Append(proto.EvSessionCreated, 0, proto.SessionCreated{Workspace: ws})
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

	// Going to where the tree already is says so, and with -yes does nothing, on the log either.
	if _, out, _ = rewind(t, "-config", cfg, "s_rewind", strconv.FormatInt(last.Seq, 10)); !strings.Contains(out, "already matches") {
		t.Errorf("no change: %q", out)
	}
	before, _ := st.Events("s_rewind", 0)
	if code, out, _ = rewind(t, "-config", cfg, "-yes", "s_rewind", strconv.FormatInt(last.Seq, 10)); code != exitOK || !strings.Contains(out, "already matches") {
		t.Errorf("no change with -yes: %d %q", code, out)
	}
	if after, _ := st.Events("s_rewind", 0); len(after) != len(before) {
		t.Errorf("a rewind that changed nothing is on the log: %d, then %d events", len(before), len(after))
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

// A rewind that stops partway says so, and leaves the way back on the log.
func TestRewindThatStopsPartway(t *testing.T) {
	cfg, ws, seq := rewindSession(t)
	state := filepath.Join(filepath.Dir(cfg), "state")
	shadow := snap.New(state, ws, 1<<30)
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	script := "#!/bin/sh\ncase \" $* \" in *\" checkout-index \"*) exit 1 ;; esac\nexec " + realGit + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	_ = shadow
	code, _, errs := rewind(t, "-config", cfg, "-yes", "s_rewind", strconv.FormatInt(seq, 10))
	if code != exitError || !strings.Contains(errs, "partway") || !strings.Contains(errs, "undoes") {
		t.Errorf("%d %q", code, errs)
	}
	st, _ := store.Open(state)
	events, err := st.Events("s_rewind", 0)
	if err != nil {
		t.Fatal(err)
	}
	v, err := events[len(events)-1].Decode()
	if err != nil {
		t.Fatal(err)
	}
	if r, ok := v.(proto.Rewind); !ok || r.Undo == "" {
		t.Errorf("last event %+v", v)
	}
}

// A tree that can't be snapshotted now can still be looked at and rewound, and it says there's no way back.
func TestRewindOfATreeThatCantBeSnapshotted(t *testing.T) {
	cfg, ws, seq := rewindSession(t)
	if err := os.Mkdir(filepath.Join(ws, "clone"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := exec.Command("git", "init", "-q")
	c.Dir = filepath.Join(ws, "clone")
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git: %v %s", err, out)
	}
	s := strconv.FormatInt(seq, 10)
	code, out, errs := rewind(t, "-config", cfg, "s_rewind", s)
	if code != exitOK || !strings.Contains(out, `remove  "clone"`) || !strings.Contains(out, "can't be undone") {
		t.Errorf("preview: %d %q %q", code, out, errs)
	}
	if _, err := os.Stat(filepath.Join(ws, "clone")); err != nil {
		t.Error("the preview removed it")
	}
	if !strings.Contains(out, "-no-undo") {
		t.Errorf("the preview doesn't say how: %q", out)
	}
	// -yes alone refuses, and says how to go on.
	code, _, errs = rewind(t, "-config", cfg, "-yes", "s_rewind", s)
	if code != exitError || !strings.Contains(errs, "can't be undone") || !strings.Contains(errs, "-no-undo") {
		t.Errorf("-yes alone: %d %q", code, errs)
	}
	if _, err := os.Stat(filepath.Join(ws, "clone")); err != nil {
		t.Error("-yes alone removed it")
	}
	// -no-undo alone is still a preview.
	if code, out, _ = rewind(t, "-config", cfg, "-no-undo", "s_rewind", s); code != exitOK || !strings.Contains(out, "can't be undone") {
		t.Errorf("-no-undo alone: %d %q", code, out)
	}
	if _, err := os.Stat(filepath.Join(ws, "clone")); err != nil {
		t.Error("-no-undo alone removed it")
	}
	code, out, errs = rewind(t, "-config", cfg, "-yes", "-no-undo", "s_rewind", s)
	if code != exitOK || !strings.Contains(out, "can't be undone") {
		t.Fatalf("rewind: %d %q %q", code, out, errs)
	}
	if b, _ := os.ReadFile(filepath.Join(ws, "keep.txt")); string(b) != "work\n" {
		t.Errorf("keep.txt: %q", b)
	}
	if _, err := os.Stat(filepath.Join(ws, "clone")); err == nil {
		t.Error("the clone stayed")
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
	if r, ok := v.(proto.Rewind); !ok || r.Undo != "" || !strings.Contains(r.NoUndo, "nested") {
		t.Errorf("last event %+v", v)
	}
	// On the list as something that can't be gone back to, and why.
	if _, out, _ = rewind(t, "-config", cfg, "s_rewind"); !strings.Contains(out, "can't be rewound (it holds a nested") {
		t.Errorf("list: %q", out)
	}
}

// One warning per reason, however they come round.
func TestNoRewindWarnsOncePerReason(t *testing.T) {
	var out bytes.Buffer
	seen := map[string]bool{}
	for _, why := range []string{"", "over the cap", "nested", "over the cap", "", "nested"} {
		warnNoRewind(&out, seen, why)
	}
	if got := strings.Count(out.String(), "\n"); got != 2 {
		t.Errorf("%d warnings: %q", got, out.String())
	}
}

// A path that's both removed and brought back, a file for a directory, counts once.
func TestRewoundPaths(t *testing.T) {
	got := rewoundPaths([]snap.Change{{Path: "a", Kind: snap.Bring}, {Path: "a", Kind: snap.Remove}, {Path: "b", Kind: snap.Remove}})
	if got != 2 {
		t.Errorf("%d paths", got)
	}
}

func TestRewindSeqMustBeANumber(t *testing.T) {
	cfg, _, _ := rewindSession(t)
	for _, seq := range []string{"1x", "1 2", "0x1", "+", "9999999999999999999999"} {
		if code, _, errs := rewind(t, "-config", cfg, "s_rewind", seq); code != exitError || !strings.Contains(errs, "not a seq") {
			t.Errorf("%q: %d %q", seq, code, errs)
		}
	}
}

// imageSession is a session whose log holds two edit checkpoints and a write checkpoint over a workspace that is not a git
// repository: a.txt edited twice, new.txt created. The files are left in their final state; the returned seqs are the
// first edit's checkpoint and the write's.
func imageSession(t *testing.T) (cfg, ws string, first, write int64) {
	t.Helper()
	cfg = writeConfig(t, "http://cassette.invalid")
	state := filepath.Join(filepath.Dir(cfg), "state")
	ws, _ = filepath.EvalSymlinks(t.TempDir())
	st, err := store.Open(state)
	if err != nil {
		t.Fatal(err)
	}
	put := func(s string) string {
		h, err := st.Blobs.Put([]byte(s))
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	log, err := st.Create("s_images")
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	log.Append(proto.EvSessionCreated, 0, proto.SessionCreated{Workspace: ws})
	call := func(id, name string) {
		in, _ := json.Marshal(map[string]string{"path": "x"})
		log.Append(proto.EvToolCall, 0, proto.ToolCall{ID: id, Name: name, Input: in})
	}
	check := func(id string, imgs ...proto.FileImage) int64 {
		ev, err := log.Append(proto.EvCheckpoint, 0, proto.Checkpoint{ID: id, Workspace: ws, Files: imgs})
		if err != nil {
			t.Fatal(err)
		}
		return ev.Seq
	}
	call("e1", "edit")
	first = check("e1", proto.FileImage{Path: "a.txt", Blob: put("v0\n")})
	call("e2", "edit")
	check("e2", proto.FileImage{Path: "a.txt", Blob: put("v1\n")})
	call("w1", "write")
	write = check("w1", proto.FileImage{Path: "sub/new.txt", Absent: true})
	os.MkdirAll(filepath.Join(ws, "sub"), 0o755)
	os.WriteFile(filepath.Join(ws, "a.txt"), []byte("v2\n"), 0o644)
	os.WriteFile(filepath.Join(ws, "sub", "new.txt"), []byte("made\n"), 0o644)
	return cfg, ws, first, write
}

// The list names an edit's file instead of calling it a command that can't be rewound, and a rewind to the first edit brings
// back the oldest pre-image of each path and removes what a write created.
func TestRewindRestoresEditPreImages(t *testing.T) {
	cfg, ws, first, _ := imageSession(t)
	code, out, _ := rewind(t, "-config", cfg, "s_images")
	if code != exitOK || strings.Contains(out, "can't be rewound") || !strings.Contains(out, `before edit of "a.txt"`) || !strings.Contains(out, `before write of "sub/new.txt"`) {
		t.Errorf("list: %d %q", code, out)
	}
	s := strconv.FormatInt(first, 10)
	code, out, _ = rewind(t, "-config", cfg, "s_images", s)
	if code != exitOK || !strings.Contains(out, `restore "a.txt"`) || !strings.Contains(out, `remove  "sub/new.txt"`) {
		t.Errorf("preview: %d %q", code, out)
	}
	if b, _ := os.ReadFile(filepath.Join(ws, "a.txt")); string(b) != "v2\n" {
		t.Error("the preview changed the tree")
	}
	code, _, errs := rewind(t, "-config", cfg, "-yes", "-no-undo", "s_images", s)
	if code != exitOK {
		t.Fatalf("rewind: %d %s", code, errs)
	}
	if b, _ := os.ReadFile(filepath.Join(ws, "a.txt")); string(b) != "v0\n" {
		t.Errorf("a.txt: %q, want the oldest pre-image", b)
	}
	if _, err := os.Stat(filepath.Join(ws, "sub", "new.txt")); err == nil {
		t.Error("the written file stayed")
	}
}

// A rewind to the write alone removes what it created and leaves the earlier edits as they are.
func TestRewindToAWriteLeavesEarlierEdits(t *testing.T) {
	cfg, ws, _, write := imageSession(t)
	code, _, errs := rewind(t, "-config", cfg, "-yes", "-no-undo", "s_images", strconv.FormatInt(write, 10))
	if code != exitOK {
		t.Fatalf("rewind: %d %s", code, errs)
	}
	if b, _ := os.ReadFile(filepath.Join(ws, "a.txt")); string(b) != "v2\n" {
		t.Errorf("a.txt: %q", b)
	}
	if _, err := os.Stat(filepath.Join(ws, "sub", "new.txt")); err == nil {
		t.Error("the written file stayed")
	}
	// Without -no-undo it refuses and changes nothing.
	cfg2, ws2, first, _ := imageSession(t)
	code, _, errs = rewind(t, "-config", cfg2, "-yes", "s_images", strconv.FormatInt(first, 10))
	if code == exitOK || !strings.Contains(errs, "-no-undo") {
		t.Errorf("without -no-undo: %d %q", code, errs)
	}
	if b, _ := os.ReadFile(filepath.Join(ws2, "a.txt")); string(b) != "v2\n" {
		t.Errorf("a.txt: %q", b)
	}
}

// A path in a log that leaves the workspace, or goes through a symlink out of it, is refused and nothing is written: a log
// is data and grants nothing.
func TestRestoreImagesRefusesPathsOutsideTheWorkspace(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	outside, _ := filepath.EvalSymlinks(t.TempDir())
	st, err := store.Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	h, _ := st.Blobs.Put([]byte("planted\n"))
	tree := newTree(t, root, "")
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"../escape.txt", "/etc/escape.txt", "link/escape.txt"} {
		if _, err := tree.restore([]proto.FileImage{{Path: p, Blob: h}}, st.Blobs); err == nil {
			t.Errorf("%q: no error", p)
		}
	}
	// The file itself became a symlink to a file outside, as a sandboxed command could have made it: neither the plan nor the
	// restore follows it, and the file it points at is untouched.
	target := filepath.Join(outside, "target.txt")
	if err := os.WriteFile(target, []byte("theirs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "a.txt")); err != nil {
		t.Fatal(err)
	}
	for _, img := range []proto.FileImage{{Path: "a.txt", Blob: h}, {Path: "a.txt", Absent: true}} {
		if _, err := tree.plan([]proto.FileImage{img}, st.Blobs); err == nil {
			t.Errorf("plan of a symlink leaf %+v: no error", img)
		}
		if _, err := tree.restore([]proto.FileImage{img}, st.Blobs); err == nil {
			t.Errorf("restore of a symlink leaf %+v: no error", img)
		}
	}
	if b, _ := os.ReadFile(target); string(b) != "theirs\n" {
		t.Errorf("the file outside the workspace now holds %q", b)
	}
	if _, err := os.Lstat(filepath.Join(root, "a.txt")); err != nil {
		t.Errorf("the link was removed: %v", err)
	}
	if ents, _ := os.ReadDir(outside); len(ents) != 1 {
		t.Errorf("wrote outside the workspace: %v", ents)
	}
}

// newTree opens root as an imageTree, closed when the test ends.
func newTree(t *testing.T, root, state string) *imageTree {
	t.Helper()
	tree, err := openImageTree(root, state)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tree.close)
	return tree
}

func testBlobs(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// The preview never opens a FIFO, which would hang it, and never reads a file over the cap.
func TestPlanImagesDoesNotOpenWhatIsNotARegularFile(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	st := testBlobs(t)
	h, _ := st.Blobs.Put([]byte("x\n"))
	if err := syscall.Mkfifo(filepath.Join(root, "pipe"), 0o644); err != nil {
		t.Skip("no mkfifo:", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := newTree(t, root, "").plan([]proto.FileImage{{Path: "pipe", Blob: h}}, st.Blobs)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Error("a FIFO was planned over")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the plan hung on a FIFO")
	}
	big := filepath.Join(root, "big")
	f, _ := os.Create(big)
	f.Truncate(maxImageBytes + 1)
	f.Close()
	ch, err := newTree(t, root, "").plan([]proto.FileImage{{Path: "big", Blob: h}}, st.Blobs)
	if err != nil || len(ch) != 1 {
		t.Errorf("a file over the cap: %v, %v", ch, err)
	}
}

// A path inside tack's state directory, even one under the workspace, is refused, and so is a path not already clean.
func TestRestoreImagesRefusesStateAndUncleanPaths(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	state := filepath.Join(root, ".tackstate")
	os.MkdirAll(state, 0o755)
	st := testBlobs(t)
	h, _ := st.Blobs.Put([]byte("planted\n"))
	tree := newTree(t, root, state)
	for _, p := range []string{".tackstate/token", ".tackstate/sessions/x/events.jsonl", "a/../b.txt", "./b.txt", "a//b.txt", "a/"} {
		if _, err := tree.restore([]proto.FileImage{{Path: p, Blob: h}}, st.Blobs); err == nil {
			t.Errorf("%q: no error", p)
		}
	}
	if ents, _ := os.ReadDir(state); len(ents) != 0 {
		t.Errorf("wrote into the state directory: %v", ents)
	}
	if _, err := os.Stat(filepath.Join(root, "b.txt")); err == nil {
		t.Error("an unclean path was written")
	}
}

// A hard link to a file elsewhere is replaced by the restore, not written through.
func TestRestoreImagesDoesNotWriteThroughAHardLink(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	outside, _ := filepath.EvalSymlinks(t.TempDir())
	target := filepath.Join(outside, "t.txt")
	os.WriteFile(target, []byte("theirs\n"), 0o600)
	if err := os.Link(target, filepath.Join(root, "a.txt")); err != nil {
		t.Skip("no hard links:", err)
	}
	st := testBlobs(t)
	h, _ := st.Blobs.Put([]byte("v0\n"))
	n, err := newTree(t, root, "").restore([]proto.FileImage{{Path: "a.txt", Blob: h}}, st.Blobs)
	if err != nil || n != 1 {
		t.Fatalf("restore: %d, %v", n, err)
	}
	if b, _ := os.ReadFile(target); string(b) != "theirs\n" {
		t.Errorf("wrote through the link: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "a.txt")); string(b) != "v0\n" {
		t.Errorf("a.txt: %q", b)
	}
	if fi, _ := os.Stat(filepath.Join(root, "a.txt")); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want the file's own 0600", fi.Mode().Perm())
	}
}

// A checkpoint whose workspace is not the one the session started in is listed as unrewindable and never restored, and a later
// checkpoint of another workspace adds nothing to an earlier point's pre-images.
func TestRewindTrustsTheSessionsWorkspace(t *testing.T) {
	cfg := writeConfig(t, "http://cassette.invalid")
	state := filepath.Join(filepath.Dir(cfg), "state")
	ws, _ := filepath.EvalSymlinks(t.TempDir())
	other, _ := filepath.EvalSymlinks(t.TempDir())
	os.WriteFile(filepath.Join(other, "victim.txt"), []byte("mine\n"), 0o644)
	st, _ := store.Open(state)
	h, _ := st.Blobs.Put([]byte("planted\n"))
	log, _ := st.Create("s_ws")
	defer log.Close()
	log.Append(proto.EvSessionCreated, 0, proto.SessionCreated{Workspace: ws})
	in, _ := json.Marshal(map[string]string{"path": "a.txt"})
	log.Append(proto.EvToolCall, 0, proto.ToolCall{ID: "e1", Name: "edit", Input: in})
	ev1, _ := log.Append(proto.EvCheckpoint, 0, proto.Checkpoint{ID: "e1", Workspace: ws, Files: []proto.FileImage{{Path: "a.txt", Blob: h}}})
	log.Append(proto.EvToolCall, 0, proto.ToolCall{ID: "e2", Name: "edit", Input: in})
	ev2, _ := log.Append(proto.EvCheckpoint, 0, proto.Checkpoint{ID: "e2", Workspace: other, Files: []proto.FileImage{{Path: "victim.txt", Blob: h}}})
	evs, _ := st.Events("s_ws", 0)
	for _, p := range rewindPoints(evs) {
		if p.seq == ev1.Seq && (len(p.images) != 1 || p.images[0].Path != "a.txt") {
			t.Errorf("the session's own point took in another workspace's image: %+v", p.images)
		}
		if p.seq == ev2.Seq && (len(p.images) != 0 || p.why == "") {
			t.Errorf("the foreign checkpoint is rewindable: %+v", p)
		}
	}
	code, _, errs := rewind(t, "-config", cfg, "-yes", "-no-undo", "s_ws", strconv.FormatInt(ev2.Seq, 10))
	if code == exitOK || !strings.Contains(errs, "workspace") {
		t.Errorf("rewind to the foreign checkpoint: %d %q", code, errs)
	}
	if b, _ := os.ReadFile(filepath.Join(other, "victim.txt")); string(b) != "mine\n" {
		t.Errorf("victim.txt: %q", b)
	}
	// A log with no start event has no workspace to trust.
	log2, _ := st.Create("s_nostart")
	defer log2.Close()
	log2.Append(proto.EvCheckpoint, 0, proto.Checkpoint{ID: "e1", Workspace: ws, Files: []proto.FileImage{{Path: "a.txt", Blob: h}}})
	evs, _ = st.Events("s_nostart", 0)
	if p := rewindPoints(evs); len(p) != 1 || len(p[0].images) != 0 {
		t.Errorf("a log with no start event: %+v", p)
	}
}
