package loop

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/townsendmerino/tack/internal/perms"
	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
	"github.com/townsendmerino/tack/internal/tools"
)

type approver struct {
	answer Approval
	asked  []proto.PermissionRequested
}

func (a *approver) Approve(_ context.Context, req proto.PermissionRequested) (Approval, error) {
	a.asked = append(a.asked, req)
	return a.answer, nil
}

// permHarness is newHarness with the write tools, a permission engine in mode, and an approver (nil: no one attached).
func permHarness(t *testing.T, mode proto.Mode, ap Approver, withEngine bool, replies ...goinfertest.Reply) *harness {
	t.Helper()
	h := newHarness(t, Budget{}, replies...)
	cfg := h.sess.cfg
	cfg.Tools = tools.New(tools.Options{Write: true})
	cfg.Mode, cfg.Approve = mode, ap
	if withEngine {
		cfg.Perms = &perms.Engine{Root: h.ws.Root(), Path: os.Getenv("PATH")}
	}
	h.sess = New(cfg)
	return h
}

// readThenEdit is a model that reads the file and then edits it.
func readThenEdit() []goinfertest.Reply {
	return []goinfertest.Reply{
		goinfertest.Calls("", "r1", "read", `{"path":"mcp/token_budget.go"}`),
		goinfertest.Calls("", "e1", "edit", `{"path":"mcp/token_budget.go","old_string":"func applyTokenBudget() {}","new_string":"func applyTokenBudget() int { return 0 }"}`),
		goinfertest.Says("done"),
	}
}

func (h *harness) file(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(h.ws.Root(), "mcp", "token_budget.go"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func (h *harness) finished(id string) proto.ToolFinished {
	for _, p := range h.payloads(proto.EvToolFinished) {
		if f := p.(proto.ToolFinished); f.ID == id {
			return f
		}
	}
	return proto.ToolFinished{}
}

// TestHeadlessAskRefusesWithGuidance: in ask mode with no one attached, the edit is requested, refused as no_user,
// answered with a denied result that says what to do, and the file is untouched. The read records no decision.
func TestHeadlessAskRefusesWithGuidance(t *testing.T) {
	h := permHarness(t, proto.ModeAsk, nil, true, readThenEdit()...)
	h.sess.Turn(context.Background(), "change it")
	if req := h.payloads(proto.EvPermissionRequested); len(req) != 1 || req[0].(proto.PermissionRequested).Tool != "edit" {
		t.Fatalf("requests %+v", req)
	}
	res := h.payloads(proto.EvPermissionResolved)
	if len(res) != 1 || res[0].(proto.PermissionResolved).By != proto.DecidedByNoUser || res[0].(proto.PermissionResolved).Decision != proto.DecisionDeny {
		t.Fatalf("resolutions %+v", res)
	}
	if f := h.finished("e1"); f.Outcome != proto.OutcomeDenied || !strings.Contains(f.Result, "no one is attached") {
		t.Fatalf("edit result %+v", f)
	}
	if strings.Contains(h.file(t), "return 0") {
		t.Fatal("the refused edit changed the file")
	}
	h.checkRebuild(t)
}

// TestUserApproves: the approver sees the request, allows it once, and the edit runs after its decision is recorded.
func TestUserApproves(t *testing.T) {
	ap := &approver{answer: Approval{Allow: true}}
	h := permHarness(t, proto.ModeAsk, ap, true, readThenEdit()...)
	h.sess.Turn(context.Background(), "change it")
	if len(ap.asked) != 1 || ap.asked[0].Tool != "edit" || ap.asked[0].Mode != proto.ModeAsk {
		t.Fatalf("asked %+v", ap.asked)
	}
	if f := h.finished("e1"); f.Outcome != proto.OutcomeOK || !strings.Contains(h.file(t), "return 0") {
		t.Fatalf("approved edit %+v; file %q", f, h.file(t))
	}
	// The decision is recorded before the tool starts.
	got := strings.Join(h.types(), " ")
	if !strings.Contains(got, "permission.requested permission.resolved tool.started") {
		t.Fatalf("events in order: %s", got)
	}
}

// TestModesThroughTheLoop: edits mode allows the edit without asking; plan mode refuses it; a session whose tools can
// write but has no engine refuses it rather than run it unchecked.
func TestModesThroughTheLoop(t *testing.T) {
	h := permHarness(t, proto.ModeEdits, nil, true, readThenEdit()...)
	h.sess.Turn(context.Background(), "change it")
	if len(h.payloads(proto.EvPermissionRequested)) != 0 || h.finished("e1").Outcome != proto.OutcomeOK {
		t.Fatalf("edits mode: requests %v, edit %+v", h.payloads(proto.EvPermissionRequested), h.finished("e1"))
	}
	if r := h.payloads(proto.EvPermissionResolved); len(r) != 1 || r[0].(proto.PermissionResolved).By != proto.DecidedByMode {
		t.Fatalf("edits mode resolution %+v", r)
	}

	h = permHarness(t, proto.ModePlan, &approver{answer: Approval{Allow: true}}, true, readThenEdit()...)
	h.sess.Turn(context.Background(), "change it")
	if f := h.finished("e1"); f.Outcome != proto.OutcomeDenied || !strings.Contains(f.Result, "plan mode") {
		t.Fatalf("plan mode edit %+v", f)
	}

	h = permHarness(t, proto.ModeAuto, nil, false, readThenEdit()...)
	h.sess.Turn(context.Background(), "change it")
	if f := h.finished("e1"); f.Outcome != proto.OutcomeDenied || !strings.Contains(f.Result, "no permission engine") ||
		strings.Contains(h.file(t), "return 0") {
		t.Fatalf("no engine: edit %+v", f)
	}
}

// TestSessionApprovalSkipsTheNextQuestion: allowing edit for the session asks once and records what was added.
func TestSessionApprovalSkipsTheNextQuestion(t *testing.T) {
	ap := &approver{answer: Approval{Allow: true, Session: true}}
	replies := append(readThenEdit()[:2],
		goinfertest.Calls("", "e2", "edit", `{"path":"mcp/token_budget.go","old_string":"return 0","new_string":"return 1"}`),
		goinfertest.Says("done"))
	h := permHarness(t, proto.ModeAsk, ap, true, replies...)
	h.sess.Turn(context.Background(), "change it twice")
	if len(ap.asked) != 1 || h.finished("e2").Outcome != proto.OutcomeOK || !strings.Contains(h.file(t), "return 1") {
		t.Fatalf("asked %d times; second edit %+v", len(ap.asked), h.finished("e2"))
	}
	r := h.payloads(proto.EvPermissionResolved)
	if first := r[0].(proto.PermissionResolved); first.Scope != "session" || first.RuleAdded != "tool edit" {
		t.Fatalf("first resolution %+v", first)
	}
	if second := r[1].(proto.PermissionResolved); second.By != proto.DecidedByUser || second.Decision != proto.DecisionAllow {
		t.Fatalf("second resolution %+v", second)
	}
}
