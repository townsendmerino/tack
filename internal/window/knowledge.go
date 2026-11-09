package window

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/tools"
)

// Knowledge pages (DESIGN §2.5, §6.7 "Memory across sessions"; ADR-007): markdown files under .agents/knowledge/, one
// page per file, package or subject. The first message carries only a short index of their titles. A page pinned to a
// file loads when a tool call reads or edits that file, drawing on the instruction budget, unless the file did not
// match the hash the page records when the session began: then the page is possibly out of date, and it is named, not
// loaded as fact.
//
// A page's frontmatter names its pins, each a file from the workspace root and the sha256 of its content (a prefix of
// at least 12 hex digits):
//
//	---
//	title: The agent loop
//	pins:
//	  - internal/loop/loop.go sha256:3f2a9c0d41be
//	---

// KnowledgeDir is where a workspace's knowledge pages live, from its root.
const KnowledgeDir = ".agents/knowledge"

// IndexShare is the share of the served window the first message's index of pages may take, in percent.
const IndexShare = 2

// minPinHex is the shortest hash prefix a pin may record.
const minPinHex = 12

// Pin is a file a page describes and the hash of its content when the page was written; Hash is "" when the page gives
// none, and such a pin never matches.
type Pin struct {
	File string
	Hash string
}

// Page is one knowledge page.
type Page struct {
	Path  string // from the workspace root
	Title string
	Pins  []Pin
	Body  string // the text after the frontmatter
}

// ParsePage reads a page's frontmatter: its title (else its first heading, else its file name) and its pins. A pin
// whose file leaves the workspace (absolute, or through "..") or names the root itself is dropped.
func ParsePage(p, content string) Page {
	pg := Page{Path: p, Body: content}
	if rest, ok := strings.CutPrefix(content, "---\n"); ok {
		if front, body, ok := strings.Cut(rest, "\n---\n"); ok {
			pg.Body = body
			inPins := false
			for l := range strings.SplitSeq(front, "\n") {
				t := strings.TrimSpace(l)
				switch {
				case strings.HasPrefix(t, "title:"):
					pg.Title, inPins = strings.TrimSpace(strings.TrimPrefix(t, "title:")), false
				case strings.HasPrefix(t, "pins:"):
					inPins = true
					for v := range strings.SplitSeq(strings.TrimPrefix(t, "pins:"), ",") {
						pg.addPin(v)
					}
				case inPins && strings.HasPrefix(t, "- "):
					pg.addPin(strings.TrimPrefix(t, "- "))
				default:
					inPins = false
				}
			}
		}
	}
	if pg.Title == "" {
		for _, s := range ParseSections(pg.Body) {
			if h := s.Heading(); h != "" {
				pg.Title = h
				break
			}
		}
	}
	if pg.Title == "" {
		pg.Title = strings.TrimSuffix(path.Base(p), ".md")
	}
	return pg
}

func (pg *Page) addPin(v string) {
	f := strings.Fields(v)
	if len(f) == 0 {
		return
	}
	file := path.Clean(f[0])
	if path.IsAbs(file) || file == "." || file == ".." || strings.HasPrefix(file, "../") {
		return
	}
	pin := Pin{File: file}
	if len(f) > 1 {
		if h, ok := strings.CutPrefix(f[1], "sha256:"); ok && len(h) >= minPinHex {
			pin.Hash = strings.ToLower(h)
		}
	}
	pg.Pins = append(pg.Pins, pin)
}

// LoadPages reads every .md file under root's KnowledgeDir, in path order; none when the directory is missing.
func LoadPages(root string) ([]Page, error) {
	dir := filepath.Join(root, filepath.FromSlash(KnowledgeDir))
	var pages []Page
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == dir && os.IsNotExist(err) {
				return filepath.SkipDir
			}
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".md") || !d.Type().IsRegular() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		pages = append(pages, ParsePage(filepath.ToSlash(rel), string(b)))
		return nil
	})
	sort.Slice(pages, func(i, j int) bool { return pages[i].Path < pages[j].Path })
	return pages, err
}

// Knowledge is a workspace's pages in one session, and which are in the window. It is not safe for concurrent use; a
// session's loop calls it from its turn.
type Knowledge struct {
	root   string
	pages  []Page
	window int
	b      *Budget
	fresh  []bool      // each page's pins matched their files when the session began
	state  []pageState // where each page stands in the window
	note   []int       // the note that carries or names each page: an index into notes
	named  []int       // for a page loaded after a notice named it for want of room, that notice's note, else -1
	notes  []knowNote
	// indexCost is what the first message's index took of the budget.
	indexCost int
}

type pageState int

const (
	pageOut    pageState = iota // not in the window
	pageIn                      // its text is in the window
	pageStale                   // named in the window as possibly out of date
	pageNoRoom                  // named in the window as not loaded for want of budget; it loads once it fits
)

// knowNote is one call's note: its id (which a model may leave empty or use again), what it took of the budget, and
// whether it is still in the window.
type knowNote struct {
	id   string
	cost int
	live bool
}

// NewKnowledge is root's pages for a window of window tokens, drawing on b. Each page's freshness is judged now,
// against the tree as the session begins: the session's own edits of a pinned file do not make its page stale midway
// (the loop asks only after an edit has run), and a change made before the session does.
func NewKnowledge(root string, pages []Page, window int, b *Budget) *Knowledge {
	n := len(pages)
	k := &Knowledge{root: root, pages: pages, window: window, b: b, fresh: make([]bool, n), state: make([]pageState, n),
		note: make([]int, n), named: make([]int, n)}
	for i := range k.named {
		k.named[i] = -1
	}
	for i, pg := range pages {
		k.fresh[i] = k.matches(pg)
	}
	return k
}

// matches says whether every pin of the page matches its file now.
func (k *Knowledge) matches(pg Page) bool {
	for _, p := range pg.Pins {
		if p.Hash == "" {
			return false // a pin without a hash never matches; its file is not read
		}
		b, err := os.ReadFile(filepath.Join(k.root, filepath.FromSlash(p.File)))
		if err != nil {
			return false
		}
		sum := sha256.Sum256(b)
		if !strings.HasPrefix(hex.EncodeToString(sum[:]), p.Hash) {
			return false
		}
	}
	return true
}

// Index is the first message's list of pages, one line each, cut from the end to IndexShare of the window and to what
// the budget has left; it draws on the budget. It is "" when there are none, or when not even its first line fits.
func (k *Knowledge) Index() string {
	if len(k.pages) == 0 {
		return ""
	}
	capTokens := min(k.window*IndexShare/100, k.b.Limit-k.b.Used)
	head := "Knowledge pages in " + KnowledgeDir + "/, notes on this code; one loads when a call reads or edits the file it is pinned to:\n"
	var lines []string
	for i := range k.pages {
		lines = append(lines, k.indexLine(i))
	}
	build := func(lines []string, more int) string {
		s := head + strings.Join(lines, "\n")
		if more > 0 {
			s += fmt.Sprintf("\n[… %d more pages]", more)
		}
		return s
	}
	more := 0
	for len(lines) > 0 && SectionTokens(build(lines, more)) > capTokens {
		lines, more = lines[:len(lines)-1], more+1
	}
	text := build(lines, more)
	if len(lines) == 0 || SectionTokens(text) > capTokens {
		return "" // not one page line fits: no index rather than a header over "[… N more pages]"
	}
	k.indexCost = SectionTokens(text)
	k.b.Used += k.indexCost
	return text
}

// indexLine is a page's line in the index; a page whose pins did not match as the session began says so, which also
// lets Replay read its freshness back from the logged index.
func (k *Knowledge) indexLine(i int) string {
	pg := k.pages[i]
	l := "- " + pg.Title + " (" + pg.Path
	if len(pg.Pins) > 0 {
		files := make([]string, len(pg.Pins))
		for j, p := range pg.Pins {
			files[j] = p.File
		}
		l += "; pinned to " + strings.Join(files, ", ")
	}
	if !k.fresh[i] {
		l += "; possibly out of date"
	}
	return l + ")"
}

// ForCall is the pages tool call id brings in, as text to add to its result: each page pinned to a file the call reads
// or edits. A page that was fresh when the session began is loaded whole while the budget lasts, and named as not
// loaded when it does not fit; a named page loads at a later matching call once it fits, and is not named again. A
// page whose pins did not match is named once as possibly out of date. A compaction that stubs id's result must call
// Masked.
func (k *Knowledge) ForCall(id, tool string, input json.RawMessage) string {
	files := map[string]bool{}
	for _, kind := range callKinds(tool, input) {
		for _, p := range []string{"read:", "edit:"} {
			if f, ok := strings.CutPrefix(kind, p); ok {
				files[f] = true
			}
		}
	}
	var out strings.Builder
	var touched []int
	for i, pg := range k.pages {
		if !pinnedTo(pg, files) || k.state[i] == pageIn || k.state[i] == pageStale {
			continue
		}
		body, stale, notice := k.texts(i)
		fits := func(add string) bool { return k.b.Used+SectionTokens(out.String()+add) <= k.b.Limit }
		// A notice that does not fit what is left is not given either; it is tried again at the next matching call.
		switch {
		case !k.fresh[i]:
			if !fits(stale) {
				continue
			}
			out.WriteString(stale)
			k.state[i] = pageStale
		case fits(body):
			if k.state[i] == pageNoRoom {
				k.named[i] = k.note[i] // its notice stays in the window
			}
			out.WriteString(body)
			k.state[i] = pageIn
		case k.state[i] == pageNoRoom:
			continue // named already; it waits for room
		default:
			if !fits(notice) {
				continue
			}
			out.WriteString(notice)
			k.state[i] = pageNoRoom
		}
		touched = append(touched, i)
	}
	text := out.String()
	k.record(id, touched, text)
	return text
}

// texts are what a page can add to a result: its body, framed; the notice that it is possibly out of date; and the
// notice that it is not loaded for want of room.
func (k *Knowledge) texts(i int) (body, stale, noRoom string) {
	pg := k.pages[i]
	body = "\n\n[Knowledge page " + pg.Path + ", " + pg.Title + ":]\n\n" + strings.TrimSpace(pg.Body)
	stale = "\n\n[Knowledge page " + pg.Path + " is possibly out of date: the code it is pinned to changed since it " +
		"was written, so it was not loaded. Read it only to check it against the code.]"
	noRoom = "\n\n[Knowledge page " + pg.Path + " is not loaded, the instruction budget is spent; read it if you need it.]"
	return body, stale, noRoom
}

// record makes call id's note, which carries touched: every call that ran gets one, empty when it brought nothing, so
// a model's reused or empty ids still pair each compaction with the right call.
func (k *Knowledge) record(id string, touched []int, text string) {
	n := len(k.notes)
	for _, i := range touched {
		k.note[i] = n
	}
	cost := 0
	if text != "" {
		cost = SectionTokens(text)
	}
	k.notes = append(k.notes, knowNote{id: id, cost: cost, live: true})
	k.b.Used += k.notes[n].cost
}

// Replay rebuilds a reopened session's tracker from its log, before anything is priced: core makes the tracker over
// the tree as it is now, then calls Replay instead of Index, and only then sizes the instruction file. So:
//   - the index is the logged first message's, priced as logged, and each page's freshness is read from its line there
//     (a stale page's line says so), not judged again over a tree the session may have edited;
//   - each result of a call that ran is read for the texts ForCall gave there, by their framing lines (so a page whose
//     own file changed since is still found), and its note charged as logged: a page's text and a no-room notice say it
//     was fresh, a stale notice that it was not;
//   - each compaction's stubbed ids go through Masked.
//
// Pages the log never mentions keep the judgment made over the tree as it is now. Only this tracker's own share of the
// shared budget is taken back first.
func (k *Knowledge) Replay(evs []proto.Event) error {
	k.b.Used -= k.Own()
	k.indexCost, k.notes = 0, nil
	for i := range k.pages {
		k.state[i], k.note[i], k.named[i] = pageOut, 0, -1
	}
	first := true
	for _, ev := range evs {
		v, err := ev.Decode()
		if err != nil {
			return fmt.Errorf("window: replay seq %d: %w", ev.Seq, err)
		}
		switch p := v.(type) {
		case proto.UserMessage:
			if first {
				first = false
				var text strings.Builder
				for _, c := range p.Content {
					text.WriteString(c.Text)
				}
				k.indexFrom(text.String())
			}
		case proto.ToolFinished:
			if p.Outcome != proto.OutcomeOK && p.Outcome != proto.OutcomeError {
				continue // the loop asks only about calls that ran
			}
			k.replayResult(p.ID, p.Result)
		case proto.Compaction:
			ids := make([]string, len(p.Masked))
			for i, m := range p.Masked {
				ids[i] = m.ID
			}
			k.Masked(ids)
		}
	}
	return nil
}

// indexOpen opens the index in the first message.
const indexOpen = "Knowledge pages in " + KnowledgeDir + "/"

// indexFrom prices the index the logged first message carries, and reads each listed page's freshness from its line.
// The index runs from its opening line to the first part the message adds after it, or to the task.
func (k *Knowledge) indexFrom(msg string) {
	s := strings.Index(msg, indexOpen)
	if s < 0 {
		return
	}
	part := msg[s:]
	for _, end := range []string{"\n\nTask: ", "\n\n" + RepoMapTitle, "\n\nCode that may be relevant", "\n\n[Project rules from"} {
		if e := strings.Index(part, end); e >= 0 {
			part = part[:e]
		}
	}
	k.indexCost = SectionTokens(part)
	k.b.Used += k.indexCost
	for i := range k.pages {
		was := k.fresh[i]
		for _, f := range []bool{true, false} {
			k.fresh[i] = f
			if strings.Contains(part, k.indexLine(i)+"\n") || strings.HasSuffix(part, k.indexLine(i)) {
				was = f
				break
			}
		}
		k.fresh[i] = was
	}
}

// opens are the framing lines a page's texts open with, which hold its path and not its body: its text's, its stale
// notice's, and its no-room notice's.
func (k *Knowledge) opens(i int) (body, stale, noRoom string) {
	p := k.pages[i].Path
	return "\n\n[Knowledge page " + p + ", ", "\n\n[Knowledge page " + p + " is possibly out of date:",
		"\n\n[Knowledge page " + p + " is not loaded, the instruction budget is spent;"
}

// replayResult records the note one result holds: the texts ForCall gave, which run from the first page frame to the
// result's end or edit's current-symbol note after it, charged as they stand, and the pages they carry or name. A
// result with none gets an empty note, as ForCall gives one.
func (k *Knowledge) replayResult(id, result string) {
	start := strings.Index(result, "\n\n[Knowledge page ")
	if start < 0 {
		k.record(id, nil, "")
		return
	}
	seg := result[start:]
	if c := strings.Index(seg, tools.CurrentSymbolOpen); c >= 0 {
		seg = seg[:c]
	}
	var touched []int
	for i := range k.pages {
		if k.state[i] == pageIn || k.state[i] == pageStale {
			continue
		}
		body, stale, noRoom := k.opens(i)
		switch {
		case strings.Contains(seg, body):
			if k.state[i] == pageNoRoom {
				k.named[i] = k.note[i]
			}
			k.state[i], k.fresh[i] = pageIn, true
		case strings.Contains(seg, stale):
			k.state[i], k.fresh[i] = pageStale, false
		case k.state[i] != pageNoRoom && strings.Contains(seg, noRoom):
			k.state[i], k.fresh[i] = pageNoRoom, true // only a fresh page is named for want of room
		default:
			continue
		}
		touched = append(touched, i)
	}
	if len(touched) == 0 {
		seg = ""
	}
	k.record(id, touched, seg)
}

// Budget is the budget this tracker draws on, shared with the instruction sections.
func (k *Knowledge) Budget() *Budget { return k.b }

// Own is what this tracker's texts take of the budget: its index's and its live notes'.
func (k *Knowledge) Own() int {
	n := k.indexCost
	for _, c := range k.notes {
		if c.live {
			n += c.cost
		}
	}
	return n
}

func pinnedTo(pg Page, files map[string]bool) bool {
	for _, p := range pg.Pins {
		if files[p.File] {
			return true
		}
	}
	return false
}

// Masked tells the pages a compaction stubbed the results of these tool calls: for each id, its oldest note still in
// the window is out, with the pages it carried or named, and what it took goes back to the budget.
func (k *Knowledge) Masked(ids []string) {
	for _, id := range ids {
		for n := range k.notes {
			if k.notes[n].live && k.notes[n].id == id {
				k.free(n)
				break
			}
		}
	}
}

// Drop takes back the newest note still in the window with id: the loop calls it when the note did not fit the room
// left with the call's result.
func (k *Knowledge) Drop(id string) {
	for n := len(k.notes) - 1; n >= 0; n-- {
		if k.notes[n].live && k.notes[n].id == id {
			k.free(n)
			return
		}
	}
}

func (k *Knowledge) free(n int) {
	k.notes[n].live = false
	k.b.Used -= k.notes[n].cost
	for i := range k.pages {
		switch {
		case k.named[i] == n: // the notice that named a page loaded since is gone
			k.named[i] = -1
		case k.state[i] != pageOut && k.note[i] == n:
			k.state[i] = pageOut
			if m := k.named[i]; m >= 0 && k.notes[m].live { // its earlier notice still names it
				k.state[i], k.note[i], k.named[i] = pageNoRoom, m, -1
			}
		}
	}
}
