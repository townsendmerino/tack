package window

import "strings"

// The markdown line classifier, ported from ken's engine/markdown.go (scanMdLines and its helpers, ken v1.12.1), which
// ports aikit/chunk/markdown's. Section boundaries must be ken's, so that a hit ken finds names the section tack loads;
// TestParseSectionsAgreesWithKen holds the two together.

type mdKind int

const (
	mdText mdKind = iota
	mdBlank
	mdHeadingATX
	mdSetextUnderline
	mdCodeFence
	mdCodeInside
	mdFrontmatterDelim
	mdFrontmatterInside
	mdTableSep
	mdTableRow
	mdList
)

// mdLines classifies each line of lines (without their newlines; a trailing "\r" is ignored).
func mdLines(lines []string) []mdKind {
	out := make([]mdKind, len(lines))
	inFence, inFront := false, false
	var fenceChar byte
	fenceLen := 0
	frontDelim := ""
	for i, l := range lines {
		body := strings.TrimSuffix(l, "\r")
		switch {
		case inFront:
			if strings.TrimRight(body, " \t") == frontDelim {
				inFront, out[i] = false, mdFrontmatterDelim
			} else {
				out[i] = mdFrontmatterInside
			}
		case inFence:
			if c, n := mdFence(body); c != 0 && c == fenceChar && n >= fenceLen {
				inFence, out[i] = false, mdCodeFence
			} else {
				out[i] = mdCodeInside
			}
		default:
			if t := strings.TrimRight(body, " \t"); i == 0 && (t == "---" || t == "+++") {
				inFront, frontDelim, out[i] = true, t, mdFrontmatterDelim
				continue
			}
			if c, n := mdFence(body); c != 0 {
				inFence, fenceChar, fenceLen, out[i] = true, c, n, mdCodeFence
				continue
			}
			out[i] = mdContent(body)
		}
	}
	return out
}

// mdFence is a fence line's character and run length, or 0: up to three spaces, then three or more backticks or tildes.
func mdFence(body string) (byte, int) {
	i := 0
	for i < len(body) && i < 3 && body[i] == ' ' {
		i++
	}
	if i >= len(body) || body[i] != '`' && body[i] != '~' {
		return 0, 0
	}
	c, n := body[i], 0
	for i < len(body) && body[i] == c {
		n, i = n+1, i+1
	}
	if n < 3 {
		return 0, 0
	}
	return c, n
}

func mdContent(body string) mdKind {
	t := strings.TrimLeft(body, " \t")
	if t == "" {
		return mdBlank
	}
	if t[0] == '#' {
		n := 0
		for n < len(t) && n < 7 && t[n] == '#' {
			n++
		}
		if n <= 6 && (n == len(t) || t[n] == ' ' || t[n] == '\t') {
			return mdHeadingATX
		}
	}
	if (t[0] == '=' || t[0] == '-') && allSame(t, t[0]) {
		return mdSetextUnderline
	}
	if tableSep(t) {
		return mdTableSep
	}
	if len(t) >= 2 && t[0] == '|' && strings.IndexByte(t[1:], '|') >= 0 {
		return mdTableRow
	}
	if listMarker(t) {
		return mdList
	}
	return mdText
}

func allSame(s string, c byte) bool {
	t := strings.TrimRight(s, " \t")
	if t == "" {
		return false
	}
	for i := 0; i < len(t); i++ {
		if t[i] != c {
			return false
		}
	}
	return true
}

func tableSep(t string) bool {
	if !strings.Contains(t, "-") || !strings.Contains(t, "|") {
		return false
	}
	for i := 0; i < len(t); i++ {
		switch t[i] {
		case '|', '-', ':', ' ', '\t':
		default:
			return false
		}
	}
	return true
}

func listMarker(t string) bool {
	c := t[0]
	if c == '-' || c == '*' || c == '+' {
		return len(t) >= 2 && (t[1] == ' ' || t[1] == '\t')
	}
	if c >= '0' && c <= '9' {
		i := 0
		for i < len(t) && i < 9 && t[i] >= '0' && t[i] <= '9' {
			i++
		}
		if i < len(t) && (t[i] == '.' || t[i] == ')') {
			i++
			return i < len(t) && (t[i] == ' ' || t[i] == '\t')
		}
	}
	return false
}

// mdHeading is an ATX heading line's level and text: the opening hashes and a closing run of hashes (after a space)
// stripped, "" when the line is hashes alone.
func mdHeading(body string) (int, string) {
	t := strings.TrimLeft(strings.TrimSuffix(body, "\r"), " \t")
	lvl := 0
	for lvl < len(t) && t[lvl] == '#' {
		lvl++
	}
	rest := strings.TrimRight(strings.TrimLeft(t[lvl:], " \t"), " \t")
	if j := len(rest); j > 0 {
		k := j
		for k > 0 && rest[k-1] == '#' {
			k--
		}
		if k < j && k > 0 && (rest[k-1] == ' ' || rest[k-1] == '\t') {
			rest = strings.TrimRight(rest[:k], " \t")
		} else if k == 0 {
			rest = ""
		}
	}
	return lvl, rest
}
