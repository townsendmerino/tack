package window

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"

	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/tools"
)

// Instruction files by section (DESIGN §2.3, §6.7). A file within the budget is loaded whole. Above it, the first
// message gets the file's first section, every section marked <!-- tack:always -->, and an outline of the other
// headings with their lines; the other sections arrive when the first tool call of a matching kind runs (ForCall), and
// only if they are not in the window already. Every load counts against one budget, cumulative until the next
// compaction; a compaction that stubs a result carrying sections returns their share (Masked).

// SectionShare is the share of the served window the instruction file's sections may take, in percent, cumulative until
// the next compaction (DESIGN §2.3, *proposed* 15%).
const SectionShare = 15

// Section is one heading-bounded part of an instruction file, in the shape of ken's engine.Section (K9): it runs from
// its heading line to the line before the next heading, and the text before the first heading is a level-0 section
// with no heading path. Lines are 1-based and inclusive.
type Section struct {
	HeadingPath []string
	Level       int
	StartLine   int
	EndLine     int
	Text        string
	// Always is set by a <!-- tack:always --> marker in the section: it is loaded with the first message.
	Always bool
	// On is the section's <!-- tack:on KIND ... --> kinds, such as "bash:git", "bash:go test" or "edit:*_test.go". A
	// section with any is matched by them alone, not by its heading.
	On []string
	// body is set when the section has text beyond its heading line, a setext underline, frontmatter, blank lines
	// and markers.
	body bool
}

// Heading is the section's own heading, "" for the preamble.
func (s Section) Heading() string {
	if len(s.HeadingPath) == 0 {
		return ""
	}
	return s.HeadingPath[len(s.HeadingPath)-1]
}

// marker is a tack marker comment.
var marker = regexp.MustCompile(`<!--\s*tack:(always|on)\b([^>]*?)-->`)

// stripCode is line without its code spans, CommonMark's way: a run of n backticks opens a span that the next run of
// exactly n closes; a run with no such closer is literal.
func stripCode(line string) string {
	var b strings.Builder
	for i := 0; i < len(line); {
		if line[i] != '`' {
			b.WriteByte(line[i])
			i++
			continue
		}
		n := 0
		for i+n < len(line) && line[i+n] == '`' {
			n++
		}
		closeAt := -1
		for j := i + n; j < len(line); {
			if line[j] != '`' {
				j++
				continue
			}
			m := 0
			for j+m < len(line) && line[j+m] == '`' {
				m++
			}
			if m == n {
				closeAt = j + m
				break
			}
			j += m
		}
		if closeAt < 0 {
			b.WriteString(line[i : i+n])
			i += n
			continue
		}
		i = closeAt
	}
	return b.String()
}

// ParseSections splits content into its sections in file order, where ken's Workspace.Sections does
// (TestParseSectionsAgreesWithKen): at ATX headings, and at setext headings (a text line over a line of = or -), but not
// inside code fences or frontmatter. A section's markers are read from its text outside code: fenced blocks,
// frontmatter and inline code spans can quote a marker without setting it.
func ParseSections(content string) []Section {
	if content == "" {
		return nil
	}
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	kinds := mdLines(lines)
	var out []Section
	var stack []struct {
		level int
		text  string
	}
	for i := range lines {
		level, text := 0, ""
		switch {
		case i == 0 && kinds[0] != mdHeadingATX && !(kinds[0] == mdText && len(lines) > 1 && kinds[1] == mdSetextUnderline):
			out = append(out, Section{StartLine: 1})
			continue
		case kinds[i] == mdHeadingATX:
			level, text = mdHeading(lines[i])
		case kinds[i] == mdText && i+1 < len(lines) && kinds[i+1] == mdSetextUnderline:
			level, text = 2, strings.TrimSpace(strings.TrimSuffix(lines[i], "\r"))
			if strings.HasPrefix(strings.TrimLeft(lines[i+1], " \t"), "=") {
				level = 1
			}
		default:
			continue
		}
		for len(stack) > 0 && stack[len(stack)-1].level >= level {
			stack = stack[:len(stack)-1]
		}
		stack = append(stack, struct {
			level int
			text  string
		}{level, text})
		path := make([]string, len(stack))
		for j, s := range stack {
			path[j] = s.text
		}
		if n := len(out); n > 0 {
			out[n-1].EndLine = i
		}
		out = append(out, Section{HeadingPath: path, Level: level, StartLine: i + 1})
	}
	out[len(out)-1].EndLine = len(lines)
	for i := range out {
		s := &out[i]
		s.Text = strings.Join(lines[s.StartLine-1:s.EndLine], "\n")
		var prose []string
		for j := s.StartLine - 1; j < s.EndLine; j++ {
			switch kinds[j] {
			case mdFrontmatterDelim, mdFrontmatterInside:
			case mdCodeFence, mdCodeInside:
				s.body = true
			default:
				l := stripCode(lines[j])
				prose = append(prose, l)
				heading := s.Level > 0 && (j == s.StartLine-1 || j == s.StartLine && kinds[j] == mdSetextUnderline)
				if !heading && strings.TrimSpace(marker.ReplaceAllString(l, "")) != "" {
					s.body = true
				}
			}
		}
		for _, m := range marker.FindAllStringSubmatch(strings.Join(prose, "\n"), -1) {
			if m[1] == "always" {
				s.Always = true
				continue
			}
			for k := range strings.SplitSeq(m[2], ",") {
				if k = strings.Join(strings.Fields(k), " "); k != "" {
					s.On = append(s.On, k)
				}
			}
		}
	}
	return out
}

// SectionTokens estimates a text's tokens as the injection does (InjectTokens).
func SectionTokens(s string) int { return InjectTokens(s) }

// Instructions is one instruction file's sections in one session, and which of them are in the window. It is not safe
// for concurrent use; a session's loop calls it from its turn.
type Instructions struct {
	name    string
	secs    []Section
	whole   bool
	b       *Budget
	in      []bool     // the section's text is in the window
	offered []bool     // a note in the window names the section as not loaded
	by      []int      // the note that carries or names the section: an index into notes, or -1 for the first message
	notes   []callNote // every note ForCall gave, in order
	initial int        // what the first message's text took of the budget
}

// callNote is one call's note: the call's id (which a model may leave empty or use again), what it took of the budget, and
// whether it is still in the window.
type callNote struct {
	id   string
	cost int
	live bool
}

// Budget is the share of the window that instruction sections and knowledge pages take together, in tokens: Limit is
// SectionShare of the served window, and Used goes down when a compaction stubs a result that carried some.
type Budget struct{ Limit, Used int }

// NewBudget is the budget for a window of window tokens.
func NewBudget(window int) *Budget { return &Budget{Limit: window * SectionShare / 100} }

// NewInstructions is name's content read by section, drawing on b. name is the file's path from the workspace root, as
// the model's read tool takes it. A file within what b has left, framing included, is loaded whole.
func NewInstructions(name, content string, b *Budget) *Instructions {
	in := &Instructions{name: name, secs: ParseSections(content), b: b}
	n := len(in.secs)
	in.in, in.offered, in.by = make([]bool, n), make([]bool, n), make([]int, n)
	for i := range in.by {
		in.by[i] = -1
	}
	in.whole = SectionTokens(in.wholeText()) <= b.Limit-b.Used
	return in
}

// wholeText is the file loaded whole, framed as Initial gives it.
func (in *Instructions) wholeText() string {
	return "Project instructions, from " + in.name + ":\n\n" + in.join(0, len(in.secs))
}

// Used is the tokens the budget's loads take, of Budget.
func (in *Instructions) Used() int { return in.b.Used }

// Budget is the budget's limit in tokens.
func (in *Instructions) Budget() int { return in.b.Limit }

// firstSection is how many sections the opening takes: the preamble and the sections up to and including the first one
// with text beyond its heading, so a file that opens with only its title takes in the section after it.
func (in *Instructions) firstSection() int {
	for i, s := range in.secs {
		if len(s.On) > 0 {
			return i // a marked section waits for its call, even after a bare title or as the first section
		}
		if s.body {
			return i + 1
		}
	}
	return len(in.secs)
}

// Initial is the text the session's first message carries, framed as reference notes, and marks what it holds as in
// the window. A file within the budget, framing included, is whole. Otherwise it is the first section, each always
// section with its unmarked subsections, and an outline of the rest with their lines, in file order. A first section
// over half the budget is cut to half first, so the outline and the calls' sections keep room. If the rest still
// passes the budget, always sections give way, from the last, each leaving its outline entries; then outline entries,
// from the end; then more lines of the first section; and each cut is said. The first section counts as in even when
// cut: it cannot come whole, and its cut names the lines to read. It is "" for an empty file.
func (in *Instructions) Initial() string {
	if len(in.secs) == 0 {
		return ""
	}
	if in.whole {
		for i := range in.secs {
			in.in[i] = true
		}
		text := in.wholeText()
		in.initial = SectionTokens(text)
		in.b.Used += in.initial
		return text
	}
	first := in.firstSection()
	// An always section comes with its subtree, less any marked subsection's own; each is one unit.
	var always [][]int
	var rest []int
	for i := first; i < len(in.secs); i++ {
		if !in.secs[i].Always {
			rest = append(rest, i)
			continue
		}
		unit := []int{i}
		for j := i + 1; j < len(in.secs) && in.within(i, j); j++ {
			if len(in.secs[j].On) > 0 {
				for j+1 < len(in.secs) && in.within(j, j+1) {
					j++
				}
				continue
			}
			unit = append(unit, j)
		}
		always = append(always, unit)
		i = unit[len(unit)-1]
		for k := unit[0] + 1; k <= i; k++ {
			if !slices.Contains(unit, k) {
				rest = append(rest, k)
			}
		}
	}
	firstLines := strings.Split(in.join(0, first), "\n")
	firstEnd := in.secs[max(0, first-1)].EndLine
	build := func(firstLines []string, cutFirst bool, always [][]int, outline []int, more int) string {
		var b strings.Builder
		fmt.Fprintf(&b, "Project notes from %s, for reference:\n\n", in.name)
		b.WriteString(strings.Join(firstLines, "\n"))
		if cutFirst {
			fmt.Fprintf(&b, "\n[… the rest of this section is cut to fit the window; read %s lines 1–%d for it]", in.name, firstEnd)
		}
		for _, unit := range always {
			for _, i := range unit {
				b.WriteString("\n\n" + in.secs[i].Text)
			}
		}
		if len(outline) > 0 || more > 0 {
			b.WriteString("\n\nOther sections of " + in.name + ", with their lines; their rules arrive when a tool call needs " +
				"them, or read the lines:\n")
			for _, i := range outline {
				s := in.secs[i]
				fmt.Fprintf(&b, "%s%s (lines %d–%d)\n", strings.Repeat("  ", max(0, s.Level-2)), s.Heading(), s.StartLine, s.EndLine)
			}
			if more > 0 {
				fmt.Fprintf(&b, "[… %d more headings]\n", more)
			}
		}
		return strings.TrimSuffix(b.String(), "\n")
	}
	outline, more, cut := rest, 0, false
	fits := func() bool {
		return SectionTokens(build(firstLines, cut, always, outline, more)) <= in.b.Limit-in.b.Used
	}
	// An opening over half the budget is cut to half first, so the outline and the calls' sections keep room.
	for len(firstLines) > 1 && SectionTokens(strings.Join(firstLines, "\n")) > (in.b.Limit-in.b.Used)/2 {
		firstLines, cut = firstLines[:len(firstLines)-1], true
	}
	for len(always) > 0 && !fits() {
		outline = append(outline, always[len(always)-1]...)
		slices.Sort(outline)
		always = always[:len(always)-1]
	}
	for len(outline) > 0 && !fits() {
		outline, more = outline[:len(outline)-1], more+1
	}
	for len(firstLines) > 1 && !fits() {
		firstLines, cut = firstLines[:len(firstLines)-1], true
	}
	// With little left, the outline's count of headings goes too; with less, there are no first-message notes at all,
	// so the budget is never passed.
	if !fits() {
		more = 0
	}
	if !fits() {
		return ""
	}
	text := build(firstLines, cut, always, outline, more)
	// The opening counts as in even when cut: the cut says which lines to read, and it cannot come whole.
	for i := range first {
		in.in[i] = true
	}
	for _, unit := range always {
		for _, i := range unit {
			in.in[i] = true
		}
	}
	in.initial = SectionTokens(text)
	in.b.Used += in.initial
	return text
}

func (in *Instructions) join(from, to int) string {
	parts := make([]string, 0, to-from)
	for _, s := range in.secs[from:to] {
		parts = append(parts, s.Text)
	}
	return strings.Join(parts, "\n")
}

// defaultWords are the heading words a call's kinds match in a section without markers: a git command brings in the
// sections on git, staging and committing; go test, or an edit of a _test.go file, the sections on testing.
var defaultWords = []struct {
	kind  string
	words []string
}{
	{"bash:git", []string{"git", "commit", "commits", "committing", "staging", "stage", "push", "pushing", "branch", "branches"}},
	{"bash:go test", []string{"test", "tests", "testing"}},
	{"edit:*_test.go", []string{"test", "tests", "testing"}},
}

// wordText is a shell word's text when it is plain or quoted text, and "?" when part of it is only known when it runs.
func wordText(w *syntax.Word) string {
	var b strings.Builder
	for _, p := range w.Parts {
		switch pt := p.(type) {
		case *syntax.Lit:
			b.WriteString(pt.Value)
		case *syntax.SglQuoted:
			b.WriteString(pt.Value)
		case *syntax.DblQuoted:
			for _, dp := range pt.Parts {
				lit, ok := dp.(*syntax.Lit)
				if !ok {
					return "?"
				}
				b.WriteString(lit.Value)
			}
		default:
			return "?"
		}
	}
	if b.Len() == 0 {
		return "?"
	}
	return b.String()
}

// valueFlags are the flags, before a subcommand, that take the next word as their value: git -C dir, git -c k=v,
// go -C dir.
var valueFlags = map[string]map[string]bool{"git": {"-C": true, "-c": true}, "go": {"-C": true}}

// callKinds is what a tool call is, for matching: each simple command of a bash command as "bash:" and its words
// (name first, as its base name), once as written and once without its flags, so "go -C pkg test" is also
// "bash:go test"; an edit or write as "edit:<path>"; and a read as "read:<path>". A quoted word is its text; a word
// whose text is not known before it runs ($VAR, $(cmd)) is "?".
func callKinds(tool string, input json.RawMessage) []string {
	switch tool {
	case "bash":
		var in struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(input, &in) != nil {
			return nil
		}
		f, err := syntax.NewParser().Parse(strings.NewReader(in.Command), "")
		if err != nil {
			return nil
		}
		var out []string
		syntax.Walk(f, func(n syntax.Node) bool {
			call, ok := n.(*syntax.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			name := call.Args[0].Lit()
			if name == "" {
				return true
			}
			name = path.Base(name)
			words, bare := []string{name}, []string{name}
			skip := false
			for _, a := range call.Args[1:] {
				w := wordText(a)
				words = append(words, w)
				switch {
				case skip:
					skip = false
				case valueFlags[name][w]:
					skip = true
				case strings.HasPrefix(w, "-") && len(bare) == 1:
				default:
					bare = append(bare, w)
				}
			}
			out = append(out, "bash:"+strings.Join(words, " "))
			if len(bare) != len(words) {
				out = append(out, "bash:"+strings.Join(bare, " "))
			}
			return true
		})
		return out
	case "edit", "write", "read":
		var in struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(input, &in) != nil || in.Path == "" {
			return nil
		}
		kind := "edit:"
		if tool == "read" {
			kind = "read:"
		}
		return []string{kind + path.Clean(in.Path)}
	}
	return nil
}

// kindMatches says whether a call kind matches a pattern kind. A bash pattern's words up to its first flag must begin
// the command's words, and each of its flags must be among the rest: "bash:git commit --amend" matches
// "git commit -m x --amend", and not "git commit -m x". An edit pattern matches the cleaned path segment by segment
// with path.Match, "**" standing for any number of segments, or matches the path's last element when it has no slash.
func kindMatches(pattern, kind string) bool {
	if p, ok := strings.CutPrefix(pattern, "bash:"); ok {
		k, ok := strings.CutPrefix(kind, "bash:")
		pw, kw := strings.Fields(p), strings.Fields(k)
		head := len(pw)
		for i, w := range pw {
			if strings.HasPrefix(w, "-") {
				head = i
				break
			}
		}
		if !ok || head == 0 || head > len(kw) || !slices.Equal(pw[:head], kw[:head]) {
			return false
		}
		for _, f := range pw[head:] {
			if !slices.Contains(kw[head:], f) {
				return false
			}
		}
		return true
	}
	if p, ok := strings.CutPrefix(pattern, "edit:"); ok {
		k, ok := strings.CutPrefix(kind, "edit:")
		if !ok {
			return false
		}
		p, k = path.Clean(p), path.Clean(k)
		if !strings.Contains(p, "/") {
			k = path.Base(k)
		}
		return globSegments(strings.Split(p, "/"), strings.Split(k, "/"))
	}
	return pattern == kind
}

// globSegments matches path segments against pattern segments, "**" matching any number of them.
func globSegments(pat, segs []string) bool {
	if len(pat) == 0 {
		return len(segs) == 0
	}
	if pat[0] == "**" {
		for i := 0; i <= len(segs); i++ {
			if globSegments(pat[1:], segs[i:]) {
				return true
			}
		}
		return false
	}
	if len(segs) == 0 {
		return false
	}
	m, _ := path.Match(pat[0], segs[0])
	return m && globSegments(pat[1:], segs[1:])
}

// markedAbove says whether a section above i in its heading path has a tack:on marker: i is then that section's to
// bring, and does not match on its own heading.
func (in *Instructions) markedAbove(i int) bool {
	level := in.secs[i].Level
	for k := i - 1; k >= 0 && level > 1; k-- {
		if l := in.secs[k].Level; l > 0 && l < level {
			if len(in.secs[k].On) > 0 {
				return true
			}
			level = l
		}
	}
	return false
}

var nonWord = regexp.MustCompile(`[^a-z0-9]+`)

// matches says whether section i is wanted for a call of these kinds: by its markers when it has any, else by a word of
// its own heading.
func (in *Instructions) matches(i int, kinds []string) bool {
	s := in.secs[i]
	if len(s.On) > 0 {
		for _, p := range s.On {
			for _, k := range kinds {
				if kindMatches(p, k) {
					return true
				}
			}
		}
		return false
	}
	if in.markedAbove(i) {
		return false
	}
	words := map[string]bool{}
	for _, w := range nonWord.Split(strings.ToLower(s.Heading()), -1) {
		words[w] = true
	}
	for _, d := range defaultWords {
		for _, k := range kinds {
			if !kindMatches(d.kind, k) {
				continue
			}
			for _, w := range d.words {
				if words[w] {
					return true
				}
			}
		}
	}
	return false
}

// within says whether section j sits under section i: after it, before the next section at i's level or above.
func (in *Instructions) within(i, j int) bool {
	if j <= i || in.secs[i].Level == 0 {
		return false
	}
	for k := i + 1; k <= j; k++ {
		if in.secs[k].Level <= in.secs[i].Level {
			return false
		}
	}
	return true
}

// ForCall is the sections tool call id brings in, as text to add to its result, and marks them in the window. The
// sections it wants are those matching the call, each with its subsections that carry no tack:on marker of their own
// (a level-1 section, a file's title, brings in only itself), less those already in or already named. They are loaded
// in file order while the budget lasts, the note's framing counted; the ones that do not fit are named with their
// lines, once. It is "" when nothing is wanted, and always "" for a file loaded whole. A compaction that stubs id's
// result must call Masked.
func (in *Instructions) ForCall(id, tool string, input json.RawMessage) string {
	if in.whole {
		return ""
	}
	kinds := callKinds(tool, input)
	if len(kinds) == 0 {
		return ""
	}
	want := map[int]bool{}
	for i := range in.secs {
		if !in.matches(i, kinds) {
			continue
		}
		want[i] = true
		if in.secs[i].Level < 2 && len(in.secs[i].On) == 0 { // an unmarked title matched on a word brings only itself
			continue
		}
		for j := i + 1; j < len(in.secs) && in.within(i, j); j++ {
			if len(in.secs[j].On) == 0 {
				want[j] = true
				continue
			}
			for j+1 < len(in.secs) && in.within(j, j+1) { // a marked subsection keeps its own subtree out too
				j++
			}
		}
	}

	var order []int // a call's sections are taken in file order
	for i := range in.secs {
		if want[i] {
			order = append(order, i)
		}
	}
	return in.load(order, id, "call")
}

// SectionSearcher finds the sections of an instruction file that match a user message: the heading paths of its best
// hits in file, best first (kensearch.Searcher, over ken's exact SearchFiles).
type SectionSearcher interface {
	SectionPaths(ctx context.Context, query string, k int, file string) ([][]string, error)
}

// MessageID is the note id of sections a user message brings in. No compaction stubs a message, so its notes stay.
const MessageID = "\x00message"

// ForHits is the sections holding these heading paths, what ken found in the file for a user message (DESIGN §2.3),
// as a note for that message ("for this message"), loaded as ForCall loads a call's: each with its unmarked
// subsections, less those already in or named, within the budget. It is "" when nothing is wanted, and always "" for a
// file loaded whole.
func (in *Instructions) ForHits(paths [][]string) string {
	if in.whole {
		return ""
	}
	// Ken's order is kept: each hit's section, then its unmarked subsections, best hit first; the budget cuts from
	// the tail. A hit in a level-1 section (a file's title and introduction) brings only that section: its subtree is
	// the whole file.
	var order []int
	seen := map[int]bool{}
	add := func(i int) {
		if !seen[i] {
			seen[i] = true
			order = append(order, i)
		}
	}
	for _, p := range paths {
		for i, s := range in.secs {
			if len(p) == 0 || !slices.Equal(s.HeadingPath, p) {
				continue
			}
			add(i)
			if s.Level < 2 && len(s.On) == 0 {
				continue
			}
			for j := i + 1; j < len(in.secs) && in.within(i, j); j++ {
				if len(in.secs[j].On) == 0 {
					add(j)
					continue
				}
				for j+1 < len(in.secs) && in.within(j, j+1) {
					j++
				}
			}
		}
	}
	return in.load(order, MessageID, "message")
}

// Name is the instruction file's path from the workspace root.
func (in *Instructions) Name() string { return in.name }

// load adds the sections of order that are neither in nor named already, taken in that order while the budget
// lasts, and names the rest; the note shows them in file order, and the whole of it is charged to call id. When the
// notice of what is left out does not fit, the last taken give way to it.
func (in *Instructions) load(order []int, id, what string) string {
	var taken, left []int // taken in order's order
	for _, i := range order {
		if in.in[i] || in.offered[i] || slices.Contains(taken, i) || slices.Contains(left, i) {
			continue
		}
		trial := append(slices.Clone(taken), i)
		slices.Sort(trial)
		if in.b.Used+SectionTokens(in.render(trial, nil, what)) > in.b.Limit {
			left = append(left, i)
			continue
		}
		taken = append(taken, i)
	}
	if len(taken) == 0 && len(left) == 0 {
		return ""
	}
	note := func() string {
		got, l := slices.Clone(taken), slices.Clone(left)
		slices.Sort(got)
		slices.Sort(l)
		return in.render(got, l, what)
	}
	// A notice alone may pass the budget, so the model always hears what it is missing.
	for len(left) > 0 && len(taken) > 0 && in.b.Used+SectionTokens(note()) > in.b.Limit {
		left = append(left, taken[len(taken)-1])
		taken = taken[:len(taken)-1]
	}
	text := note()
	got := slices.Clone(taken)
	slices.Sort(got)
	slices.Sort(left)
	in.record(id, got, left, text)
	return text
}

// header opens a note of loaded sections for what ("call" or "message"); notLoaded opens its notice of the sections it
// names instead, and notLoadedEnd closes that notice.
func (in *Instructions) header(what string) string {
	return "\n\n[Project rules from " + in.name + " for this " + what + ":]"
}

func (in *Instructions) notLoaded() string {
	return "\n\n[Not loaded, the instruction budget is spent; read " + in.name + " lines"
}

func notLoadedEnd(what string) string { return " for the rules on this " + what + ".]" }

// entry is how the notice names a section.
func (in *Instructions) entry(i int) string {
	s := in.secs[i]
	return fmt.Sprintf(" %d–%d (%s)", s.StartLine, s.EndLine, s.Heading())
}

// render is a call's note: the loaded sections got under the header, then the notice naming left.
func (in *Instructions) render(got, left []int, what string) string {
	var out strings.Builder
	if len(got) > 0 {
		out.WriteString(in.header(what))
		for _, i := range got {
			out.WriteString("\n\n" + in.secs[i].Text)
		}
	}
	if len(left) > 0 {
		out.WriteString(in.notLoaded())
		for k, i := range left {
			if k > 0 {
				out.WriteString(",")
			}
			out.WriteString(in.entry(i))
		}
		out.WriteString(notLoadedEnd(what))
	}
	return out.String()
}

// record makes call id's note a new note in the window: got in, left named, its text's tokens charged.
func (in *Instructions) record(id string, got, left []int, text string) {
	k := len(in.notes)
	for _, i := range got {
		in.in[i], in.by[i] = true, k
	}
	for _, i := range left {
		in.offered[i], in.by[i] = true, k
	}
	cost := SectionTokens(text)
	in.b.Used += cost
	in.notes = append(in.notes, callNote{id: id, cost: cost, live: true})
}

// Replay rebuilds which sections are in the window from a session's log, for a session resumed after loop.Rebuild
// (core's open calls it before loop.Resume, with the log from seq 1). It starts over from Initial, then takes each
// tool.finished result's note in log order, and each compaction's stubbed ids through Masked.
//
// A note is taken only when it can be read back exactly. That means: the sections that follow the header, one after
// another, each a section that is neither in nor named; then the notice's entries, in order; and re-rendering those
// with render gives the result's note byte for byte, up to its end or edit's current-symbol note
// (tools.CurrentSymbolOpen). A note that does not read back is skipped whole, never recorded in part; that happens
// when the file changed since the session read it, and the skipped sections come in again at their next matching call.
// The tracker must be built from the instruction file as the session read it.
func (in *Instructions) Replay(evs []proto.Event) error {
	// The budget may be shared with knowledge pages, so only this tracker's own share is taken back: its notes', and its
	// first message's, which is charged again as logged.
	in.b.Used -= in.own()
	in.initial, in.notes = 0, nil
	for i := range in.secs {
		in.in[i], in.offered[i], in.by[i] = false, false, -1
	}
	// The first message is read from the log, as the session sent it; the file may have changed since. Only a log
	// with no first message falls back to Initial over the file as it is now.
	read := false
	for _, ev := range evs {
		if ev.Type != proto.EvUserMessage {
			continue
		}
		v, err := ev.Decode()
		if err != nil {
			return fmt.Errorf("window: replay seq %d: %w", ev.Seq, err)
		}
		var text strings.Builder
		for _, c := range v.(proto.UserMessage).Content {
			text.WriteString(c.Text)
		}
		read = in.firstFrom(text.String())
		break
	}
	if !read {
		in.Initial()
	}
	for _, ev := range evs {
		v, err := ev.Decode()
		if err != nil {
			return fmt.Errorf("window: replay seq %d: %w", ev.Seq, err)
		}
		switch p := v.(type) {
		case proto.UserMessage:
			var text strings.Builder
			for _, c := range p.Content {
				text.WriteString(c.Text)
			}
			if got, left, note, ok := in.noteInMessage(text.String()); ok {
				in.record(MessageID, got, left, note)
			}
		case proto.ToolFinished:
			if got, left, text, ok := in.noteIn(p.Result); ok {
				in.record(p.ID, got, left, text)
			}
		case proto.Compaction:
			ids := make([]string, len(p.Masked))
			for i, m := range p.Masked {
				ids[i] = m.ID
			}
			in.Masked(ids)
		}
	}
	return nil
}

// firstFrom sets what the first message holds from that message as logged: its instruction text runs from Initial's
// opening line to the first part the message adds after it (the knowledge index, a repository map, injected code, a
// message's sections, or the task), or to its end for a whole file, which follows the task. The sections whose text it holds whole are in,
// and so is a cut opening, which counts as in; its tokens are the first message's charge. It is false when the message
// holds no instruction text.
func (in *Instructions) firstFrom(msg string) bool {
	for _, open := range []string{"Project notes from " + in.name + ", for reference:", "Project instructions, from " + in.name + ":"} {
		k := strings.Index(msg, open)
		if k < 0 {
			continue
		}
		part := msg[k:]
		for _, end := range []string{"\n\nTask: ", "\n\n" + RepoMapTitle, "\n\nCode that may be relevant", "\n\n[Project rules from",
			"\n\n" + indexOpen} {
			if e := strings.Index(part, end); e >= 0 {
				part = part[:e]
			}
		}
		for i, s := range in.secs {
			in.in[i] = strings.Contains(part, s.Text)
		}
		if strings.Contains(part, "[… the rest of this section is cut to fit the window;") {
			for i := range in.firstSection() {
				in.in[i] = true
			}
		}
		in.initial = SectionTokens(part)
		in.b.Used += in.initial
		return true
	}
	return false
}

// noteIn reads load's note back from a result, as Replay describes: its sections, those it names, and its text, or
// false when the result holds no note that reads back exactly.
func (in *Instructions) noteIn(result string) (got, left []int, text string, ok bool) {
	// The note sits at the result's end, after the tool's own output, which can hold text like a header (a read of the
	// instruction file, say): each place a header opens is tried, from the last back. A note with sections holds its
	// notice after them, so a notice alone is tried only in a result with no header: one whose header does not read
	// back is skipped whole, never taken for its notice.
	opens := []string{in.header("call")}
	if !strings.Contains(result, in.header("call")) {
		opens = []string{in.notLoaded()}
	}
	for _, open := range opens {
		for end := len(result); ; {
			k := strings.LastIndex(result[:end], open)
			if k < 0 {
				break
			}
			note := result[k:]
			if c := strings.Index(note, tools.CurrentSymbolOpen); c >= 0 {
				note = note[:c]
			}
			if got, left, text, ok := in.noteAt(note, "call"); ok {
				return got, left, text, true
			}
			end = k
		}
	}
	return nil, nil, "", false
}

// MessageTask joins a user message's note of sections (ForHits) to its task: the note comes first, so the message
// ends with the task, and Replay finds the note's end here.
const MessageTask = "\n\nTask: "

// noteInMessage reads ForHits's note back from a user message: it opens with its header (or its notice) and runs to a
// MessageTask that follows it.
func (in *Instructions) noteInMessage(msg string) (got, left []int, text string, ok bool) {
	for _, open := range []string{in.header("message"), in.notLoaded()} {
		start := strings.Index(msg, open)
		if start < 0 {
			continue
		}
		for from := start; ; {
			k := strings.Index(msg[from:], MessageTask)
			if k < 0 {
				break
			}
			if got, left, text, ok := in.noteAt(msg[start:from+k], "message"); ok {
				return got, left, text, true
			}
			from += k + 1
		}
	}
	return nil, nil, "", false
}

// noteAt reads a note back from note, which must begin with it and hold nothing after it.
func (in *Instructions) noteAt(note, what string) (got, left []int, text string, ok bool) {
	free := func(i int) bool {
		return !in.in[i] && !in.offered[i] && !slices.Contains(got, i) && !slices.Contains(left, i)
	}
	rest, isLoad := strings.CutPrefix(note, in.header(what))
	for isLoad {
		next := -1
		for i := range in.secs {
			if free(i) && strings.HasPrefix(rest, "\n\n"+in.secs[i].Text) {
				r := rest[len(in.secs[i].Text)+2:]
				if r == "" || strings.HasPrefix(r, "\n\n") {
					next = i
					break
				}
			}
		}
		if next < 0 {
			break
		}
		got = append(got, next)
		rest = rest[len(in.secs[next].Text)+2:]
	}
	if !isLoad {
		rest = note
	}
	if list, isNotice := strings.CutPrefix(rest, in.notLoaded()); isNotice {
		list, closed := strings.CutSuffix(list, notLoadedEnd(what))
		if !closed {
			return nil, nil, "", false
		}
		for list != "" {
			next := -1
			for i := range in.secs {
				if e := in.entry(i); free(i) && (list == e || strings.HasPrefix(list, e+",")) {
					next = i
					break
				}
			}
			if next < 0 {
				return nil, nil, "", false
			}
			left = append(left, next)
			list = strings.TrimPrefix(strings.TrimPrefix(list, in.entry(next)), ",")
		}
	}
	text = in.render(got, left, what)
	if len(got)+len(left) == 0 || text != note {
		return nil, nil, "", false
	}
	return got, left, text, true
}

// Loaded is which sections are in the window and which are named as not loaded, by index into the file's sections;
// the id of the call whose note carries or names each, "" for the first message's; and the tokens this tracker's texts
// take of the budget (own).
func (in *Instructions) Loaded() (loaded, offered []bool, by []string, used int) {
	by = make([]string, len(in.by))
	for i, k := range in.by {
		if k >= 0 {
			by[i] = in.notes[k].id
		}
	}
	return slices.Clone(in.in), slices.Clone(in.offered), by, in.own()
}

// own is what this tracker's texts in the window take of the budget: the first message's and its live notes'.
func (in *Instructions) own() int {
	n := in.initial
	for _, c := range in.notes {
		if c.live {
			n += c.cost
		}
	}
	return n
}

// Masked tells the sections a compaction stubbed the results of these tool calls: for each id, the oldest note still
// in the window with that id is out, as are the sections it carried or named, and what it took goes back to the
// budget. A compaction stubs old results, so an id a model used again frees its first use.
func (in *Instructions) Masked(ids []string) {
	for _, id := range ids {
		for k := range in.notes {
			if in.notes[k].live && in.notes[k].id == id {
				in.free(k)
				break
			}
		}
	}
}

// Drop takes back the newest note still in the window with id: the loop calls it when the note did not fit the room
// left with the call's result, so the result never carried it.
func (in *Instructions) Drop(id string) {
	for k := len(in.notes) - 1; k >= 0; k-- {
		if in.notes[k].live && in.notes[k].id == id {
			in.free(k)
			return
		}
	}
}

func (in *Instructions) free(k int) {
	in.notes[k].live = false
	in.b.Used -= in.notes[k].cost
	for i := range in.secs {
		if in.by[i] == k {
			in.in[i], in.offered[i], in.by[i] = false, false, -1
		}
	}
}

// Whole says the file is loaded whole: it is within the budget, and no call brings anything in.
func (in *Instructions) Whole() bool { return in.whole }
