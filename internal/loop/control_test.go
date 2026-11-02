package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

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
		{"line\r\nover\rwrite", "line\r\nover\\rwrite"}, // a CRLF line ending stays, and a CR anywhere else can overwrite
		{"a\r\r\nb", "a\\r\r\nb"},
		{"ends\r", `ends\r`},
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
// text there, in a terminal that printed it. It ends in a C1 control both ways: a character, and a lone byte.
const rawControl = "\x1b[2K\x1b]52;c;QUJD\x07\rAllow? [y]\x7f\u009b1A\x9b"

// rawControlEscaped is rawControl as it must be shown.
const rawControlEscaped = `\x1b[2K\x1b]52;c;QUJD\x07\rAllow? [y]\x7f\u009b1A\x9b`

// noRawControl fails on a control character in s, but for newline, tab and the CR of a CRLF. It decodes rather than
// scanning bytes, since 0x80-0x9f are also the continuation bytes of valid text: a lone one is what's invalid.
func noRawControl(t *testing.T, where, s string) {
	t.Helper()
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && n == 1 && s[i] >= 0x80 && s[i] < 0xa0:
			t.Fatalf("%s holds raw C1 byte %#x at %d: %.200q", where, s[i], i, s)
		case r == '\r' && i+1 < len(s) && s[i+1] == '\n':
		case r != '\n' && r != '\t' && (r < 0x20 || (r >= 0x7f && r < 0xa0)):
			t.Fatalf("%s holds raw control character %#x at %d: %.200q", where, r, i, s)
		}
		i += n
	}
}

// jsonStrings is every string in a JSON document, decoded.
func jsonStrings(t *testing.T, doc []byte) []string {
	t.Helper()
	var v any
	if err := json.Unmarshal(doc, &v); err != nil {
		t.Fatal(err)
	}
	var out []string
	var walk func(any)
	walk = func(v any) {
		switch v := v.(type) {
		case string:
			out = append(out, v)
		case []any:
			for _, e := range v {
				walk(e)
			}
		case map[string]any:
			for k, e := range v {
				out = append(out, k)
				walk(e)
			}
		}
	}
	walk(v)
	return out
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Tool output reaches neither the model, the log nor the blob store with a control character in it, whether the result
// is cut by the byte cap or by fit_window.
func TestToolOutputControlIsEscaped(t *testing.T) {
	for _, fit := range []bool{false, true} {
		t.Run(fmt.Sprintf("fit=%v", fit), func(t *testing.T) {
			h := newHarness(t, Budget{}, goinfertest.Calls("", "g1", "grep", `{"pattern":"FIND"}`), goinfertest.Says("ok"))
			body := "FIND " + rawControl + "\n" + strings.Repeat("FIND filler line\n", fitMinBytes/8)
			writeFile(t, filepath.Join(h.ws.Root(), "out.txt"), body)
			h.sess.cfg.FitWindow = fit
			h.script.Window = 100_000
			h.sess.cfg.MaxResultBytes = fitMinBytes + 100 // over fit_window's floor, so it cuts rather than sending the note
			if _, err := h.sess.Turn(context.Background(), "find it"); err != nil {
				t.Fatal(err)
			}
			f := h.finished("g1")
			noRawControl(t, "the result", f.Result)
			if !strings.Contains(f.Result, rawControlEscaped) {
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
			// What the model was sent and what the log holds, decoded: JSON spells a control character out, so its text hides one.
			sent := jsonStrings(t, h.script.Requests[1])
			noRawControl(t, "the request", strings.Join(sent, "\n"))
			if !strings.Contains(strings.Join(sent, "\n"), rawControlEscaped) {
				t.Fatalf("the request does not show the escapes: %.300s", h.script.Requests[1])
			}
			evs, err := h.st.Events("s_test", 0)
			if err != nil {
				t.Fatal(err)
			}
			if len(evs) == 0 {
				t.Fatal("the log holds no events")
			}
			for _, ev := range evs {
				noRawControl(t, "the log's "+string(ev.Type), strings.Join(jsonStrings(t, ev.Payload), "\n"))
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
	if !strings.Contains(f.Result, rawControlEscaped) {
		t.Fatalf("the build note does not show the escapes: %q", f.Result)
	}
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
	writeFile(t, filepath.Join(h.ws.Root(), "mcp", "token_budget.go"), "package mcp\n\nfunc applyTokenBudget() {} // "+rawControl+"\n")
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
	if !strings.Contains(f.Result, rawControlEscaped) {
		t.Fatalf("the note does not show the escapes: %q", f.Result)
	}
}

// A widened read whose output changed shows its base view (Result.Base) in place of the widened text: the base view is
// escaped too. The file changes between the two reads.
func TestWidenedBaseControlIsEscaped(t *testing.T) {
	read := `{"path":"wide.txt"}`
	h := cliffHarness(t, true, callsAt(100, "r1", "read", read), callsAt(100, "r2", "read", read), goinfertest.Says("ok"))
	h.script.Window = 100_000
	h.sess.cfg.MaxResultBytes = 1 << 20
	path := filepath.Join(h.ws.Root(), "wide.txt")
	writeFile(t, path, strings.Repeat("w line\n", 700))
	emit := h.sess.cfg.Emit
	h.sess.cfg.Emit = func(ev proto.Event) {
		emit(ev)
		if ev.Type == proto.EvToolFinished {
			writeFile(t, path, strings.Repeat("w "+rawControl+"\n", 700))
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
	if !strings.Contains(f.Result, rawControlEscaped) {
		t.Fatalf("the base view does not show the escapes: %.200q", f.Result)
	}
}

// A build note's last line is followed by the note's own text, so a CR there is not a line ending.
func TestBuildNoteLineEndingIsEscaped(t *testing.T) {
	sh := &buildShell{result: shell.Result{Exit: 1, Output: "mcp/token_budget.go:3:30: bad\r\n"}}
	h := buildHarness(t, proto.ModeAuto, sh, editCalls("mcp/token_budget.go", "func applyTokenBudget() {}", "func applyTokenBudget() { x }")...)
	if _, err := h.sess.Turn(context.Background(), "change it"); err != nil {
		t.Fatal(err)
	}
	f := h.finished("e1")
	if !strings.Contains(f.Result, "does not build") {
		t.Fatalf("no build note: %q", f.Result)
	}
	noRawControl(t, "the build note", f.Result)
	if !strings.Contains(f.Result, `bad\r`) {
		t.Fatalf("the build note does not show the escape: %q", f.Result)
	}
}

// crlfFile is mcp/token_budget.go with CRLF line endings.
const crlfFile = "package mcp\r\n\r\nfunc applyTokenBudget() {}\r\nfunc other() {}\r\n"

// A CRLF file stays editable: lines a model copies out of a read match the file as old_string, and written back as
// new_string they keep its line endings, with no literal backslash-r in it.
func TestCRLFFileRoundTripsReadToEdit(t *testing.T) {
	read := goinfertest.Calls("", "r1", "read", `{"path":"mcp/token_budget.go"}`)
	setup := func(replies ...goinfertest.Reply) *harness {
		h := permHarness(t, proto.ModeEdits, nil, true, replies...)
		writeFile(t, filepath.Join(h.ws.Root(), "mcp", "token_budget.go"), crlfFile)
		if _, err := h.sess.Turn(context.Background(), "change it"); err != nil {
			t.Fatal(err)
		}
		return h
	}
	// What the read shows, without its line numbers.
	var lines []string
	for _, l := range strings.SplitAfter(setup(read, goinfertest.Says("ok")).finished("r1").Result, "\n") {
		if _, text, ok := strings.Cut(l, "\t"); ok {
			lines = append(lines, text)
		}
	}
	if len(lines) != 4 {
		t.Fatalf("read shows %d lines: %q", len(lines), lines)
	}
	old := strings.TrimSuffix(lines[2]+lines[3], "\r\n")
	input, err := json.Marshal(map[string]string{"path": "mcp/token_budget.go", "old_string": old,
		"new_string": strings.ReplaceAll(old, "other", "renamed")})
	if err != nil {
		t.Fatal(err)
	}
	h := setup(read, goinfertest.Calls("", "e1", "edit", string(input)), goinfertest.Says("ok"))
	if f := h.finished("e1"); f.Outcome != proto.OutcomeOK || strings.HasPrefix(f.Result, "error") {
		t.Fatalf("the edit of copied lines failed: %+v", f)
	}
	got, err := os.ReadFile(filepath.Join(h.ws.Root(), "mcp", "token_budget.go"))
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.ReplaceAll(crlfFile, "other", "renamed"); string(got) != want {
		t.Fatalf("the file is %q, want %q", got, want)
	}
}

// edit's current-symbol note says it quotes the file exactly, so it shows a CRLF as it is.
func TestCurrentSymbolNoteKeepsCRLF(t *testing.T) {
	const wrong = `{"path":"mcp/token_budget.go","old_string":"func applyTokenBudget() {};","new_string":"x"}`
	h := permHarness(t, proto.ModeEdits, nil, true,
		goinfertest.Calls("", "r1", "read", `{"path":"mcp/token_budget.go"}`),
		goinfertest.Calls("", "e1", "edit", wrong),
		goinfertest.Calls("", "r2", "read", `{"path":"mcp/token_budget.go"}`),
		goinfertest.Calls("", "e2", "edit", wrong),
		goinfertest.Says("done"))
	writeFile(t, filepath.Join(h.ws.Root(), "mcp", "token_budget.go"), crlfFile)
	cfg := h.sess.cfg
	cfg.Tools = tools.New(tools.Options{Write: true, CurrentSymbol: spanSymbols{}})
	cfg.StopRepeats, cfg.ResultNotes = true, true
	h.sess = New(cfg)
	if _, err := h.sess.Turn(context.Background(), "change it"); err != nil {
		t.Fatal(err)
	}
	f := h.finished("e2")
	if !strings.Contains(f.Result, tools.CurrentSymbolOpen) || !strings.Contains(f.Result, "func applyTokenBudget() {}\r\n") {
		t.Fatalf("the note does not quote the file: %q", f.Result)
	}
}

// A cut at any byte leaves no raw control character: not the CR of a CRLF it divides, and not a lone byte of a character
// it divides, whose continuation bytes fall in the C1 range. Every offset, through both caps.
func TestCutsLeaveNoRawControl(t *testing.T) {
	body := strings.Repeat("FIND a\r\nFIND …日本語🙂\r\n", 200)
	for _, fit := range []bool{false, true} {
		for size := fitMinBytes; size < fitMinBytes+64; size++ {
			h := newHarness(t, Budget{}, goinfertest.Calls("", "g1", "read", `{"path":"out.txt"}`), goinfertest.Says("ok"))
			writeFile(t, filepath.Join(h.ws.Root(), "out.txt"), body)
			h.sess.cfg.FitWindow = fit
			h.script.Window = 100_000
			h.sess.cfg.MaxResultBytes = size
			if _, err := h.sess.Turn(context.Background(), "read it"); err != nil {
				t.Fatal(err)
			}
			evs, err := h.st.Events("s_test", 0)
			if err != nil {
				t.Fatal(err)
			}
			if f := h.finished("g1"); !f.Truncated {
				t.Fatalf("fit=%v size=%d: not cut", fit, size)
			}
			for _, ev := range evs {
				noRawControl(t, fmt.Sprintf("fit=%v size=%d", fit, size), strings.Join(jsonStrings(t, ev.Payload), "\n"))
				if strings.Contains(string(ev.Payload), `\ufffd`) || strings.ContainsRune(string(ev.Payload), utf8.RuneError) {
					t.Fatalf("fit=%v size=%d: a cut divided a character: %.200s", fit, size, ev.Payload)
				}
			}
		}
	}
}
