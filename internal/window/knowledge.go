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
// file loads when a tool call reads or edits that file, drawing on the instruction budget, unless the file no longer
// matches the hash the page records: then the page is possibly out of date, and it is named, not loaded as fact.
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
		for l := range strings.SplitSeq(pg.Body, "\n") {
			if m := atxHeading.FindStringSubmatch(l); m != nil && strings.TrimSpace(m[2]) != "" {
				pg.Title = strings.TrimSpace(m[2])
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
	in     []bool
	cost   []int
	by     []string
}

// NewKnowledge is root's pages for a window of window tokens, drawing on b.
func NewKnowledge(root string, pages []Page, window int, b *Budget) *Knowledge {
	n := len(pages)
	return &Knowledge{root: root, pages: pages, window: window, b: b, in: make([]bool, n), cost: make([]int, n), by: make([]string, n)}
}

// fresh says whether every pin of the page matches its file now.
func (k *Knowledge) fresh(pg Page) bool {
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

// Index is the first message's list of pages, one line each, cut from the end to IndexShare of the window; it draws on
// the budget. It is "" when there are none.
func (k *Knowledge) Index() string {
	if len(k.pages) == 0 {
		return ""
	}
	capTokens := k.window * IndexShare / 100
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
	k.b.Used += SectionTokens(text)
	return text
}

// ForCall is the pages tool call id brings in, as text to add to its result: each page pinned to a file the call reads
// or edits that is not in already. A fresh page is loaded whole while the budget lasts; a page whose pins no longer
// match is named as possibly out of date and not loaded. A compaction that stubs id's result must call Masked.
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
	for i, pg := range k.pages {
		if k.in[i] || !pinnedTo(pg, files) {
			continue
		}
		var text string
		switch {
		case !k.fresh(pg):
			text = "\n\n[Knowledge page " + pg.Path + " is possibly out of date: the code it is pinned to changed since it " +
				"was written, so it was not loaded. Read it only to check it against the code.]"
		default:
			text = "\n\n[Knowledge page " + pg.Path + ", " + pg.Title + ":]\n\n" + strings.TrimSpace(pg.Body)
			if k.b.Used+SectionTokens(text) > k.b.Limit {
				text = "\n\n[Knowledge page " + pg.Path + " is not loaded, the instruction budget is spent; read it if you need it.]"
			}
		}
		cost := SectionTokens(text)
		k.b.Used += cost
		k.in[i], k.cost[i], k.by[i] = true, cost, id
		out.WriteString(text)
	}
	return out.String()
}

func pinnedTo(pg Page, files map[string]bool) bool {
	for _, p := range pg.Pins {
		if files[p.File] {
			return true
		}
	}
	return false
}

// Masked tells the pages a compaction stubbed the results of these tool calls: the pages those results carried are out
// again, and their cost goes back to the budget.
func (k *Knowledge) Masked(ids []string) {
	gone := map[string]bool{}
	for _, id := range ids {
		gone[id] = true
	}
	for i := range k.pages {
		if k.in[i] && gone[k.by[i]] {
			k.b.Used -= k.cost[i]
			k.in[i], k.cost[i], k.by[i] = false, 0, ""
		}
	}
}
