package core

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/perms"
	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/sandbox"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
	"github.com/townsendmerino/tack/internal/trust"
)

// trustFixture starts an auto-mode session over a workspace whose .tack/config.toml is config, with the trust store st
// (whose grants the test makes first, through grant), prompts once and returns the session's engine and events. grant
// gets the workspace root and the store before the session starts.
func trustFixture(t *testing.T, st *trust.Store, config string, grant func(root string, st *trust.Store)) (*perms.Engine, []proto.Event) {
	t.Helper()
	f := newFixture(t, goinfertest.Calls("", "d1", "done", `{"reason":"nothing to do"}`), goinfertest.Calls("", "d2", "done", `{"reason":"none"}`))
	eng := &perms.Engine{Sandboxed: true}
	f.core.opt.Edit = func(root string, _ sandbox.Target) (EditKit, error) {
		eng.Root = root
		return EditKit{Shell: noShell{}, Engine: eng}, nil
	}
	f.core.opt.EditTask, f.core.opt.Trust = true, st
	ws := gitWorkspace(t, "Run go test.\n")
	if config != "" {
		os.MkdirAll(filepath.Join(ws, ".tack"), 0o755)
		os.WriteFile(filepath.Join(ws, trust.ConfigPath), []byte(config), 0o644)
	}
	if grant != nil {
		grant(ws, st)
	}
	sid, err := f.sessionIn(t, ws, proto.ModeAuto)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.client.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: sid}, nil); err != nil {
		t.Fatal(err)
	}
	f.prompt(t, sid, "Add a test.")
	return eng, f.waitTurnEnd(t)
}

func grantNow(root string, st *trust.Store) {
	_, digest, _ := trust.ReadRepo(root)
	if err := st.Trust(root, digest, time.Now()); err != nil {
		panic(err)
	}
}

// notices are the instructions-class error events of a turn: the owner's warnings.
func notices(evs []proto.Event) []string {
	var out []string
	for _, ev := range evs {
		if v, _ := ev.Decode(); v != nil {
			if p, ok := v.(proto.ErrorEvent); ok && p.Class == "instructions" {
				out = append(out, p.Message)
			}
		}
	}
	return out
}

func repoTrusted(evs []proto.Event) []proto.RepoTrusted {
	var out []proto.RepoTrusted
	for _, ev := range evs {
		if v, _ := ev.Decode(); v != nil {
			if p, ok := v.(proto.RepoTrusted); ok {
				out = append(out, p)
			}
		}
	}
	return out
}

const repoConfigWithEnv = "[commands]\nenv = [\"MY_FLAG\", \"CI\"]\n"

// TestATrustedRepoConfigIsAppliedAndRecorded: a repository whose .tack/config.toml the owner trusted gives its commands
// the variables the file names, and the session's log says so right after session.created (DESIGN §6.6).
func TestATrustedRepoConfigIsAppliedAndRecorded(t *testing.T) {
	st := trust.Open(t.TempDir())
	eng, evs := trustFixture(t, st, repoConfigWithEnv, grantNow)
	if !slices.Equal(eng.EnvExtra, []string{"MY_FLAG", "CI"}) {
		t.Errorf("the engine's extra variables %v, want the file's", eng.EnvExtra)
	}
	rt := repoTrusted(evs)
	if len(rt) != 1 || !slices.Equal(rt[0].Trusted, []string{"config"}) || len(rt[0].Config) != 64 || rt[0].Workspace == "" {
		t.Errorf("repo.trusted events %+v", rt)
	}
	if evs[0].Type != proto.EvSessionCreated || evs[1].Type != proto.EvRepoTrusted {
		t.Errorf("event order %v; want repo.trusted right after session.created", types(evs[:2]))
	}
	if n := notices(evs); len(n) != 0 {
		t.Errorf("notices %q for a trusted repository", n)
	}
}

// TestAnUntrustedRepoConfigIsNotApplied: content grants nothing. A file in the repository the owner has not trusted adds
// no variable and gets no repo.trusted event, and the owner is told, with the command to run.
func TestAnUntrustedRepoConfigIsNotApplied(t *testing.T) {
	eng, evs := trustFixture(t, trust.Open(t.TempDir()), repoConfigWithEnv, nil)
	if len(eng.EnvExtra) != 0 {
		t.Errorf("an untrusted repository added %v to its commands", eng.EnvExtra)
	}
	if rt := repoTrusted(evs); len(rt) != 0 {
		t.Errorf("repo.trusted for an untrusted repository: %+v", rt)
	}
	if n := notices(evs); len(n) != 1 || !strings.Contains(n[0], "not trusted") || !strings.Contains(n[0], "tack trust ") {
		t.Errorf("notices %q", n)
	}
}

// TestAConfigChangedAfterTheGrantIsNotApplied: trust names the file's content, so a change after it (a pull, a commit, a
// tool) is untrusted again.
func TestAConfigChangedAfterTheGrantIsNotApplied(t *testing.T) {
	eng, evs := trustFixture(t, trust.Open(t.TempDir()), repoConfigWithEnv, func(root string, st *trust.Store) {
		grantNow(root, st)
		os.WriteFile(filepath.Join(root, trust.ConfigPath), []byte("[commands]\nenv = [\"MY_FLAG\", \"CI\", \"EXTRA\"]\n"), 0o644)
	})
	if len(eng.EnvExtra) != 0 {
		t.Errorf("a changed file added %v", eng.EnvExtra)
	}
	if n := notices(evs); len(n) != 1 || !strings.Contains(n[0], "changed since you trusted") {
		t.Errorf("notices %q", n)
	}
}

// TestRepoConfigNeverAddsASecretNamedVariable: a trusted file that names one is not applied at all, and says why; no
// variable of it reaches a command.
func TestRepoConfigNeverAddsASecretNamedVariable(t *testing.T) {
	eng, evs := trustFixture(t, trust.Open(t.TempDir()), "[commands]\nenv = [\"CI\", \"DEPLOY_TOKEN\"]\n", grantNow)
	if len(eng.EnvExtra) != 0 {
		t.Errorf("EnvExtra %v", eng.EnvExtra)
	}
	if n := notices(evs); len(n) != 1 || !strings.Contains(n[0], "DEPLOY_TOKEN") || !strings.Contains(n[0], "secret") {
		t.Errorf("notices %q", n)
	}
	if rt := repoTrusted(evs); len(rt) != 0 {
		t.Errorf("repo.trusted %+v", rt)
	}
}

// TestNoTrustStoreAppliesNoRepoConfig: a core with no trust store never applies, and says nothing, whatever the file holds.
func TestNoTrustStoreAppliesNoRepoConfig(t *testing.T) {
	eng, evs := trustFixture(t, nil, repoConfigWithEnv, nil)
	if len(eng.EnvExtra) != 0 || len(repoTrusted(evs)) != 0 || len(notices(evs)) != 0 {
		t.Errorf("EnvExtra %v, repo.trusted %v, notices %q", eng.EnvExtra, repoTrusted(evs), notices(evs))
	}
}

// TestWorkspaceTrust: the method needs the client token; with it, it records the grant for the file's current content, so a
// session started afterwards applies it; a file that does not parse, or names a secret, is refused and nothing is recorded.
func TestWorkspaceTrust(t *testing.T) {
	f := newFixture(t)
	st := trust.Open(t.TempDir())
	f.core.opt.Trust = st
	ws := gitWorkspace(t, "")
	os.MkdirAll(filepath.Join(ws, ".tack"), 0o755)
	os.WriteFile(filepath.Join(ws, trust.ConfigPath), []byte(repoConfigWithEnv), 0o644)

	var pe *proto.Error
	if err := f.unprivileged(t).Call(proto.MethodWorkspaceTrust, proto.WorkspaceTrustParams{Root: ws}, nil); !errors.As(err, &pe) || pe.Code != proto.CodeUnauthorized {
		t.Fatalf("without the token: %v; want unauthorized", err)
	}
	if _, ok, _ := st.Lookup(ws); ok {
		t.Fatal("an unprivileged connection trusted a repository")
	}
	tui, _, _ := f.privileged(t)
	var res proto.WorkspaceTrustResult
	if err := tui.Call(proto.MethodWorkspaceTrust, proto.WorkspaceTrustParams{Root: ws}, &res); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Trusted, []string{"config"}) || len(res.Config) != 64 || res.Root == "" {
		t.Errorf("result %+v", res)
	}
	if g, ok, err := st.Lookup(ws); err != nil || !ok || g.Config != res.Config {
		t.Errorf("grant %+v %v %v", g, ok, err)
	}
	if err := tui.Call(proto.MethodWorkspaceTrust, proto.WorkspaceTrustParams{Root: filepath.Join(ws, "missing")}, nil); err == nil {
		t.Error("a missing directory was trusted")
	}

	for name, body := range map[string]string{"a typo": "[commands]\nenvs = [\"CI\"]\n", "a secret": "[commands]\nenv = [\"GH_TOKEN\"]\n"} {
		bad := gitWorkspace(t, "")
		os.MkdirAll(filepath.Join(bad, ".tack"), 0o755)
		os.WriteFile(filepath.Join(bad, trust.ConfigPath), []byte(body), 0o644)
		if err := tui.Call(proto.MethodWorkspaceTrust, proto.WorkspaceTrustParams{Root: bad}, nil); !errors.As(err, &pe) || pe.Code != proto.CodeInvalidParams || !strings.Contains(pe.Message, "nothing was trusted") {
			t.Errorf("%s: %v", name, err)
		}
		if _, ok, _ := st.Lookup(bad); ok {
			t.Errorf("%s: recorded a grant for a file that was refused", name)
		}
	}
	// A repository with no file is trusted as having none: the grant covers nothing, and a file that appears is untrusted.
	bare := gitWorkspace(t, "")
	var none proto.WorkspaceTrustResult
	if err := tui.Call(proto.MethodWorkspaceTrust, proto.WorkspaceTrustParams{Root: bare}, &none); err != nil || len(none.Trusted) != 0 || none.Config != "" {
		t.Errorf("no file: %+v, %v", none, err)
	}
}

// TestWorkspaceTrustWithoutAStoreIsRefused: a core that keeps no grants says so instead of pretending.
func TestWorkspaceTrustWithoutAStoreIsRefused(t *testing.T) {
	f := newFixture(t)
	tui, _, _ := f.privileged(t)
	if err := tui.Call(proto.MethodWorkspaceTrust, proto.WorkspaceTrustParams{Root: gitWorkspace(t, "")}, nil); err == nil || !strings.Contains(err.Error(), "no trust grants") {
		t.Errorf("err %v", err)
	}
}
