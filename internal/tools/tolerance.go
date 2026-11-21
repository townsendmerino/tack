package tools

import (
	"go/parser"
	"go/scanner"
	"go/token"
	"strings"
)

// Edit tolerances (Options.EditTolerances; e1-calibration-preregistration-2026-10-18.md §12.22): when a .go file has no
// exact match for old_string, edit tries, in order, old_string with the whitespace between Go tokens ignored, and then
// old_string with each of TolerancePunct dropped where it ends a line. Both compare Go tokens (go/scanner): every
// literal, identifier and comment must match as written, so a tolerance never changes the text of a string or joins two
// names (DESIGN §7.5, "Whitespace-tolerant matching"). A tolerance applies only when it finds exactly one place, and edit
// applies new_string there only when the file still parses as Go. Fuzzy matching was replayed and dropped: it applies
// wrong edits (e1-calibration-c6-next-direction-proposed-2026-10-27.md).

// TolNoSpace names the whitespace tolerance; a punctuation tolerance is named by TolPunctName. The name is what the
// model reads in edit's result, so it stays as the replay registered it.
const TolNoSpace = "all whitespace ignored"

// TolerancePunct is the punctuation kinds edit drops from line ends, in order: the kinds §12.22's replay kept. `,` `)` `]` `}`
// `.` and `:` were replayed and left out (e1-calibration-c6-tolerances-2026-10-27.md).
var TolerancePunct = []byte{';'}

// TolPunctName names the tolerance that drops k from line ends.
func TolPunctName(k byte) string { return "a trailing " + string(k) + " ignored" }

// ToleranceMatch is where a tolerance placed old_string: [Start, End) of the content, and the tolerance's name; Name is
// empty when none placed it. AmbiguousIn names a tolerance that found more than one place, which stops the search.
type ToleranceMatch struct {
	Name        string
	Start, End  int
	AmbiguousIn string
}

// MatchTolerances tries the whitespace tolerance and then each of kinds for old in content, after an exact match failed.
func MatchTolerances(content, old string, kinds []byte) ToleranceMatch {
	file, toks := goTokens(content), goTokens(old)
	if s, e, n := matchTokens(file, toks); n == 1 {
		return ToleranceMatch{Name: TolNoSpace, Start: s, End: e}
	} else if n > 1 {
		return ToleranceMatch{AmbiguousIn: TolNoSpace}
	}
	for _, k := range kinds {
		dropped, ok := dropLineEnd(old, toks, k)
		if !ok {
			continue
		}
		switch s, e, n := matchTokens(file, dropped); n {
		case 1:
			return ToleranceMatch{Name: TolPunctName(k), Start: s, End: e}
		case 0:
		default:
			return ToleranceMatch{AmbiguousIn: TolPunctName(k)}
		}
	}
	return ToleranceMatch{}
}

// goTok is one Go token of a text: its kind, its source bytes and where they lie, [start, end).
type goTok struct {
	tok        token.Token
	text       string
	start, end int
}

// goTokens is src's Go tokens, comments included, without the semicolons the scanner inserts at line ends, so a line break
// counts as whitespace. A token's text is its source up to the next token with trailing whitespace trimmed, so a literal
// holding a carriage return keeps it. src may be a fragment; a scan error leaves an ILLEGAL token or a short literal,
// which then matches only the same text.
func goTokens(src string) []goTok {
	fset := token.NewFileSet()
	f := fset.AddFile("", -1, len(src))
	var s scanner.Scanner
	s.Init(f, []byte(src), func(token.Position, string) {}, scanner.ScanComments)
	var toks []goTok
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		if tok == token.SEMICOLON && lit == "\n" {
			continue
		}
		toks = append(toks, goTok{tok: tok, start: f.Offset(pos)})
	}
	for i := range toks {
		next := len(src)
		if i+1 < len(toks) {
			next = toks[i+1].start
		}
		t := strings.TrimRight(src[toks[i].start:next], " \t\r\n")
		toks[i].text, toks[i].end = t, toks[i].start+len(t)
	}
	return toks
}

// matchTokens finds old's tokens as a run of file's: the span in the file's source of the one match, and how many there
// are, counting to two.
func matchTokens(file, old []goTok) (int, int, int) {
	if len(old) == 0 {
		return 0, 0, 0
	}
	n, at := 0, 0
	for i := 0; i+len(old) <= len(file); i++ {
		j := 0
		for j < len(old) && file[i+j].tok == old[j].tok && file[i+j].text == old[j].text {
			j++
		}
		if j == len(old) {
			if n++; n == 2 {
				return 0, 0, n
			}
			at = i
		}
	}
	if n == 0 {
		return 0, 0, 0
	}
	return file[at].start, file[at+len(old)-1].end, 1
}

// dropLineEnd is toks, the tokens of old, with each k token that ends a line removed, and whether there was one. Only
// an operator token is dropped: a literal's or comment's text holds more than k.
func dropLineEnd(old string, toks []goTok, k byte) ([]goTok, bool) {
	out := toks[:0:0]
	dropped := false
	for i, t := range toks {
		if t.text == string(k) {
			next := len(old)
			if i+1 < len(toks) {
				next = toks[i+1].start
			}
			if strings.Contains(old[t.end:next], "\n") || i+1 == len(toks) {
				dropped = true
				continue
			}
		}
		out = append(out, t)
	}
	return out, dropped
}

// ParsesAsGo reports whether content parses as a Go file.
func ParsesAsGo(content string) bool {
	_, err := parser.ParseFile(token.NewFileSet(), "x.go", content, parser.SkipObjectResolution)
	return err == nil
}
