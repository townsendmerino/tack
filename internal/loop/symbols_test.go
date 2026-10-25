package loop

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/townsendmerino/tack/internal/perms"
	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
	"github.com/townsendmerino/tack/internal/tools"
)

// budgetSymbols is a symbol index that knows one definition: applyTokenBudget, line 3 of mcp/token_budget.go in the
// harness's workspace.
type budgetSymbols struct{}

func (budgetSymbols) Definition(_ context.Context, name string) ([]tools.Def, error) {
	if name != "applyTokenBudget" {
		return nil, nil
	}
	return []tools.Def{{File: "mcp/token_budget.go", Name: name, Kind: "func", StartLine: 3, EndLine: 3}}, nil
}
func (budgetSymbols) Outline(context.Context, string) ([]tools.Def, error)       { return nil, nil }
func (budgetSymbols) Callers(context.Context, string) ([]tools.Site, error)      { return nil, nil }
func (budgetSymbols) Callees(context.Context, string) ([]tools.Site, error)      { return nil, nil }
func (budgetSymbols) TestsFor(context.Context, string) ([]tools.TestSite, error) { return nil, nil }
func (budgetSymbols) SymbolAt(context.Context, string, int) ([]tools.Def, error) { return nil, nil }
func (budgetSymbols) Sync(context.Context, []string) (bool, error)               { return false, nil }

// symbolThenEdit is a model that shows the function with symbol and then replaces it by name.
func symbolThenEdit() []goinfertest.Reply {
	return []goinfertest.Reply{
		goinfertest.Calls("", "s1", "symbol", `{"name":"applyTokenBudget"}`),
		goinfertest.Calls("", "e1", "edit", `{"path":"mcp/token_budget.go","symbol":"applyTokenBudget","new_string":"func applyTokenBudget() int { return 0 }"}`),
		goinfertest.Says("done"),
	}
}

func symbolHarness(t *testing.T, mode proto.Mode) *harness {
	t.Helper()
	h := permHarness(t, mode, nil, true, symbolThenEdit()...)
	cfg := h.sess.cfg
	cfg.Tools = tools.New(tools.Options{Write: true, Symbols: budgetSymbols{}})
	h.sess = New(cfg)
	return h
}

// TestEditBySymbolTakesTheEditPermissionPath: an edit by symbol is an edit to the permission engine, in every mode: it
// asks in ask mode (refused here, with no one attached), is refused in plan mode, and runs in edits mode. symbol itself
// is read-only and records no decision.
func TestEditBySymbolTakesTheEditPermissionPath(t *testing.T) {
	const before, after = "package mcp\n\nfunc applyTokenBudget() {}\n", "package mcp\n\nfunc applyTokenBudget() int { return 0 }\n"
	for _, tc := range []struct {
		mode    proto.Mode
		asked   bool
		outcome string
		file    string
	}{
		{proto.ModeAsk, true, proto.OutcomeDenied, before},
		{proto.ModePlan, false, proto.OutcomeDenied, before},
		{proto.ModeEdits, false, proto.OutcomeOK, after},
	} {
		h := symbolHarness(t, tc.mode)
		h.sess.Turn(context.Background(), "change it")
		if f := h.finished("s1"); f.Outcome != proto.OutcomeOK || !strings.Contains(f.Result, "func applyTokenBudget() {}") {
			t.Fatalf("%s: symbol finished %+v", tc.mode, f)
		}
		req := h.payloads(proto.EvPermissionRequested)
		if tc.asked != (len(req) == 1) || tc.asked && req[0].(proto.PermissionRequested).Tool != "edit" {
			t.Fatalf("%s: requests %+v", tc.mode, req)
		}
		if res := h.payloads(proto.EvPermissionResolved); len(res) != 1 {
			t.Fatalf("%s: %d decisions recorded, want one, for the edit alone", tc.mode, len(res))
		}
		if f := h.finished("e1"); f.Outcome != tc.outcome {
			t.Fatalf("%s: edit finished %+v", tc.mode, f)
		}
		if got := h.file(t); got != tc.file {
			t.Fatalf("%s: file =\n%s", tc.mode, got)
		}
	}
	// The engine decides an edit by its tool name and mode; the input's form is not consulted.
	e := &perms.Engine{Root: os.TempDir()}
	byString := e.Decide(proto.ModeAsk, "edit", false, []byte(`{"path":"a","old_string":"x","new_string":"y"}`))
	bySymbol := e.Decide(proto.ModeAsk, "edit", false, []byte(`{"path":"a","symbol":"F","new_string":"y"}`))
	if byString.Action != bySymbol.Action || byString.By != bySymbol.By {
		t.Fatalf("edit by string %+v, by symbol %+v", byString, bySymbol)
	}
}
