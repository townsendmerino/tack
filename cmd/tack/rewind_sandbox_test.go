//go:build darwin && sandboxtest

package main

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
	"github.com/townsendmerino/tack/internal/store"
)

// TestRewindOfWhatASandboxedCommandPlanted: tack run --edit, end to end under the real Seatbelt profile. The model edits
// a.txt (a checkpoint holding its pre-image), then runs a command that replaces a.txt with a symlink to a file outside the
// workspace, or with a FIFO, as a sandboxed command can. tack rewind to the edit's checkpoint then refuses, the preview
// as well as the restore, leaves what the command planted where it is, writes nothing outside the workspace, and does not
// hang on the FIFO. The control plants nothing and the rewind restores a.txt, so the refusals are the planted file's doing.
func TestRewindOfWhatASandboxedCommandPlanted(t *testing.T) {
	for _, c := range []struct {
		name  string
		plant string // the command; OUTSIDE is the directory outside the workspace
		mode  fs.FileMode
	}{
		{"control, nothing planted", "true", 0},
		{"a symlink to a file outside", "rm a.txt && ln -s OUTSIDE/target a.txt", fs.ModeSymlink},
		{"a FIFO", "rm a.txt && mkfifo a.txt", fs.ModeNamedPipe},
	} {
		t.Run(c.name, func(t *testing.T) {
			ws, _ := filepath.EvalSymlinks(t.TempDir())
			outside, _ := filepath.EvalSymlinks(t.TempDir())
			target := filepath.Join(outside, "target")
			os.WriteFile(target, []byte("theirs\n"), 0o644)
			os.WriteFile(filepath.Join(ws, "a.txt"), []byte("v0\n"), 0o644)
			for _, args := range [][]string{{"init", "-q"}, {"add", "a.txt"}, {"commit", "-q", "-m", "base"}} {
				cmd := exec.Command("git", args...)
				cmd.Dir = ws
				cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("git %v: %v %s", args, err, out)
				}
			}
			plant, _ := json.Marshal(map[string]string{"command": strings.ReplaceAll(c.plant, "OUTSIDE", outside)})
			script := goinfertest.NewScripted("m", 32768,
				goinfertest.Calls("", "r1", "read", `{"path":"a.txt"}`),
				goinfertest.Calls("", "e1", "edit", `{"path":"a.txt","old_string":"v0","new_string":"v1"}`),
				goinfertest.Calls("", "b1", "bash", string(plant)),
				goinfertest.Calls("", "d1", "done", `{"reason":"done"}`))
			ts := httptest.NewServer(script)
			defer ts.Close()
			cfg := writeConfig(t, ts.URL)
			var stdout, stderr bytes.Buffer
			if code := dispatch([]string{"run", "--config", cfg, "--workspace", ws, "--edit", "--json", "Edit a.txt."}, &stdout, &stderr); code != exitOK {
				t.Fatalf("run: exit %d\nstderr: %s\nstdout: %s", code, stderr.String(), stdout.String())
			}
			for line := range strings.SplitSeq(strings.TrimSpace(stdout.String()), "\n") {
				var ev proto.Event
				if json.Unmarshal([]byte(line), &ev) != nil {
					continue
				}
				if v, _ := ev.Decode(); v != nil {
					if p, ok := v.(proto.ToolFinished); ok && p.ID == "b1" && p.Outcome != proto.OutcomeOK {
						t.Fatalf("the planting command failed in the sandbox: %+v", p)
					}
				}
			}
			info, err := os.Lstat(filepath.Join(ws, "a.txt"))
			if err != nil || info.Mode().Type() != c.mode {
				t.Fatalf("a.txt after the command: %v, %v; want mode type %v", info, err, c.mode)
			}

			st, err := store.Open(filepath.Join(filepath.Dir(cfg), "state"))
			if err != nil {
				t.Fatal(err)
			}
			ids, err := st.Sessions()
			if err != nil || len(ids) != 1 {
				t.Fatalf("sessions: %v, %v", ids, err)
			}
			events, err := st.Events(ids[0], 0)
			if err != nil {
				t.Fatal(err)
			}
			var seq int64
			for _, ev := range events {
				if v, _ := ev.Decode(); v != nil {
					if cp, ok := v.(proto.Checkpoint); ok && len(cp.Files) > 0 {
						seq = ev.Seq
						break
					}
				}
			}
			if seq == 0 {
				t.Fatal("no checkpoint holding the edit's pre-image in the log")
			}

			// rewind runs under a deadline: a FIFO opened for read blocks forever.
			type result struct {
				code      int
				out, errs string
			}
			run := func(args ...string) result {
				done := make(chan result, 1)
				go func() {
					var out, errb bytes.Buffer
					code := dispatch(append([]string{"rewind", "-config", cfg}, args...), &out, &errb)
					done <- result{code, out.String(), errb.String()}
				}()
				select {
				case r := <-done:
					return r
				case <-time.After(20 * time.Second):
					t.Fatalf("tack rewind %v hung", args)
					return result{}
				}
			}
			s := strconv.FormatInt(seq, 10)
			preview := run(string(ids[0]), s)
			restore := run("-yes", "-no-undo", string(ids[0]), s)
			if c.mode == 0 {
				if preview.code != exitOK || restore.code != exitOK {
					t.Fatalf("control: preview %+v, restore %+v; want both to succeed", preview, restore)
				}
				if b, _ := os.ReadFile(filepath.Join(ws, "a.txt")); string(b) != "v0\n" {
					t.Fatalf("control: a.txt holds %q after the rewind, want its pre-image", b)
				}
				return
			}
			if preview.code == exitOK || restore.code == exitOK {
				t.Errorf("preview %+v, restore %+v; want both refused", preview, restore)
			}
			if info, err := os.Lstat(filepath.Join(ws, "a.txt")); err != nil || info.Mode().Type() != c.mode {
				t.Errorf("a.txt after the refused rewind: %v, %v; want what the command planted, untouched", info, err)
			}
			if b, _ := os.ReadFile(target); string(b) != "theirs\n" {
				t.Errorf("the file outside the workspace holds %q", b)
			}
			if ents, _ := os.ReadDir(outside); len(ents) != 1 {
				t.Errorf("something was written outside the workspace: %v", ents)
			}
		})
	}
}
