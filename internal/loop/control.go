package loop

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// escapeControl replaces the control characters in a tool's output with a visible escape, so that output can't move a
// terminal's cursor, rewrite earlier lines, forge a prompt or set the clipboard (DESIGN §9.3). Newline and tab pass.
//   - C0 and DEL become \xNN, except carriage return, which becomes \r: CRLF files have one on every line.
//   - C1 becomes \uNNNN, and a lone invalid byte in the C1 range becomes \xNN.
//   - Other invalid bytes pass: they control nothing, and their encoding on the way to a client is not this pass's.
//
// The escape is one-way: text that already spells \x1b looks the same. A string with nothing to escape returns as is.
func escapeControl(s string) string {
	var b strings.Builder
	copied := 0 // s[:copied] is in b or replaced
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		esc := ""
		switch {
		case r == '\n' || r == '\t':
		case r == '\r':
			esc = `\r`
		case r < 0x20 || r == 0x7f, r == utf8.RuneError && n == 1 && s[i] >= 0x80 && s[i] < 0xa0:
			esc = fmt.Sprintf(`\x%02x`, s[i])
		case unicode.IsControl(r):
			esc = fmt.Sprintf(`\u%04x`, r)
		}
		if esc != "" {
			b.WriteString(s[copied:i])
			b.WriteString(esc)
			copied = i + n
		}
		i += n
	}
	if copied == 0 {
		return s
	}
	b.WriteString(s[copied:])
	return b.String()
}
