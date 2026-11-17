package loop

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
)

// The characters under test are built from their code points, so that this file holds none of them.
var (
	invRLO  = string(rune(0x202E))
	invZWSP = string(rune(0x200B))
	invSHY  = string(rune(0x00AD))
	invBOM  = string(rune(0xFEFF))
	invMan  = string(rune(0x1F468))
	invComp = string(rune(0x1F4BB))
	invZWJ  = string(rune(0x200D))
)

// escapeControl also writes an invisible character as a visible escape (DESIGN §6.6: text that reaches the prompt is
// scanned for them), and leaves alone what text legitimately holds.
func TestEscapeControlEscapesInvisibleCharacters(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"a" + invRLO + "b", `a\u{202E}b`},
		{"x" + invZWSP + "y" + invSHY + "z", `x\u{200B}y\u{00AD}z`},
		{"\x1b[2K" + invRLO, `\x1b[2K\u{202E}`}, // a control character and an invisible one in one string
		{invBOM + "# a file that starts with a byte order mark\n", invBOM + "# a file that starts with a byte order mark\n"},
		{invMan + invZWJ + invComp + " emoji sequence", invMan + invZWJ + invComp + " emoji sequence"},
		{"plain text, café\n", "plain text, café\n"},
	} {
		if got := escapeControl(c.in); got != c.want {
			t.Errorf("escapeControl(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// A tool's result reaches neither the model nor the log with an invisible character in it: a file the model reads can hold
// a bidirectional override as well as an instruction file can.
func TestToolOutputInvisibleIsEscaped(t *testing.T) {
	h := newHarness(t, Budget{}, goinfertest.Calls("", "g1", "grep", `{"pattern":"FIND"}`), goinfertest.Says("ok"))
	writeFile(t, filepath.Join(h.ws.Root(), "out.txt"), "FIND safe"+invRLO+" text"+invZWSP+" here\n")
	if _, err := h.sess.Turn(context.Background(), "find it"); err != nil {
		t.Fatal(err)
	}
	f := h.finished("g1")
	if strings.ContainsAny(f.Result, invRLO+invZWSP) || !strings.Contains(f.Result, `\u{202E}`) || !strings.Contains(f.Result, `\u{200B}`) {
		t.Fatalf("the result holds the raw characters, or does not show them escaped: %.200q", f.Result)
	}
	sent := strings.Join(jsonStrings(t, h.script.Requests[1]), "\n")
	if strings.ContainsAny(sent, invRLO+invZWSP) || !strings.Contains(sent, `\u{202E}`) {
		t.Errorf("the request to the model: %.300q", sent)
	}
	evs, err := h.st.Events("s_test", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range evs {
		if got := strings.Join(jsonStrings(t, ev.Payload), "\n"); strings.ContainsAny(got, invRLO+invZWSP) {
			t.Errorf("the log's %s holds a raw invisible character", ev.Type)
		}
	}
}
