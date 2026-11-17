package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/townsendmerino/tack/internal/perms"
	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/sandbox"
	"github.com/townsendmerino/tack/internal/trust"
)

// ceilingSession starts a session asking for mode in a repository whose .tack/config.toml is config, trusted or not, and returns
// the mode its session.created records and the error session/new gave.
func ceilingSession(t *testing.T, config string, trusted bool, mode proto.Mode) (proto.Mode, error) {
	t.Helper()
	f := newFixture(t)
	f.core.opt.Edit = func(root string, _ sandbox.Target) (EditKit, error) {
		return EditKit{Shell: noShell{}, Engine: &perms.Engine{Root: root, Sandboxed: true}}, nil
	}
	st := trust.Open(t.TempDir())
	f.core.opt.Trust = st
	ws := gitWorkspace(t, "")
	os.MkdirAll(filepath.Join(ws, ".tack"), 0o755)
	os.WriteFile(filepath.Join(ws, trust.ConfigPath), []byte(config), 0o644)
	if trusted {
		grantNow(ws, st)
	}
	sid, err := f.sessionIn(t, ws, mode)
	if err != nil {
		return "", err
	}
	evs, err := f.core.opt.Store.Events(sid, 0)
	if err != nil {
		t.Fatal(err)
	}
	v, _ := evs[0].Decode()
	return v.(proto.SessionCreated).Mode, nil
}

// TestATrustedRepoCanOnlyLowerTheMode: a trusted repository's max_mode is a ceiling on the mode a session starts in, in the
// strictness order plan, ask, edits, auto. It never raises one: a request below the ceiling stays, and a ceiling of auto
// changes nothing.
func TestATrustedRepoCanOnlyLowerTheMode(t *testing.T) {
	for _, c := range []struct {
		ceiling, asked, want proto.Mode
	}{
		{proto.ModeAsk, proto.ModeAuto, proto.ModeAsk},
		{proto.ModeAsk, proto.ModeEdits, proto.ModeAsk},
		{proto.ModeAsk, proto.ModeAsk, proto.ModeAsk},
		{proto.ModeAsk, proto.ModePlan, proto.ModePlan},
		{proto.ModePlan, proto.ModeAuto, proto.ModePlan},
		{proto.ModeEdits, proto.ModeAuto, proto.ModeEdits},
		{proto.ModeAuto, proto.ModeAuto, proto.ModeAuto},
		{proto.ModeAuto, proto.ModePlan, proto.ModePlan},
	} {
		got, err := ceilingSession(t, "[session]\nmax_mode = \""+string(c.ceiling)+"\"\n", true, c.asked)
		if err != nil || got != c.want {
			t.Errorf("ceiling %s, asked %s: got %q, %v; want %s", c.ceiling, c.asked, got, err, c.want)
		}
	}
}

// TestAnUntrustedRepoSetsNoCeiling: the file is not applied until the owner trusts it, so a session in auto stays in auto.
func TestAnUntrustedRepoSetsNoCeiling(t *testing.T) {
	got, err := ceilingSession(t, "[session]\nmax_mode = \"plan\"\n", false, proto.ModeAuto)
	if err != nil || got != proto.ModeAuto {
		t.Errorf("untrusted ceiling applied: %q, %v", got, err)
	}
}

// TestARepoCannotSetOrRaiseTheMode: a repository has no setting that picks a mode, and a max_mode that is not a mode is an
// error that applies nothing.
func TestARepoCannotSetOrRaiseTheMode(t *testing.T) {
	for _, cfg := range []string{"[session]\nmode = \"auto\"\n", "mode = \"auto\"\n"} {
		_, _, err := readRepoString(t, cfg)
		if err == nil || !strings.Contains(err.Error(), "mode") && !strings.Contains(err.Error(), "unknown setting") {
			t.Errorf("%q: %v", cfg, err)
		}
	}
}

func readRepoString(t *testing.T, cfg string) (trust.Repo, string, error) {
	t.Helper()
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".tack"), 0o755)
	os.WriteFile(filepath.Join(root, trust.ConfigPath), []byte(cfg), 0o644)
	return trust.ReadRepo(root)
}

// TestAMaxModeThatIsNotAModeIsNeverTrustedOrApplied: workspace/trust refuses it and records nothing, and a grant made out of band
// for such a file fails closed: the session starts in plan, not in the mode asked for.
func TestAMaxModeThatIsNotAModeIsNeverTrustedOrApplied(t *testing.T) {
	f := newFixture(t)
	st := trust.Open(t.TempDir())
	f.core.opt.Trust = st
	ws := gitWorkspace(t, "")
	os.MkdirAll(filepath.Join(ws, ".tack"), 0o755)
	os.WriteFile(filepath.Join(ws, trust.ConfigPath), []byte("[session]\nmax_mode = \"yolo\"\n"), 0o644)
	if _, perr := f.core.trustWorkspace(proto.WorkspaceTrustParams{Root: ws}); perr == nil || !strings.Contains(perr.Message, "not a mode") {
		t.Fatalf("trusted a bad max_mode: %v", perr)
	}
	if got, err := ceilingSession(t, "[session]\nmax_mode = \"yolo\"\n", true, proto.ModeEdits); err != nil || got != proto.ModePlan {
		t.Errorf("a bad ceiling granted out of band: %q, %v; want plan", got, err)
	}
}
