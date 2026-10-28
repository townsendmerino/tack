package tools

import (
	"go/parser"
	"go/token"
	"regexp"
	"strings"
)

// Edit tolerances (Options.EditTolerances; e1-calibration-preregistration-2026-10-18.md §12.22): when a .go file has no
// exact match for old_string, edit tries, in order, old_string with all whitespace removed, and then old_string with each
// of TolerancePunct dropped where it ends a line, its whitespace runs flexible. A tolerance applies only when it finds
// exactly one place, and edit applies new_string there only when the file still parses as Go. Fuzzy matching was
// replayed and dropped: it applies wrong edits (e1-calibration-c6-next-direction-proposed-2026-10-27.md).

// TolNoSpace names the whitespace tolerance; a punctuation tolerance is named by TolPunctName.
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
	if s, e, n := matchNoSpace(content, old); n == 1 {
		return ToleranceMatch{Name: TolNoSpace, Start: s, End: e}
	} else if n > 1 {
		return ToleranceMatch{AmbiguousIn: TolNoSpace}
	}
	for _, k := range kinds {
		re := regexp.MustCompile(regexp.QuoteMeta(string(k)) + `[ \t]*(\n|$)`)
		dropped := re.ReplaceAllString(old, "$1")
		if dropped == old || strings.TrimSpace(dropped) == "" {
			continue
		}
		locs := flexibleSpace(dropped).FindAllStringIndex(content, 2)
		switch len(locs) {
		case 1:
			return ToleranceMatch{Name: TolPunctName(k), Start: locs[0][0], End: locs[0][1]}
		case 2:
			return ToleranceMatch{AmbiguousIn: TolPunctName(k)}
		}
	}
	return ToleranceMatch{}
}

// matchNoSpace finds old in content with all whitespace removed from both: the span in content of the one match, and how
// many there are.
func matchNoSpace(content, old string) (int, int, int) {
	o := strings.Join(strings.Fields(old), "")
	if o == "" {
		return 0, 0, 0
	}
	var sq strings.Builder
	var at []int
	for i := 0; i < len(content); i++ {
		if c := content[i]; c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			sq.WriteByte(c)
			at = append(at, i)
		}
	}
	s := sq.String()
	n := strings.Count(s, o)
	if n != 1 {
		return 0, 0, n
	}
	k := strings.Index(s, o)
	return at[k], at[k+len(o)-1] + 1, 1
}

// flexibleSpace matches text's words in order, separated by any run of whitespace where text has one.
func flexibleSpace(text string) *regexp.Regexp {
	fields := strings.Fields(text)
	for i, f := range fields {
		fields[i] = regexp.QuoteMeta(f)
	}
	return regexp.MustCompile(strings.Join(fields, `\s+`))
}

// ParsesAsGo reports whether content parses as a Go file.
func ParsesAsGo(content string) bool {
	_, err := parser.ParseFile(token.NewFileSet(), "x.go", content, parser.SkipObjectResolution)
	return err == nil
}
