//go:build darwin

package main

import (
	"os"
	"os/exec"
	"path/filepath"

	"github.com/townsendmerino/tack/internal/core"
	"github.com/townsendmerino/tack/internal/perms"
	"github.com/townsendmerino/tack/internal/sandbox"
	"github.com/townsendmerino/tack/internal/shell"
)

// editKit gives an auto-mode session its sandboxed shell (DESIGN §6.6). Commands run in `tack shell` under Seatbelt,
// marked with the session's group and each command's own mark. They may write only the workspace, a temp directory
// of the session's own and tack's Go cache (under the user cache directory); tack's state directory and config directory are out of their reach.
func editKit(stateDir, configDir string) func(root string, group sandbox.Target) (core.EditKit, error) {
	return func(root string, group sandbox.Target) (core.EditKit, error) {
		exe, err := os.Executable()
		if err != nil {
			return core.EditKit{}, err
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return core.EditKit{}, err
		}
		tmp, err := os.MkdirTemp("", "tack-session-")
		if err != nil {
			return core.EditKit{}, err
		}
		// tack's own Go cache, apart from the owner's and outside tack's state directory, which commands cannot reach.
		cacheDir, err := os.UserCacheDir()
		if err != nil {
			return core.EditKit{}, err
		}
		gocache := filepath.Join(cacheDir, "tack", "gocache")
		if err := os.MkdirAll(gocache, 0o755); err != nil {
			return core.EditKit{}, err
		}
		cfg := sandbox.Config{Workspace: root, Temp: tmp, GoCache: gocache, Home: home, Tack: []string{stateDir, configDir}}
		eng := &perms.Engine{Root: root, Path: os.Getenv("PATH"), Sandboxed: true}
		sh := &shell.Session{Exe: exe, Args: []string{"shell"}, Engine: eng, Group: group,
			Env: []string{"TMPDIR=" + tmp, "GOCACHE=" + gocache},
			Wrap: func(c *exec.Cmd, g, mark string) *exec.Cmd {
				cc := cfg
				cc.Group, cc.Mark = g, mark
				return sandbox.Wrap(cc, c)
			},
			Sweep: func(t sandbox.Target) (sandbox.Report, error) { return sandbox.Sweep(t, false) }}
		return core.EditKit{Shell: sh, Engine: eng}, nil
	}
}
