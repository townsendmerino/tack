package core

import (
	"fmt"
	"os"
	"time"

	"github.com/townsendmerino/tack/internal/perms"
	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/trust"
)

// repoConfig is what a session starting in root takes from the repository's own configuration (DESIGN §6.6): the extra
// environment variable names its commands are given and the event that says the trust is in effect, or a notice for the
// owner about a file that is not applied. A repository the owner has not trusted, or whose file changed since he did,
// gives only the notice; no Trust store gives nothing.
func (c *Core) repoConfig(root string) (env []string, ev *proto.RepoTrusted, notice string) {
	if c.opt.Trust == nil {
		return nil, nil, ""
	}
	st := c.opt.Trust.Check(root)
	if st.Notice != "" || !st.Applied {
		return nil, nil, st.Notice
	}
	if err := perms.CheckExtraEnv(st.Repo.Commands.Env); err != nil {
		return nil, nil, trust.ConfigPath + " is not applied: " + err.Error()
	}
	if err := checkRepoMode(st.Repo); err != nil {
		return nil, nil, trust.ConfigPath + " is not applied: " + err.Error()
	}
	return st.Repo.Commands.Env, &proto.RepoTrusted{Workspace: root, Trusted: []string{"config"}, Config: st.Digest}, ""
}

// clampMode is mode as the repository at root allows it: the stricter of mode and the ceiling in its .tack/config.toml, when
// the owner has trusted that file as it stands. An untrusted or changed file, and a daemon with no trust store, give the
// mode back unchanged. A repository can only lower a mode.
func (c *Core) clampMode(root string, mode proto.Mode) proto.Mode {
	if c.opt.Trust == nil {
		return mode
	}
	st := c.opt.Trust.Check(root)
	if !st.Applied || st.Repo.Session.MaxMode == "" {
		return mode
	}
	ceiling := proto.Mode(st.Repo.Session.MaxMode)
	if !ceiling.Valid() { // trusted out of band: fail closed rather than ignore a ceiling the owner may have meant
		ceiling = proto.ModePlan
	}
	return proto.Stricter(mode, ceiling)
}

// checkRepoMode refuses a repository configuration whose max_mode is not a mode.
func checkRepoMode(repo trust.Repo) error {
	if m := repo.Session.MaxMode; m != "" && !proto.Mode(m).Valid() {
		return fmt.Errorf("%s: [session] max_mode: %q is not a mode (plan, ask, edits or auto)", trust.ConfigPath, m)
	}
	return nil
}

// trustWorkspace is workspace/trust: it records the owner's trust in the repository at p.Root, as its configuration is now.
// A file that does not parse or names what no repository may add is refused and nothing is recorded.
func (c *Core) trustWorkspace(p proto.WorkspaceTrustParams) (any, *proto.Error) {
	if c.opt.Trust == nil {
		return nil, &proto.Error{Code: proto.CodeInvalidParams, Message: "workspace/trust: this daemon keeps no trust grants"}
	}
	root, err := trust.Canonical(p.Root)
	if err != nil {
		return nil, &proto.Error{Code: proto.CodeInvalidParams, Message: "workspace/trust: " + err.Error()}
	}
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		return nil, &proto.Error{Code: proto.CodeInvalidParams, Message: "workspace/trust: " + p.Root + " is not a directory"}
	}
	repo, digest, err := trust.ReadRepo(root)
	if err == nil {
		err = perms.CheckExtraEnv(repo.Commands.Env)
	}
	if err == nil {
		err = checkRepoMode(repo)
	}
	if err != nil {
		return nil, &proto.Error{Code: proto.CodeInvalidParams, Message: "workspace/trust: " + err.Error() + "; nothing was trusted"}
	}
	if err := c.opt.Trust.Trust(root, digest, time.Now()); err != nil {
		return nil, &proto.Error{Code: proto.CodeInternalError, Message: "workspace/trust: " + err.Error()}
	}
	res := proto.WorkspaceTrustResult{Root: root, Trusted: []string{}, Config: digest}
	if digest != "" {
		res.Trusted = []string{"config"}
	}
	return res, nil
}
