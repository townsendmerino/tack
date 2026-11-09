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
	"github.com/townsendmerino/tack/internal/sandbox"
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
			if _, _, err := s.Restore(context.Background(), cps[0].Snapshot, false); err != nil {
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
	if _, _, err := s.Restore(context.Background(), cps[0].Snapshot, false); err != nil {
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
func (c *countingSnapshots) Staged(context.Context) bool { return false }

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
	ids    []string
	after  func()
	staged []bool // each Staged's answer, in turn; false after the last
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
	if id == "" {
		return "", "the working tree is over the cap"
	}
	return id, ""
}
func (c *scriptedSnapshots) Prune(context.Context) error { return nil }
func (c *scriptedSnapshots) Staged(context.Context) bool {
	if len(c.staged) == 0 {
		return false
	}
	s := c.staged[0]
	c.staged = c.staged[1:]
	return s
}

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
		staged   []bool
		yes      []bool
		asks     int
		ran      bool
		snapshot string
	}{
		{"both taken", []string{"a", "b"}, nil, []bool{true}, 1, true, "b"},
		{"lost with something staged, then a no", []string{"a"}, []bool{true}, []bool{true, false}, 2, false, ""},
		{"staged while it waited, then a no", []string{"a", "b"}, []bool{false, true}, []bool{true, false}, 2, false, ""},
		{"lost, then a no", []string{"a"}, nil, []bool{true, false}, 2, false, ""},
		{"lost, then a yes", []string{"a"}, nil, []bool{true, true}, 2, true, ""},
		{"lost, then a yes, and back while it waited", []string{"a", "", "c"}, nil, []bool{true, true}, 2, true, "c"},
	} {
		t.Run(c.name, func(t *testing.T) {
			ap := &answers{yes: c.yes}
			h, _ := checkpointHarness(t, ap, false, bashCall("b1", command), goinfertest.Says("done"))
			sh := &buildShell{}
			cfg := h.sess.cfg
			cfg.Tools, cfg.Shell = tools.New(tools.Options{Write: true, Shell: sh}), sh
			cfg.Snapshots = &scriptedSnapshots{ids: c.ids, staged: c.staged}
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
			if c.asks == 2 && !strings.Contains(strings.Join(ap.asked[1].Reasons, " "), "snapshot") {
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
	for name, command := range map[string]string{"recording the checkpoint": "echo hi", "recording the question": "git branch -D x"} {
		t.Run(name, func(t *testing.T) {
			h, _ := checkpointHarness(t, &approver{answer: Approval{Allow: true}}, false, bashCall("b1", command), goinfertest.Says("done"))
			cfg := h.sess.cfg
			cfg.Snapshots = &scriptedSnapshots{ids: []string{"a"}, staged: []bool{true}, after: func() { h.log.Close() }}
			h.sess = New(cfg)
			if _, err := h.sess.Turn(context.Background(), "go"); err == nil {
				t.Fatal("the turn went on with its log closed")
			}
			if cfg.Perms.Snapshotted || cfg.Perms.Staged {
				t.Error("the engine still says there is a snapshot")
			}
		})
	}
}

// A lost snapshot asks again only if the snapshot was answering part of the question.
func TestASnapshotLostOverNothingDoesntAskAgain(t *testing.T) {
	ap := &answers{yes: []bool{true}}
	h, _ := checkpointHarness(t, ap, false, bashCall("b1", "git branch -D x"), goinfertest.Says("done"))
	sh := &buildShell{}
	cfg := h.sess.cfg
	cfg.Tools, cfg.Shell = tools.New(tools.Options{Write: true, Shell: sh}), sh
	cfg.Snapshots = &scriptedSnapshots{ids: []string{"a"}}
	h.sess = New(cfg)
	if _, err := h.sess.Turn(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	if len(ap.asked) != 1 || len(sh.ran) != 1 {
		t.Errorf("asked %d, ran %q", len(ap.asked), sh.ran)
	}
}

// Through the loop, on a real repository: with something staged, git reset --hard asks, and it leaves the staged work alone when
// told no; with nothing staged it runs; and the flag doesn't outlive its command.
func TestStagedWorkKeepsAsking(t *testing.T) {
	gitIn := func(t *testing.T, root string, args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = root
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	ap := &approver{answer: Approval{}}
	h, _ := checkpointHarness(t, ap, true, bashCall("b1", "git reset --hard"), goinfertest.Says("done"))
	root := h.ws.Root()
	file := filepath.Join(root, "mcp", "token_budget.go")
	if err := os.WriteFile(file, []byte("package mcp // staged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, root, "add", "mcp/token_budget.go")
	if err := os.WriteFile(file, []byte("package mcp // edited further\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := h.sess.Turn(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	if len(ap.asked) != 1 {
		t.Fatalf("asked %+v", ap.asked)
	}
	if !strings.Contains(strings.Join(ap.asked[0].Reasons, " "), "what's staged") {
		t.Errorf("reasons %q", ap.asked[0].Reasons)
	}
	if b, _ := os.ReadFile(file); string(b) != "package mcp // edited further\n" {
		t.Errorf("it ran: %q", b)
	}
	if h.sess.cfg.Perms.Staged {
		t.Error("the engine still says something is staged")
	}

	ap = &approver{answer: Approval{}}
	h, _ = checkpointHarness(t, ap, true, bashCall("b1", "git reset --hard"), goinfertest.Says("done"))
	root = h.ws.Root()
	file = filepath.Join(root, "mcp", "token_budget.go")
	if err := os.WriteFile(file, []byte("package mcp // not staged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := h.sess.Turn(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	if len(ap.asked) != 0 {
		t.Errorf("nothing staged, and it asked: %+v", ap.asked)
	}
	if b, _ := os.ReadFile(file); string(b) == "package mcp // not staged\n" {
		t.Error("it didn't run")
	}
}

// realShellHarness is a real git workspace on the real shell child, with its run-time check, in auto mode with a pretend sandbox,
// and with the snapshots and answers given.
func realShellHarness(t *testing.T, snaps Snapshotter, ap Approver, replies ...goinfertest.Reply) *harness {
	t.Helper()
	t.Setenv("TACK_SHELL_CHILD", "1")
	h, _ := checkpointHarness(t, ap, false, replies...)
	cfg := h.sess.cfg
	sh := &shell.Session{Exe: os.Args[0], Engine: cfg.Perms, Group: sandbox.Target{Mark: "s-test", Since: time.Now()},
		Wrap:  func(c *exec.Cmd, _, _ string) *exec.Cmd { return c },
		Sweep: func(sandbox.Target) (sandbox.Report, error) { return sandbox.Report{}, nil }}
	cfg.Tools, cfg.Shell, cfg.Snapshots = tools.New(tools.Options{Write: true, Shell: sh}), nil, snaps
	h.sess = New(cfg)
	return h
}

// The whole way, with the real run-time check: a clean snapshot, then something staged while the first question waited, then the
// snapshot lost while the second did. Each change asks again, the last question holds all of it, and a no stops all of it.
func TestThreeSnapshotsWithTheRealShell(t *testing.T) {
	for _, c := range []struct {
		name string
		yes  []bool
		ran  bool
	}{
		{"three yeses", []bool{true, true, true}, true},
		{"a no at the third", []bool{true, true, false}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			ap := &answers{yes: c.yes}
			snaps := &scriptedSnapshots{ids: []string{"a", "b"}, staged: []bool{false, true}}
			h := realShellHarness(t, snaps, ap, bashCall("b1", "git branch -D x; git reset --hard; rm -r mcp"), goinfertest.Says("done"))
			root := h.ws.Root()
			gitBranch := exec.Command("git", "branch", "x")
			gitBranch.Dir = root
			if out, err := gitBranch.CombinedOutput(); err != nil {
				t.Fatalf("git branch: %v %s", err, out)
			}
			if _, err := h.sess.Turn(context.Background(), "go"); err != nil {
				t.Fatal(err)
			}
			if len(ap.asked) != 3 {
				t.Fatalf("asked %d times: %+v", len(ap.asked), ap.asked)
			}
			first, second, third := strings.Join(ap.asked[0].Reasons, " "), strings.Join(ap.asked[1].Reasons, " "), strings.Join(ap.asked[2].Reasons, " ")
			if strings.Contains(first, "reset --hard discards") || strings.Contains(first, "rm -r mcp deletes") {
				t.Errorf("the first question held what the snapshot answered: %q", first)
			}
			if !strings.Contains(second, "what's staged") || strings.Contains(second, "rm -r mcp deletes") {
				t.Errorf("the second question: %q", second)
			}
			if !strings.Contains(third, "without a snapshot and a clean index") || !strings.Contains(third, "rm -r mcp deletes") {
				t.Errorf("the third question: %q", third)
			}
			_, err := os.Stat(filepath.Join(root, "mcp"))
			if gone := err != nil; gone != c.ran {
				t.Errorf("mcp gone %v, want %v; finished %+v", gone, c.ran, h.finished("b1"))
			}
			if c.ran && h.finished("b1").Outcome != proto.OutcomeOK {
				t.Errorf("the real check stopped it: %+v", h.finished("b1"))
			}
			if !c.ran && h.finished("b1").Outcome != proto.OutcomeDenied {
				t.Errorf("finished %+v", h.finished("b1"))
			}
		})
	}
}

// What the tree does during the question counts against what was asked before it: a directory turned into a link out of the workspace
// while the owner reads is a new reason, not one the yes covered.
func TestATreeChangedDuringTheQuestionAsksAgain(t *testing.T) {
	var root string
	ap := &answers{yes: []bool{true, false}}
	replaced := false
	approve := approverFunc(func(req proto.PermissionRequested) (Approval, error) {
		if !replaced {
			replaced = true
			if err := os.RemoveAll(filepath.Join(root, "mcp")); err != nil {
				t.Error(err)
			}
			if err := os.Symlink("/usr/share", filepath.Join(root, "mcp")); err != nil {
				t.Error(err)
			}
		}
		return ap.Approve(context.Background(), req)
	})
	h, _ := checkpointHarness(t, approve, false, bashCall("b1", "git branch -D x; rm -r mcp"), goinfertest.Says("done"))
	root = h.ws.Root()
	sh := &buildShell{}
	cfg := h.sess.cfg
	cfg.Tools, cfg.Shell = tools.New(tools.Options{Write: true, Shell: sh}), sh
	cfg.Snapshots = &scriptedSnapshots{ids: []string{"a", "b"}}
	h.sess = New(cfg)
	if _, err := h.sess.Turn(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	if len(ap.asked) != 2 {
		t.Fatalf("asked %d times: %+v", len(ap.asked), ap.asked)
	}
	if !strings.Contains(strings.Join(ap.asked[1].Reasons, " "), "rm -r mcp deletes") {
		t.Errorf("the second question: %q", ap.asked[1].Reasons)
	}
	if len(sh.ran) != 0 {
		t.Errorf("it ran: %q", sh.ran)
	}
}

type approverFunc func(proto.PermissionRequested) (Approval, error)

func (f approverFunc) Approve(_ context.Context, req proto.PermissionRequested) (Approval, error) {
	return f(req)
}

// So does a reason that doesn't ask in every mode: a redirection that leads out of the workspace by the time the yes comes.
func TestAWriteOutOfTheWorkspaceDuringTheQuestionAsksAgain(t *testing.T) {
	var root string
	ap := &answers{yes: []bool{true, false}}
	replaced := false
	approve := approverFunc(func(req proto.PermissionRequested) (Approval, error) {
		if !replaced {
			replaced = true
			if err := os.RemoveAll(filepath.Join(root, "mcp")); err != nil {
				t.Error(err)
			}
			if err := os.Symlink("/usr/share", filepath.Join(root, "mcp")); err != nil {
				t.Error(err)
			}
		}
		return ap.Approve(context.Background(), req)
	})
	h, _ := checkpointHarness(t, approve, false, bashCall("b1", "git branch -D x; echo hi > mcp/out.txt"), goinfertest.Says("done"))
	root = h.ws.Root()
	sh := &buildShell{}
	cfg := h.sess.cfg
	cfg.Tools, cfg.Shell = tools.New(tools.Options{Write: true, Shell: sh}), sh
	cfg.Snapshots = &scriptedSnapshots{ids: []string{"a", "b"}}
	h.sess = New(cfg)
	if _, err := h.sess.Turn(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	if len(ap.asked) != 2 {
		t.Fatalf("asked %d times: %+v", len(ap.asked), ap.asked)
	}
	if len(sh.ran) != 0 {
		t.Errorf("it ran: %q", sh.ran)
	}
}

// TestTheSnapshotResetsBeforeTurnEnd: a turn's snapshot flags are cleared before its turn.end is recorded, since the
// session may start its next turn as turn.end goes out (Config.EndTurn) and nothing of this turn may write them after.
func TestTheSnapshotResetsBeforeTurnEnd(t *testing.T) {
	h, _ := checkpointHarness(t, &approver{answer: Approval{Allow: true}}, true, goinfertest.Says("done"))
	cfg := h.sess.cfg
	var atEnd []bool
	ended := 0
	cfg.EndTurn = func(record func() error) error {
		atEnd = []bool{cfg.Perms.Snapshotted, cfg.Perms.Staged}
		err := record()
		ended++
		return err
	}
	h.sess = New(cfg)
	cfg.Perms.Snapshotted, cfg.Perms.Staged = true, true // as a snapshot the turn took would leave them
	if _, err := h.sess.Turn(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	if ended != 1 || len(atEnd) != 2 || atEnd[0] || atEnd[1] {
		t.Errorf("EndTurn called %d times; snapshotted, staged as turn.end was recorded: %v, want both false", ended, atEnd)
	}
}
