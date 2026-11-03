package loop

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/perms"
	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
	"github.com/townsendmerino/tack/internal/shell"
	"github.com/townsendmerino/tack/internal/snapshot"
	"github.com/townsendmerino/tack/internal/tools"
)

// realShell really runs the command in root, so the destruction in these tests happens.
type realShell struct{ root string }

func (r *realShell) Run(ctx context.Context, command string, _ time.Duration) (shell.Result, error) {
	c := exec.CommandContext(ctx, "sh", "-c", command)
	c.Dir = r.root
	out, err := c.CombinedOutput()
	code := 0
	if err != nil {
		code = 1
	}
	return shell.Result{Exit: code, Output: string(out)}, nil
}

// checkpointHarness is a real git workspace with a real shadow, in auto mode, with ap to answer questions.
func checkpointHarness(t *testing.T, ap Approver, withSnapshots bool, replies ...goinfertest.Reply) (*harness, *snapshot.Shadow) {
	t.Helper()
	h := newHarness(t, Budget{}, replies...)
	root := h.ws.Root()
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "one"}} {
		c := exec.Command("git", args...)
		c.Dir = root
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	cfg := h.sess.cfg
	sh := &realShell{root: root}
	cfg.Tools = tools.New(tools.Options{Write: true, Shell: sh})
	cfg.Shell = sh
	cfg.Mode, cfg.Approve = proto.ModeAuto, ap
	cfg.Perms = &perms.Engine{Root: root, Path: os.Getenv("PATH"), Sandboxed: true}
	var s *snapshot.Shadow
	if withSnapshots {
		s = snapshot.New(t.TempDir(), root, 1<<30)
		cfg.Snapshots = s
	}
	h.sess = New(cfg)
	return h, s
}

func tree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Name() == ".git" {
			return filepath.SkipDir
		}
		if d.Type().IsRegular() {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			out[p] = string(b)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func (h *harness) checkpoints() []proto.Checkpoint {
	var out []proto.Checkpoint
	for _, p := range h.payloads(proto.EvCheckpoint) {
		out = append(out, p.(proto.Checkpoint))
	}
	return out
}

// Through the loop: git reset --hard over uncommitted work runs without a question, and the checkpoint brings the work back.
func TestDestroyWithASnapshotRunsAndRewinds(t *testing.T) {
	for _, command := range []string{"git reset --hard", "rm -r mcp", "git clean -fdx"} {
		t.Run(command, func(t *testing.T) {
			ap := &approver{answer: Approval{}}
			h, s := checkpointHarness(t, ap, true, bashCall("b1", command), goinfertest.Says("done"))
			root := h.ws.Root()
			if err := os.WriteFile(filepath.Join(root, "mcp", "token_budget.go"), []byte("package mcp // a day's work\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "untracked.txt"), []byte("new\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			before := tree(t, root)
			if _, err := h.sess.Turn(context.Background(), "go"); err != nil {
				t.Fatal(err)
			}
			if len(ap.asked) != 0 {
				t.Errorf("it asked: %+v", ap.asked)
			}
			if h.finished("b1").Outcome != proto.OutcomeOK {
				t.Errorf("finished: %+v", h.finished("b1"))
			}
			if got := tree(t, root); len(got) == len(before) && got[filepath.Join(root, "untracked.txt")] != "" && got[filepath.Join(root, "mcp", "token_budget.go")] == before[filepath.Join(root, "mcp", "token_budget.go")] {
				t.Error("the command destroyed nothing")
			}
			cps := h.checkpoints()
			if len(cps) != 1 || cps[0].ID != "b1" || cps[0].Workspace != root || cps[0].Snapshot == "" || cps[0].Unavailable != "" {
				t.Fatalf("checkpoints %+v", cps)
			}
			if _, err := s.Restore(context.Background(), cps[0].Snapshot); err != nil {
				t.Fatal(err)
			}
			got := tree(t, root)
			for p, want := range before {
				if got[p] != want {
					t.Errorf("%s: %q, want %q", p, got[p], want)
				}
			}
		})
	}
}

// The checkpoint is on the log before the command starts, so a command that kills tack still has one.
func TestCheckpointComesBeforeTheCommand(t *testing.T) {
	h, _ := checkpointHarness(t, nil, true, bashCall("b1", "echo hi"), goinfertest.Says("done"))
	if _, err := h.sess.Turn(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	var order []proto.EventType
	for _, ev := range h.events {
		if ev.Type == proto.EvCheckpoint || ev.Type == proto.EvToolStarted || ev.Type == proto.EvToolCall {
			order = append(order, ev.Type)
		}
	}
	if len(order) != 3 || order[0] != proto.EvToolCall || order[1] != proto.EvCheckpoint || order[2] != proto.EvToolStarted {
		t.Errorf("order %v", order)
	}
}

// No snapshot, whatever the reason: the guard goes on asking, and the event says why.
func TestNoSnapshotKeepsAsking(t *testing.T) {
	ap := &approver{answer: Approval{}}
	h, s := checkpointHarness(t, ap, true, bashCall("b1", "git reset --hard"), goinfertest.Says("done"))
	s.MaxBytes = 1
	if _, err := h.sess.Turn(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	if len(ap.asked) != 1 || !strings.Contains(strings.Join(ap.asked[0].Reasons, " "), "without a snapshot") {
		t.Errorf("asked %+v", ap.asked)
	}
	// Refused, so it never ran: no checkpoint on the log.
	if cps := h.checkpoints(); len(cps) != 0 {
		t.Errorf("checkpoints %+v", cps)
	}

	h, s = checkpointHarness(t, nil, true, bashCall("b1", "echo hi"), goinfertest.Says("done"))
	s.MaxBytes = 1
	if _, err := h.sess.Turn(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	cps := h.checkpoints()
	if len(cps) != 1 || cps[0].Snapshot != "" || !strings.Contains(cps[0].Unavailable, "cap") {
		t.Errorf("checkpoints %+v", cps)
	}
}

// What a snapshot can't undo asks even with one, and the flag doesn't outlive its command.
func TestSnapshotOnlyAnswersItsOwnCommand(t *testing.T) {
	ap := &approver{answer: Approval{}}
	h, _ := checkpointHarness(t, ap, true, bashCall("b1", "git branch -D x"), bashCall("b2", "git reset --hard"), goinfertest.Says("done"))
	if _, err := h.sess.Turn(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	if len(ap.asked) != 1 || ap.asked[0].ID != "b1" {
		t.Errorf("asked %+v", ap.asked)
	}
	if h.sess.cfg.Perms.Snapshotted {
		t.Error("the engine still says there is a snapshot")
	}
	// A refused command ends it too.
	h, _ = checkpointHarness(t, ap, true, bashCall("b1", "git branch -D x"), goinfertest.Says("done"))
	if _, err := h.sess.Turn(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	if h.sess.cfg.Perms.Snapshotted {
		t.Error("after a refusal, the engine still says there is a snapshot")
	}
}

// When the owner is asked, files can change while the question waits: the checkpoint is of the tree the command ran on.
func TestAnApprovalTakesTheSnapshotAgain(t *testing.T) {
	var root string
	ap := approveFunc(func(proto.PermissionRequested) {
		if err := os.WriteFile(filepath.Join(root, "typed-during-the-question.txt"), []byte("mine\n"), 0o644); err != nil {
			t.Error(err)
		}
	})
	h, s := checkpointHarness(t, ap, true, bashCall("b1", "git branch -D x; rm typed-during-the-question.txt"), goinfertest.Says("done"))
	root = h.ws.Root()
	if _, err := h.sess.Turn(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	cps := h.checkpoints()
	if len(cps) != 1 {
		t.Fatalf("checkpoints %+v", cps)
	}
	if _, err := os.Stat(filepath.Join(root, "typed-during-the-question.txt")); err == nil {
		t.Fatal("the command didn't run")
	}
	if _, err := s.Restore(context.Background(), cps[0].Snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "typed-during-the-question.txt")); err != nil {
		t.Errorf("the file typed during the question isn't in the checkpoint: %v", err)
	}
}

type approveFunc func(proto.PermissionRequested)

func (f approveFunc) Approve(_ context.Context, req proto.PermissionRequested) (Approval, error) {
	f(req)
	return Approval{Allow: true}, nil
}

// Without the setting, nothing changes: no event, no git, and the guard asks.
func TestSnapshotsOff(t *testing.T) {
	ap := &approver{answer: Approval{}}
	h, _ := checkpointHarness(t, ap, false, bashCall("b1", "git reset --hard"), goinfertest.Says("done"))
	if _, err := h.sess.Turn(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	if len(ap.asked) != 1 {
		t.Errorf("asked %+v", ap.asked)
	}
	if cps := h.checkpoints(); len(cps) != 0 {
		t.Errorf("checkpoints %+v", cps)
	}
}

// Only bash takes a snapshot: an edit has its own record of what it changed.
func TestOnlyBashTakesASnapshot(t *testing.T) {
	counted := &countingSnapshots{}
	h, _ := checkpointHarness(t, nil, false, readCall("r", "mcp/token_budget.go"), editCall("e", "mcp/token_budget.go", "package mcp", "package mcp // x"), goinfertest.Says("done"))
	cfg := h.sess.cfg
	cfg.Snapshots = counted
	h.sess = New(cfg)
	if _, err := h.sess.Turn(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	if counted.takes != 0 {
		t.Errorf("%d snapshots", counted.takes)
	}
}

// Old checkpoints are pruned once a session, before its first snapshot.
func TestPruneOncePerSession(t *testing.T) {
	counted := &countingSnapshots{}
	h, _ := checkpointHarness(t, nil, false, bashCall("b1", "echo 1"), bashCall("b2", "echo 2"), goinfertest.Says("done"))
	cfg := h.sess.cfg
	cfg.Snapshots = counted
	h.sess = New(cfg)
	if _, err := h.sess.Turn(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	if counted.prunes != 1 || counted.takes != 2 {
		t.Errorf("%d prunes, %d takes", counted.prunes, counted.takes)
	}
}

type countingSnapshots struct{ takes, prunes int }

func (c *countingSnapshots) Take(context.Context) (string, string) {
	c.takes++
	return "id", ""
}
func (c *countingSnapshots) Prune(context.Context) error {
	c.prunes++
	return nil
}

// Plan mode refuses bash, so it takes no snapshot on the way.
func TestPlanModeTakesNoSnapshot(t *testing.T) {
	counted := &countingSnapshots{}
	h, _ := checkpointHarness(t, nil, false, bashCall("b1", "echo 1"), goinfertest.Says("done"))
	cfg := h.sess.cfg
	cfg.Snapshots, cfg.Mode = counted, proto.ModePlan
	h.sess = New(cfg)
	if _, err := h.sess.Turn(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	if counted.takes != 0 {
		t.Errorf("%d snapshots", counted.takes)
	}
}

// scriptedSnapshots answers each Take from a list, and after the last says there is none. after runs inside each Take.
type scriptedSnapshots struct {
	ids   []string
	after func()
}

func (c *scriptedSnapshots) Take(context.Context) (string, string) {
	if c.after != nil {
		c.after()
	}
	if len(c.ids) == 0 {
		return "", "the working tree is over the cap"
	}
	id := c.ids[0]
	c.ids = c.ids[1:]
	return id, ""
}
func (c *scriptedSnapshots) Prune(context.Context) error { return nil }

// answers says yes, then no.
type answers struct {
	asked []proto.PermissionRequested
	yes   []bool
}

func (a *answers) Approve(_ context.Context, req proto.PermissionRequested) (Approval, error) {
	a.asked = append(a.asked, req)
	allow := a.yes[0]
	a.yes = a.yes[1:]
	return Approval{Allow: allow}, nil
}

// A yes was given to a command whose snapshot answered part of the question. If the snapshot taken after that yes fails, the
// question was the wrong one: it's asked again, with what the snapshot no longer answers.
func TestASnapshotLostAfterApprovalAsksAgain(t *testing.T) {
	command := "git branch -D x; git reset --hard"
	for _, c := range []struct {
		name     string
		ids      []string
		yes      []bool
		asks     int
		ran      bool
		snapshot string
	}{
		{"both taken", []string{"a", "b"}, []bool{true}, 1, true, "b"},
		{"lost, then a no", []string{"a"}, []bool{true, false}, 2, false, ""},
		{"lost, then a yes", []string{"a"}, []bool{true, true}, 2, true, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			ap := &answers{yes: c.yes}
			h, _ := checkpointHarness(t, ap, false, bashCall("b1", command), goinfertest.Says("done"))
			sh := &buildShell{}
			cfg := h.sess.cfg
			cfg.Tools, cfg.Shell = tools.New(tools.Options{Write: true, Shell: sh}), sh
			cfg.Snapshots = &scriptedSnapshots{ids: c.ids}
			h.sess = New(cfg)
			if _, err := h.sess.Turn(context.Background(), "go"); err != nil {
				t.Fatal(err)
			}
			if len(ap.asked) != c.asks {
				t.Fatalf("asked %d times: %+v", len(ap.asked), ap.asked)
			}
			if strings.Contains(strings.Join(ap.asked[0].Reasons, " "), "without a snapshot") {
				t.Errorf("the first question held what the snapshot answers: %q", ap.asked[0].Reasons)
			}
			if c.asks == 2 && !strings.Contains(strings.Join(ap.asked[1].Reasons, " "), "without a snapshot") {
				t.Errorf("the second question lacks what the snapshot no longer answers: %q", ap.asked[1].Reasons)
			}
			if (len(sh.ran) == 1) != c.ran {
				t.Errorf("ran %q", sh.ran)
			}
			cps := h.checkpoints()
			if !c.ran {
				if len(cps) != 0 {
					t.Errorf("a refused command has a checkpoint: %+v", cps)
				}
			} else if len(cps) != 1 || cps[0].Snapshot != c.snapshot {
				t.Errorf("checkpoints %+v", cps)
			}
			if h.sess.cfg.Perms.Snapshotted {
				t.Error("the engine still says there is a snapshot")
			}
		})
	}
}

// A turn that ends on an error leaves no snapshot behind on the engine: one that fails while permit records, and one while the
// checkpoint is recorded.
func TestAnErrorLeavesNoSnapshot(t *testing.T) {
	h, _ := checkpointHarness(t, nil, false, bashCall("b1", "echo hi"), goinfertest.Says("done"))
	cfg := h.sess.cfg
	cfg.Snapshots = &scriptedSnapshots{ids: []string{"a"}, after: func() { h.log.Close() }}
	h.sess = New(cfg)
	if _, err := h.sess.Turn(context.Background(), "go"); err == nil {
		t.Fatal("the turn went on with its log closed")
	}
	if cfg.Perms.Snapshotted {
		t.Error("the engine still says there is a snapshot")
	}
}
