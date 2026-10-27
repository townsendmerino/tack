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

// spanSymbols is budgetSymbols whose index places applyTokenBudget, line 3 of mcp/token_budget.go.
type spanSymbols struct{ budgetSymbols }

func (spanSymbols) SymbolAt(_ context.Context, path string, line int) ([]tools.Def, error) {
	if path != "mcp/token_budget.go" || line != 3 {
		return nil, nil
	}
	return []tools.Def{{File: path, Name: "applyTokenBudget", Kind: "func", StartLine: 3, EndLine: 3}}, nil
}

// TestCurrentSymbolNoteThroughTheLoop: through the loop, with the result notes and the repeat rule on, a repeated edit
// whose text is not in the file ends with the current-symbol note, after the repeat note. The repeat is counted on the
// tool's reply without the note: the first call had no note and the second has one, and the second still counts as a
// repeat. StripNotes takes both notes off, which the repeat replay relies on.
func TestCurrentSymbolNoteThroughTheLoop(t *testing.T) {
	const wrong = `{"path":"mcp/token_budget.go","old_string":"func applyTokenBudget() {};","new_string":"func applyTokenBudget() int { return 0 }"}`
	h := permHarness(t, proto.ModeEdits, nil, true,
		goinfertest.Calls("", "r1", "read", `{"path":"mcp/token_budget.go"}`),
		goinfertest.Calls("", "e1", "edit", wrong),
		goinfertest.Calls("", "r2", "read", `{"path":"mcp/token_budget.go"}`),
		goinfertest.Calls("", "e2", "edit", wrong),
		goinfertest.Says("done"))
	cfg := h.sess.cfg
	cfg.Tools = tools.New(tools.Options{Write: true, CurrentSymbol: spanSymbols{}})
	cfg.StopRepeats, cfg.ResultNotes = true, true
	h.sess = New(cfg)
	if _, err := h.sess.Turn(context.Background(), "change it"); err != nil {
		t.Fatal(err)
	}
	reply := `error: this text isn't in "mcp/token_budget.go"; the edit may already have been made. Read the file again ` +
		`before editing: old_string must match it exactly, whitespace included`
	if r := h.finished("e1").Result; r != reply {
		t.Fatalf("first edit: %q", r)
	}
	note := tools.CurrentSymbolOpen + "applyTokenBudget in mcp/token_budget.go, lines 3-3, exactly as the file has it " +
		"now; copy old_string from it:\nfunc applyTokenBudget() {}\n]"
	r := h.finished("e2").Result
	if !strings.HasPrefix(r, reply+noteOpen+"unchanged: this call repeats an earlier one exactly") || !strings.HasSuffix(r, note) {
		t.Fatalf("second edit: %q", r)
	}
	if StripNotes(r) != reply {
		t.Fatalf("StripNotes left %q", StripNotes(r))
	}
	h.checkRebuild(t)
}

// TestCurrentSymbolNoteNeedsRoom: with FitWindow on, the note is added only where the result and the note fit in the
// room the window has left: with none left the second edit's reply comes alone, and with room it carries the note.
func TestCurrentSymbolNoteNeedsRoom(t *testing.T) {
	const wrong = `{"path":"mcp/token_budget.go","old_string":"func applyTokenBudget() {};","new_string":"x"}`
	for _, c := range []struct {
		window int
		note   bool
	}{{fitUsed, false}, {fitUsed + 1000, true}} {
		h := fitHarness(t, c.window,
			goinfertest.Calls("", "r1", "read", `{"path":"mcp/token_budget.go"}`),
			goinfertest.Calls("", "e1", "edit", wrong),
			goinfertest.Calls("", "r2", "read", `{"path":"mcp/token_budget.go"}`),
			goinfertest.Calls("", "e2", "edit", wrong),
			goinfertest.Says("done"))
		cfg := h.sess.cfg
		cfg.Tools = tools.New(tools.Options{Write: true, CurrentSymbol: spanSymbols{}})
		cfg.Mode, cfg.Perms = proto.ModeEdits, &perms.Engine{Root: h.ws.Root(), Path: os.Getenv("PATH")}
		h.sess = New(cfg)
		h.sess.Turn(context.Background(), "change it")
		if r := h.finished("e2").Result; strings.Contains(r, tools.CurrentSymbolOpen) != c.note {
			t.Errorf("window %d: %q", c.window, r)
		}
	}
}
