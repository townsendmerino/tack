package window

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// The scan of text loaded into the prompt (DESIGN §6.6, "Text loaded into the prompt is scanned first"). It looks for
// two things in an instruction file or a knowledge page: invisible Unicode, which the model reads and the owner does not,
// and a line that tells the model to send files or secrets out. It makes the owner look; it is not a boundary, and a
// text it passes is not thereby safe: the sandbox, which has no network, is what stops data leaving.

// Finding is one thing the scan found: its kind, the 1-based line it is on, and what it is.
type Finding struct {
	Kind   string // FindInvisible or FindExfil
	Line   int
	Detail string // "U+202E", or the line's own text, escaped and cut
}

// The kinds of finding.
const (
	FindInvisible = "invisible"
	FindExfil     = "exfiltration"
)

// invisible reports whether r is a character that renders as nothing, or flips what is shown (the Trojan Source
// overrides), and that text has no reason to hold: zero-width and joining characters, the bidirectional controls,
// the tag characters that hide a message in plain sight, and the hidden fillers. prev and next are its neighbours, for
// the two joiners that emoji and some scripts use between characters of their own.
func invisible(r, prev, next rune) bool {
	switch {
	case r == 0x200C || r == 0x200D: // zero-width non-joiner and joiner: only between two ASCII characters are they out of place
		return prev < utf8.RuneSelf && next < utf8.RuneSelf // the ends of the text count as ASCII
	case r == 0x200B, r == 0x2060, r >= 0x2061 && r <= 0x2064, r == 0x180E, r == 0xFEFF:
		return true
	case r == 0x200E, r == 0x200F, r == 0x061C, r >= 0x202A && r <= 0x202E, r >= 0x2066 && r <= 0x2069:
		return true
	case r >= 0xE0000 && r <= 0xE007F, r >= 0xE0100 && r <= 0xE01EF: // tag characters, variation selectors supplement
		return true
	case r == 0x115F, r == 0x1160, r == 0x3164, r == 0xFFA0: // Hangul fillers
		return true
	}
	return false
}

// exfil is a line that tells the model to send something valuable out: an act of sending, a thing worth stealing, and
// somewhere to send it to, all on the line. A line that says only "never send secrets" has no destination and passes.
var (
	exfilVerb   = regexp.MustCompile(`(?i)\b(send|upload|post|e-?mail|exfiltrate|transmit|leak|forward|curl|wget|scp|rsync|ftp|nc|netcat)\b`)
	exfilObject = regexp.MustCompile(`(?i)(secret|token|credential|password|passwd|api[ _-]?key|private[ _-]?key|ssh[ _-]?key|id_rsa|\.env\b|\.ssh\b|\.aws\b|\.netrc|keychain|environment variables?|env vars?|\.pem\b)`)
	exfilDest   = regexp.MustCompile(`(?i)(https?://|ftp://|\bto (a |an |the |this |that )?(remote|external|outside|attacker|third[- ]party)\b|webhook|pastebin|[\w.+-]+@[\w-]+\.[\w.-]+)`)
)

// ScanPrompt returns what the scan finds in text, in line order. A byte order mark at the very start is not a finding.
func ScanPrompt(text string) []Finding {
	var out []Finding
	line := 1
	var prev rune
	rs := []rune(text)
	for i, r := range rs {
		if r == '\n' {
			line++
		}
		var next rune
		if i+1 < len(rs) {
			next = rs[i+1]
		}
		if !(i == 0 && r == 0xFEFF) && invisible(r, prev, next) {
			out = append(out, Finding{Kind: FindInvisible, Line: line, Detail: fmt.Sprintf("U+%04X", r)})
		}
		prev = r
	}
	for n, l := range strings.Split(text, "\n") {
		if exfilVerb.MatchString(l) && exfilObject.MatchString(l) && exfilDest.MatchString(l) {
			out = append(out, Finding{Kind: FindExfil, Line: n + 1, Detail: cut(EscapeInvisible(strings.TrimSpace(l)), 100)})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Line < out[j].Line }) // invisible characters before a line's own finding
	return out
}

func cut(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// EscapeInvisible returns text with each invisible character (the ones ScanPrompt finds) written as \u{XXXX}, so it shows.
// A byte order mark at the very start stays.
func EscapeInvisible(text string) string {
	rs := []rune(text)
	var b strings.Builder
	var prev rune
	for i, r := range rs {
		var next rune
		if i+1 < len(rs) {
			next = rs[i+1]
		}
		if !(i == 0 && r == 0xFEFF) && invisible(r, prev, next) {
			fmt.Fprintf(&b, `\u{%04X}`, r)
		} else {
			b.WriteRune(r)
		}
		prev = r
	}
	return b.String()
}

// ScanNotice is the warning for the owner about what the scan found in the file called name, or "" for no findings. The
// text it quotes is escaped, so the warning shows what it names.
func ScanNotice(name string, fs []Finding) string {
	if len(fs) == 0 {
		return ""
	}
	var inv, ex []string
	for _, f := range fs {
		switch f.Kind {
		case FindInvisible:
			inv = append(inv, fmt.Sprintf("%s at line %d", f.Detail, f.Line))
		case FindExfil:
			ex = append(ex, fmt.Sprintf("line %d: %q", f.Line, f.Detail))
		}
	}
	var parts []string
	if len(inv) > 0 {
		parts = append(parts, fmt.Sprintf("%d invisible character(s) (%s), which the model is shown escaped as \\u{XXXX}", len(inv), strings.Join(inv, ", ")))
	}
	if len(ex) > 0 {
		parts = append(parts, "text that tells the model to send files or secrets out ("+strings.Join(ex, "; ")+"), which the model is still shown")
	}
	return name + " holds " + strings.Join(parts, " and ") + ". Look at it before you rely on this session."
}
