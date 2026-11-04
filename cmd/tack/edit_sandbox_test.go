//go:build darwin && sandboxtest

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
)

// TestEditRunInTheSandbox: tack run --edit, end to end on macOS. A scripted model writes a file, runs go test through
// bash (the real tack shell under the real Seatbelt profile), then calls done. The run exits 0 with stop reason done,
// the file is written, the test passed inside the sandbox, and a command writing outside the workspace was refused.
func TestEditRunInTheSandbox(t *testing.T) {
	ws, _ := filepath.EvalSymlinks(t.TempDir())
	outside, _ := filepath.EvalSymlinks(t.TempDir())
	os.WriteFile(filepath.Join(ws, "go.mod"), []byte("module x\n\ngo 1.27\n"), 0o644)
	os.WriteFile(filepath.Join(ws, "x.go"), []byte("package x\n\nfunc Add(a, b int) int { return a - b }\n"), 0o644)
	os.WriteFile(filepath.Join(ws, "x_test.go"), []byte("package x\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(2, 2) != 4 {\n\t\tt.Fatal(Add(2, 2))\n\t}\n}\n"), 0o644)
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"commit", "-q", "-m", "base"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = ws
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	script := goinfertest.NewScripted("m", 32768,
		goinfertest.Calls("", "r1", "read", `{"path":"x.go"}`),
		goinfertest.Calls("", "w1", "write", `{"path":"x.go","content":"package x\n\nfunc Add(a, b int) int { return a + b }\n"}`),
		goinfertest.Calls("", "b1", "bash", `{"command":"go test ./..."}`),
		goinfertest.Calls("", "b2", "bash", `{"command":"/usr/bin/touch `+filepath.Join(outside, "planted")+`"}`),
		goinfertest.Calls("", "d1", "done", `{"reason":"Add adds"}`))
	ts := httptest.NewServer(script)
	defer ts.Close()
	var stdout, stderr bytes.Buffer
	code := dispatch([]string{"run", "--config", writeConfig(t, ts.URL), "--workspace", ws, "--edit", "--json", "Make Add add."}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("exit %d\nstderr: %s\nstdout: %s", code, stderr.String(), stdout.String())
	}
	if b, _ := os.ReadFile(filepath.Join(ws, "x.go")); !strings.Contains(string(b), "a + b") {
		t.Fatalf("x.go: %s", b)
	}
	if _, err := os.Stat(filepath.Join(outside, "planted")); err == nil {
		t.Fatal("a command wrote outside the workspace")
	}
	results := map[string]proto.ToolFinished{}
	stop := ""
	for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		var ev proto.Event
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		v, _ := ev.Decode()
		switch p := v.(type) {
		case proto.ToolFinished:
			results[p.ID] = p
		case proto.TurnEnd:
			stop = p.StopReason
		}
	}
	if stop != proto.StopDone {
		t.Errorf("stop reason %q", stop)
	}
	if r := results["b1"]; r.Outcome != proto.OutcomeOK || !strings.Contains(r.Result, "ok") {
		t.Errorf("go test in the sandbox: %+v", r)
	}
	if r := results["b2"]; !strings.Contains(r.Result, "Operation not permitted") {
		t.Errorf("writing outside the workspace: %+v", r)
	}
}

// TestEditRefusesTackPathsOverlappingTheWorkspace: tack run --edit refuses to start when tack's config directory is
// inside the workspace or contains it, before any model request. Inside, tack's own read tool, which runs outside the
// sandbox, would hand the model tack's token (the escape this guards). Containing it, the sandbox's section tack denies
// every command its working directory (docs/measurements/e1-pilot-a1-2026-10-18.md, "The void night").
func TestEditRefusesTackPathsOverlappingTheWorkspace(t *testing.T) {
	const secret = "tack-token-7f3a"
	for _, c := range []struct {
		name string
		cfg  func(ws string) string // the config directory, given the workspace
		ws   func(root string) string
		call string
	}{
		{"config inside the workspace", func(ws string) string { return filepath.Join(ws, ".tack") },
			func(root string) string { return root }, `{"path":".tack/token"}`},
		{"config above the workspace", func(ws string) string { return filepath.Dir(ws) },
			func(root string) string { return filepath.Join(root, "repo") }, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			root, _ := filepath.EvalSymlinks(t.TempDir())
			ws := c.ws(root)
			os.MkdirAll(ws, 0o755)
			os.WriteFile(filepath.Join(ws, "go.mod"), []byte("module x\n\ngo 1.27\n"), 0o644)
			cd := c.cfg(ws)
			os.MkdirAll(cd, 0o755)
			os.WriteFile(filepath.Join(cd, "token"), []byte(secret+"\n"), 0o600)
			p := filepath.Join(cd, "config.toml")
			os.WriteFile(p, []byte(fmt.Sprintf("state_dir = %q\n", filepath.Join(cd, "state"))), 0o600)
			calls := []goinfertest.Reply{goinfertest.Calls("", "b1", "bash", `{"command":"go version"}`)}
			if c.call != "" {
				calls = []goinfertest.Reply{goinfertest.Calls("", "r1", "read", c.call)}
			}
			script := goinfertest.NewScripted("m", 32768, append(calls, goinfertest.Calls("", "d1", "done", `{"reason":"x"}`))...)
			ts := httptest.NewServer(script)
			defer ts.Close()
			f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
			fmt.Fprintf(f, "[servers.local]\nkind = \"goinfer-remote\"\nurl = %q\ntoken_file = %q\n", ts.URL, filepath.Join(cd, "token"))
			f.Close()
			var stdout, stderr bytes.Buffer
			code := dispatch([]string{"run", "--config", p, "--workspace", ws, "--edit", "--json", "Look."}, &stdout, &stderr)
			if strings.Contains(stdout.String(), secret) {
				t.Errorf("ESCAPED: tack's token reached the model's tool result\n%s", stdout.String())
			}
			if code != exitError || !strings.Contains(stderr.String(), "overlaps the workspace") {
				t.Errorf("exit %d, want %d with the overlap named\nstderr: %s\nstdout: %s", code, exitError, stderr.String(), stdout.String())
			}
			if script.Left() != len(calls)+1 {
				t.Errorf("%d model requests were made; want none", len(calls)+1-script.Left())
			}
		})
	}
}

// TestEditRunBestOf: [run] best_of through tack run --edit, in the sandbox. Attempt 1's change fails the package's own
// test and is undone; attempt 2's passes and is kept. Only attempt 2's events reach stdout, and stderr says how each
// attempt verified.
func TestEditRunBestOf(t *testing.T) {
	ws, _ := filepath.EvalSymlinks(t.TempDir())
	os.WriteFile(filepath.Join(ws, "go.mod"), []byte("module x\n\ngo 1.27\n"), 0o644)
	os.WriteFile(filepath.Join(ws, "x.go"), []byte("package x\n\nfunc Add(a, b int) int { return a - b }\n"), 0o644)
	os.WriteFile(filepath.Join(ws, "x_test.go"), []byte("package x\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(2, 2) != 4 {\n\t\tt.Fatal(Add(2, 2))\n\t}\n}\n"), 0o644)
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"commit", "-q", "-m", "base"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = ws
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	script := goinfertest.NewScripted("m", 32768,
		goinfertest.Calls("", "r1", "read", `{"path":"x.go"}`),
		goinfertest.Calls("", "w1", "write", `{"path":"x.go","content":"package x\n\nfunc Add(a, b int) int { return a * b + 1 }\n"}`),
		goinfertest.Calls("", "d1", "done", `{"reason":"first try"}`),
		goinfertest.Calls("", "r2", "read", `{"path":"x.go"}`),
		goinfertest.Calls("", "w2", "write", `{"path":"x.go","content":"package x\n\nfunc Add(a, b int) int { return a + b }\n"}`),
		goinfertest.Calls("", "d2", "done", `{"reason":"second try"}`))
	ts := httptest.NewServer(script)
	defer ts.Close()
	cfg := writeConfig(t, ts.URL)
	f, _ := os.OpenFile(cfg, os.O_APPEND|os.O_WRONLY, 0)
	fmt.Fprint(f, "[run]\nbest_of = 2\n")
	f.Close()
	var stdout, stderr bytes.Buffer
	code := dispatch([]string{"run", "--config", cfg, "--workspace", ws, "--edit", "--json", "Make Add add."}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("exit %d\nstderr: %s", code, stderr.String())
	}
	if b, _ := os.ReadFile(filepath.Join(ws, "x.go")); !strings.Contains(string(b), "a + b") {
		t.Fatalf("x.go: %s", b)
	}
	if !strings.Contains(stderr.String(), "best_of attempt 1 of 2 fails verification") ||
		!strings.Contains(stderr.String(), "best_of attempt 2 of 2 passes verification") {
		t.Fatalf("stderr does not say how the attempts verified:\n%s", stderr.String())
	}
	out := stdout.String()
	if strings.Contains(out, `"w1"`) || !strings.Contains(out, `"w2"`) || strings.Count(out, `"type":"turn.end"`) != 1 {
		t.Fatalf("stdout should carry attempt 2's events only:\n%.1500s", out)
	}
}

// TestSnapshotsInTheSandbox: end to end on macOS, under the real Seatbelt profile. In auto mode git reset --hard over uncommitted
// work runs with no question and destroys it; a sandboxed command can't reach the shadow repository, so it can't tamper with what
// a rewind brings back; and tack rewind restores the work.
func TestSnapshotsInTheSandbox(t *testing.T) {
	ws, _ := filepath.EvalSymlinks(t.TempDir())
	os.WriteFile(filepath.Join(ws, "work.txt"), []byte("committed\n"), 0o644)
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"commit", "-q", "-m", "base"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = ws
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	os.WriteFile(filepath.Join(ws, "work.txt"), []byte("a day's work\n"), 0o644)
	var script http.Handler // the model's script names the state directory, which the config holding the server's URL decides
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { script.ServeHTTP(w, r) }))
	defer ts.Close()
	cfg := writeConfig(t, ts.URL)
	state := filepath.Join(filepath.Dir(cfg), "state")
	script = goinfertest.NewScripted("m", 32768,
		goinfertest.Calls("", "b1", "bash", `{"command":"git reset --hard"}`),
		goinfertest.Calls("", "b2", "bash", `{"command":"/bin/ls `+filepath.Join(state, "shadows")+`"}`),
		goinfertest.Calls("", "d1", "done", `{"reason":"x"}`),
		goinfertest.Calls("", "d2", "done", `{"reason":"y"}`))
	var stdout, stderr bytes.Buffer
	code := dispatch([]string{"run", "--config", cfg, "--workspace", ws, "--edit", "--json", "Go."}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("exit %d\nstderr: %s\nstdout: %s", code, stderr.String(), stdout.String())
	}
	if b, _ := os.ReadFile(filepath.Join(ws, "work.txt")); string(b) != "committed\n" {
		t.Fatalf("git reset --hard didn't run: %q\n%s\n%s", b, stdout.String(), stderr.String())
	}
	var session string
	var seq int64
	results := map[string]proto.ToolFinished{}
	for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		var ev proto.Event
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		v, _ := ev.Decode()
		switch p := v.(type) {
		case proto.ToolFinished:
			results[p.ID] = p
		case proto.Checkpoint:
			if p.ID == "b1" {
				if p.Snapshot == "" {
					t.Fatalf("no snapshot: %+v", p)
				}
				seq = ev.Seq
			}
		}
	}
	if r := results["b1"]; r.Outcome != proto.OutcomeOK {
		t.Errorf("git reset --hard: %+v", r)
	}
	if r := results["b2"]; !strings.Contains(r.Result, "Operation not permitted") {
		t.Errorf("a command reached the shadow: %+v", r)
	}
	entries, err := os.ReadDir(filepath.Join(state, "sessions"))
	if err != nil || len(entries) == 0 {
		t.Fatalf("sessions: %v %v", entries, err)
	}
	session = strings.TrimSuffix(entries[0].Name(), filepath.Ext(entries[0].Name()))
	var out, errb bytes.Buffer
	if code := dispatch([]string{"rewind", "-config", cfg, "-yes", session, fmt.Sprint(seq)}, &out, &errb); code != exitOK {
		t.Fatalf("rewind: %d %s %s", code, out.String(), errb.String())
	}
	if b, _ := os.ReadFile(filepath.Join(ws, "work.txt")); string(b) != "a day's work\n" {
		t.Errorf("after the rewind: %q", b)
	}
}

// TestSnapshotsOffInTheSandbox: with snapshots = false, as E1 sets it, there's no checkpoint, the guard asks with nobody to answer,
// and the work stays.
func TestSnapshotsOffInTheSandbox(t *testing.T) {
	ws, _ := filepath.EvalSymlinks(t.TempDir())
	os.WriteFile(filepath.Join(ws, "work.txt"), []byte("committed\n"), 0o644)
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"commit", "-q", "-m", "base"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = ws
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	os.WriteFile(filepath.Join(ws, "work.txt"), []byte("a day's work\n"), 0o644)
	script := goinfertest.NewScripted("m", 32768,
		goinfertest.Calls("", "b1", "bash", `{"command":"git reset --hard"}`),
		goinfertest.Calls("", "d1", "done", `{"reason":"x"}`))
	ts := httptest.NewServer(script)
	defer ts.Close()
	cfg := writeConfig(t, ts.URL)
	f, err := os.OpenFile(cfg, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("\n[run]\nsnapshots = false\n")
	f.Close()
	var stdout, stderr bytes.Buffer
	if code := dispatch([]string{"run", "--config", cfg, "--workspace", ws, "--edit", "--json", "Go."}, &stdout, &stderr); code != exitOK {
		t.Fatalf("exit %d\nstderr: %s\nstdout: %s", code, stderr.String(), stdout.String())
	}
	if strings.Contains(stdout.String(), `"checkpoint"`) {
		t.Error("a checkpoint with snapshots off")
	}
	if b, _ := os.ReadFile(filepath.Join(ws, "work.txt")); string(b) != "a day's work\n" {
		t.Errorf("the guard let it run: %q", b)
	}
	if !strings.Contains(stdout.String(), "without a snapshot") {
		t.Errorf("no reason in %s", stdout.String())
	}
}
