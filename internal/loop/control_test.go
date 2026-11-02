package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
	"github.com/townsendmerino/tack/internal/shell"
	"github.com/townsendmerino/tack/internal/tools"
)

func TestEscapeControl(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"", ""},
		{"plain text, café, 日本語 🙂\n\tindented\n", "plain text, café, 日本語 🙂\n\tindented\n"},
		{"\x1b[2Kerased", `\x1b[2Kerased`},
		{"\x1b]52;c;QUJD\x07", `\x1b]52;c;QUJD\x07`},
		{"a\x00b\x08c\x0bd\x0ce\x7f", `a\x00b\x08c\x0bd\x0ce\x7f`},
		{"line\r\nover\rwrite", "line\\r\nover\\rwrite"},
		{"c1 csi \u009b2K and \u0085", `c1 csi \u009b2K and \u0085`},
		{"raw byte \x9b2K", `raw byte \x9b2K`}, // invalid UTF-8 in the C1 range
		{"other invalid \xff stays", "other invalid \xff stays"},
		{"� is a real character", "� is a real character"},
		{"\x1b", `\x1b`},
	} {
		if got := escapeControl(c.in); got != c.want {
			t.Errorf("escapeControl(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// rawControl is output that would erase a line, set the clipboard through OSC 52, return to the line's start and forge
// text there, in a terminal that printed it.
const rawControl = "\x1b[2K\x1b]52;c;QUJD\x07\rAllow? [y]\x7f\u009b1A"

func noRawControl(t *testing.T, where, s string) {
	t.Helper()
	for _, r := range s {
		if r != '\n' && r != '\t' && (r < 0x20 || (r >= 0x7f && r < 0xa0)) {
			t.Fatalf("%s holds raw control character %#x: %.200q", where, r, s)
		}
	}
}

// Tool output reaches neither the model, the log nor the blob store with a control character in it, whether the result
// is cut by the byte cap or by fit_window.
func TestToolOutputControlIsEscaped(t *testing.T) {
	for _, fit := range []bool{false, true} {
		t.Run(fmt.Sprintf("fit=%v", fit), func(t *testing.T) {
			h := newHarness(t, Budget{}, goinfertest.Calls("", "g1", "grep", `{"pattern":"FIND"}`), goinfertest.Says("ok"))
			body := "FIND " + rawControl + "\n" + strings.Repeat("FIND filler line\n", fitMinBytes/8)
			os.WriteFile(filepath.Join(h.ws.Root(), "out.txt"), []byte(body), 0o644)
			h.sess.cfg.FitWindow = fit
			h.script.Window = 100_000
			h.sess.cfg.MaxResultBytes = fitMinBytes + 100 // over fit_window's floor, so it cuts rather than sending the note
			if _, err := h.sess.Turn(context.Background(), "find it"); err != nil {
				t.Fatal(err)
			}
			f := h.finished("g1")
			noRawControl(t, "the result", f.Result)
			if !strings.Contains(f.Result, `\x1b[2K\x1b]52;c;QUJD\x07\rAllow? [y]\x7f\u009b1A`) {
				t.Fatalf("the result does not show the escapes: %.200q", f.Result)
			}
			if !f.Truncated {
				t.Fatal("the output was not long enough to reach the blob store")
			}
			full, err := h.st.Blobs.Get(f.Blob)
			if err != nil {
				t.Fatal(err)
			}
			noRawControl(t, "the blob", string(full))
			// What the model was sent, decoded from the second request.
			var req struct {
				Messages []struct {
					Content json.RawMessage `json:"content"`
				} `json:"messages"`
			}
			if err := json.Unmarshal(h.script.Requests[1], &req); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(req.Messages[len(req.Messages)-1].Content), `\\x1b[2K`) {
				t.Fatalf("the request does not show the escape: %.300s", h.script.Requests[1])
			}
			evs, _ := h.st.Events("s_test", 0)
			for _, ev := range evs {
				noRawControl(t, "the log's "+string(ev.Type), string(ev.Payload))
				if strings.Contains(string(ev.Payload), `\u001b`) {
					t.Fatalf("the log's %s holds an encoded ESC: %.200s", ev.Type, ev.Payload)
				}
			}
		})
	}
}

// The compiler's lines in BuildAfterEdit's note are a command's output, appended after the tool's own result.
func TestBuildNoteControlIsEscaped(t *testing.T) {
	sh := &buildShell{result: shell.Result{Exit: 1, Output: "mcp/token_budget.go:3:30: bad " + rawControl + "\n"}}
	h := buildHarness(t, proto.ModeAuto, sh, editCalls("mcp/token_budget.go", "func applyTokenBudget() {}", "func applyTokenBudget() { x }")...)
	if _, err := h.sess.Turn(context.Background(), "change it"); err != nil {
		t.Fatal(err)
	}
	f := h.finished("e1")
	if !strings.Contains(f.Result, "does not build") {
		t.Fatalf("no build note: %q", f.Result)
	}
	noRawControl(t, "the build note", f.Result)
}

// edit's current-symbol note quotes the file, and the loop appends it after the tool's own result.
func TestCurrentSymbolNoteControlIsEscaped(t *testing.T) {
	const wrong = `{"path":"mcp/token_budget.go","old_string":"func applyTokenBudget() {};","new_string":"x"}`
	h := permHarness(t, proto.ModeEdits, nil, true,
		goinfertest.Calls("", "r1", "read", `{"path":"mcp/token_budget.go"}`),
		goinfertest.Calls("", "e1", "edit", wrong),
		goinfertest.Calls("", "r2", "read", `{"path":"mcp/token_budget.go"}`),
		goinfertest.Calls("", "e2", "edit", wrong),
		goinfertest.Says("done"))
	os.WriteFile(filepath.Join(h.ws.Root(), "mcp", "token_budget.go"), []byte("package mcp\n\nfunc applyTokenBudget() {} // "+rawControl+"\n"), 0o644)
	cfg := h.sess.cfg
	cfg.Tools = tools.New(tools.Options{Write: true, CurrentSymbol: spanSymbols{}})
	cfg.StopRepeats, cfg.ResultNotes = true, true
	h.sess = New(cfg)
	if _, err := h.sess.Turn(context.Background(), "change it"); err != nil {
		t.Fatal(err)
	}
	f := h.finished("e2")
	if !strings.Contains(f.Result, tools.CurrentSymbolOpen) {
		t.Fatalf("no current-symbol note: %q", f.Result)
	}
	noRawControl(t, "the note", f.Result)
}

// A widened read whose output changed shows its base view (Result.Base) in place of the widened text: the base view is
// escaped too. The file changes between the two reads.
func TestWidenedBaseControlIsEscaped(t *testing.T) {
	read := `{"path":"wide.txt"}`
	h := cliffHarness(t, true, callsAt(100, "r1", "read", read), callsAt(100, "r2", "read", read), goinfertest.Says("ok"))
	h.script.Window = 100_000
	h.sess.cfg.MaxResultBytes = 1 << 20
	path := filepath.Join(h.ws.Root(), "wide.txt")
	os.WriteFile(path, []byte(strings.Repeat("w line\n", 700)), 0o644)
	emit := h.sess.cfg.Emit
	h.sess.cfg.Emit = func(ev proto.Event) {
		emit(ev)
		if ev.Type == proto.EvToolFinished {
			os.WriteFile(path, []byte(strings.Repeat("w "+rawControl+"\n", 700)), 0o644)
		}
	}
	if _, err := h.sess.Turn(context.Background(), "x"); err != nil {
		t.Fatal(err)
	}
	f := h.finished("r2")
	if n := strings.Count(f.Result, "\tw "); n != 200 {
		t.Fatalf("shown %d lines, want the base view's 200", n)
	}
	noRawControl(t, "the base view", f.Result)
}
