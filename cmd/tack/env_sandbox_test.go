//go:build darwin && sandboxtest

package main

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
	"github.com/townsendmerino/tack/internal/trust"
)

// TestCommandEnvironmentInTheSandbox: tack run --edit, end to end under the real Seatbelt profile. A command that runs env
// sees the short environment and not tack's own: a token and an unlisted variable in tack's environment do not reach it
// (DESIGN §6.6). A variable the repository's .tack/config.toml names reaches it only once the owner has trusted that file,
// and not after the file changes; a secret-named variable never does.
func TestCommandEnvironmentInTheSandbox(t *testing.T) {
	t.Setenv("GH_TOKEN", "ghp_tack_secret_1")
	t.Setenv("UNLISTED_VAR", "unlisted_value_2")
	t.Setenv("MY_FLAG", "flag_value_3")
	t.Setenv("DEPLOY_TOKEN", "deploy_secret_4")
	const config = "[commands]\nenv = [\"MY_FLAG\"]\n"
	for _, c := range []struct {
		name      string
		grant     bool   // the owner trusts the file before the run
		changeTo  string // the file is rewritten after the grant, when set
		wantFlag  bool
		wantNotes string // what the owner's notice says, when not applied
	}{
		{"untrusted", false, "", false, "not trusted"},
		{"trusted", true, "", true, ""},
		{"changed after trust", true, "[commands]\nenv = [\"MY_FLAG\", \"UNLISTED_VAR\"]\n", false, "changed since you trusted"},
	} {
		t.Run(c.name, func(t *testing.T) {
			ws, _ := filepath.EvalSymlinks(t.TempDir())
			os.MkdirAll(filepath.Join(ws, ".tack"), 0o755)
			os.WriteFile(filepath.Join(ws, trust.ConfigPath), []byte(config), 0o644)
			os.WriteFile(filepath.Join(ws, "x.txt"), []byte("x\n"), 0o644)
			for _, args := range [][]string{{"init", "-q"}, {"add", "x.txt"}, {"commit", "-q", "-m", "base"}} {
				cmd := exec.Command("git", args...)
				cmd.Dir = ws
				cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("git %v: %v %s", args, err, out)
				}
			}
			script := goinfertest.NewScripted("m", 32768,
				goinfertest.Calls("", "b1", "bash", `{"command":"/usr/bin/env"}`),
				goinfertest.Calls("", "d1", "done", `{"reason":"seen"}`),
				goinfertest.Calls("", "d2", "done", `{"reason":"seen"}`))
			ts := httptest.NewServer(script)
			defer ts.Close()
			cfg := writeConfig(t, ts.URL)
			if c.grant {
				_, digest, _ := trust.ReadRepo(ws)
				if err := trust.Open(filepath.Join(filepath.Dir(cfg), "state")).Trust(ws, digest, time.Now()); err != nil {
					t.Fatal(err)
				}
			}
			if c.changeTo != "" {
				os.WriteFile(filepath.Join(ws, trust.ConfigPath), []byte(c.changeTo), 0o644)
			}
			var stdout, stderr bytes.Buffer
			dispatch([]string{"run", "--config", cfg, "--workspace", ws, "--edit", "--json", "Look at the environment."}, &stdout, &stderr)
			var env string
			var trusted bool
			var notes []string
			for line := range strings.SplitSeq(strings.TrimSpace(stdout.String()), "\n") {
				var ev proto.Event
				if json.Unmarshal([]byte(line), &ev) != nil {
					continue
				}
				switch v, _ := ev.Decode(); p := v.(type) {
				case proto.ToolFinished:
					if p.ID == "b1" {
						env = p.Result
					}
				case proto.RepoTrusted:
					trusted = true
				case proto.ErrorEvent:
					if p.Class == "instructions" {
						notes = append(notes, p.Message)
					}
				}
			}
			if !strings.Contains(env, "PATH=") || !strings.Contains(env, "HOME=") {
				t.Fatalf("the command's environment lacks the short list:\n%s\nstderr: %s", env, stderr.String())
			}
			for _, leaked := range []string{"GH_TOKEN", "ghp_tack_secret_1", "UNLISTED_VAR", "unlisted_value_2", "DEPLOY_TOKEN", "deploy_secret_4"} {
				if strings.Contains(env, leaked) {
					t.Errorf("%s reached the command:\n%s", leaked, env)
				}
			}
			if got := strings.Contains(env, "MY_FLAG=flag_value_3"); got != c.wantFlag {
				t.Errorf("MY_FLAG reached the command: %v, want %v\n%s", got, c.wantFlag, env)
			}
			if trusted != c.wantFlag {
				t.Errorf("repo.trusted in the log: %v, want %v", trusted, c.wantFlag)
			}
			switch {
			case c.wantNotes == "" && len(notes) != 0:
				t.Errorf("notices %q for a trusted file", notes)
			case c.wantNotes != "" && (len(notes) != 1 || !strings.Contains(notes[0], c.wantNotes)):
				t.Errorf("notices %q, want one saying %q", notes, c.wantNotes)
			}
		})
	}
}
