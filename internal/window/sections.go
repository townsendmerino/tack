package window

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"
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
		if i > 0 && len(s.On) > 0 {
			return i // a marked section waits for its call, even after a bare title
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
		in.b.Used += SectionTokens(text)
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
	in.b.Used += SectionTokens(text)
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

// valueFlags are the flags, before a subcommand, that take the next word as their value: git -C dir, git -c k=v,
// go -C dir.
var valueFlags = map[string]map[string]bool{"git": {"-C": true, "-c": true}, "go": {"-C": true}}

// callKinds is what a tool call is, for matching: each simple command of a bash command as "bash:" and its words
// (name first, as its base name), once as written and once without its flags, so "go -C pkg test" is also
// "bash:go test"; an edit or write as "edit:<path>"; and a read as "read:<path>". A word that is not a plain literal ends the command's words.
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
				w := a.Lit()
				if w == "" {
					break
				}
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

// kindMatches says whether a call kind matches a pattern kind. A bash pattern matches a command whose words begin with
// its words ("bash:git commit" matches "git commit -m x"). An edit pattern matches the cleaned path segment by segment
// with path.Match, "**" standing for any number of segments, or matches the path's last element when it has no slash.
func kindMatches(pattern, kind string) bool {
	if p, ok := strings.CutPrefix(pattern, "bash:"); ok {
		k, ok := strings.CutPrefix(kind, "bash:")
		pw, kw := strings.Fields(p), strings.Fields(k)
		return ok && len(pw) > 0 && len(pw) <= len(kw) && slices.Equal(pw, kw[:len(pw)])
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
		if in.secs[i].Level < 2 {
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
	return in.load(want, id)
}

// load adds the wanted sections that are neither in nor named already, in file order, while the budget lasts, and
// names the rest; the whole note is charged to call id.
func (in *Instructions) load(want map[int]bool, id string) string {
	header := "\n\n[Project rules from " + in.name + " for this call:]"
	var got, left []int
	body := "" // the loaded sections' text, to price the next
	for i := range in.secs {
		if !want[i] || in.in[i] || in.offered[i] {
			continue
		}
		next := body + "\n\n" + in.secs[i].Text
		if in.b.Used+SectionTokens(header+next) > in.b.Limit {
			left = append(left, i)
			continue
		}
		body = next
		got = append(got, i)
	}
	if len(got) == 0 && len(left) == 0 {
		return ""
	}
	note := func() string {
		var out strings.Builder
		if len(got) > 0 {
			out.WriteString(header)
			for _, i := range got {
				out.WriteString("\n\n" + in.secs[i].Text)
			}
		}
		if len(left) > 0 {
			out.WriteString("\n\n[Not loaded, the instruction budget is spent; read " + in.name + " lines")
			for k, i := range left {
				if k > 0 {
					out.WriteString(",")
				}
				fmt.Fprintf(&out, " %d–%d (%s)", in.secs[i].StartLine, in.secs[i].EndLine, in.secs[i].Heading())
			}
			out.WriteString(" for the rules on this call.]")
		}
		return out.String()
	}
	// The notice of what was left out must fit too: loaded sections give way to it from the last. A notice alone may
	// pass the budget, so the model always hears what it is missing.
	for len(left) > 0 && len(got) > 0 && in.b.Used+SectionTokens(note()) > in.b.Limit {
		last := got[len(got)-1]
		got = got[:len(got)-1]
		left = append(left, last)
		slices.Sort(left)
	}
	text := note()
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
	return text
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
