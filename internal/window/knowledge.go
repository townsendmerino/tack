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
// whose file leaves the workspace (absolute, or through "..") is dropped.
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
	if path.IsAbs(file) || file == ".." || strings.HasPrefix(file, "../") {
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
	notes  []knowNote
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
		note: make([]int, n)}
	for i, pg := range pages {
		k.fresh[i] = k.matches(pg)
	}
	return k
}

// matches says whether every pin of the page matches its file now.
func (k *Knowledge) matches(pg Page) bool {
	for _, p := range pg.Pins {
		b, err := os.ReadFile(filepath.Join(k.root, filepath.FromSlash(p.File)))
		if err != nil || p.Hash == "" {
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
	for _, pg := range k.pages {
		l := "- " + pg.Title + " (" + pg.Path
		if len(pg.Pins) > 0 {
			files := make([]string, len(pg.Pins))
			for i, p := range pg.Pins {
				files[i] = p.File
			}
			l += "; pinned to " + strings.Join(files, ", ")
		}
		lines = append(lines, l+")")
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
	if SectionTokens(text) > capTokens {
		return ""
	}
	k.b.Used += SectionTokens(text)
	return text
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
	if len(files) == 0 {
		return ""
	}
	var out strings.Builder
	var touched []int
	for i, pg := range k.pages {
		if !pinnedTo(pg, files) || k.state[i] == pageIn || k.state[i] == pageStale {
			continue
		}
		body := "\n\n[Knowledge page " + pg.Path + ", " + pg.Title + ":]\n\n" + strings.TrimSpace(pg.Body)
		switch {
		case !k.fresh[i]:
			out.WriteString("\n\n[Knowledge page " + pg.Path + " is possibly out of date: the code it is pinned to changed " +
				"since it was written, so it was not loaded. Read it only to check it against the code.]")
			k.state[i] = pageStale
		case k.b.Used+SectionTokens(out.String()+body) <= k.b.Limit:
			out.WriteString(body)
			k.state[i] = pageIn
		case k.state[i] == pageNoRoom:
			continue // named already; it waits for room
		default:
			out.WriteString("\n\n[Knowledge page " + pg.Path + " is not loaded, the instruction budget is spent; read it if you need it.]")
			k.state[i] = pageNoRoom
		}
		touched = append(touched, i)
	}
	if len(touched) == 0 {
		return ""
	}
	text := out.String()
	n := len(k.notes)
	for _, i := range touched {
		k.note[i] = n
	}
	k.notes = append(k.notes, knowNote{id: id, cost: SectionTokens(text), live: true})
	k.b.Used += k.notes[n].cost
	return text
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
		if k.state[i] != pageOut && k.note[i] == n {
			k.state[i] = pageOut
		}
	}
}
