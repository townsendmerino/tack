package tools

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
)

// Completion is one path workspace/complete offers: workspace-relative with forward slashes, Kind "file" or "dir".
type Completion struct{ Path, Kind string }

// completeRefresh is how old a Completer's list may be before a call starts a refresh in the background.
const completeRefresh = 3 * time.Second

// Completer answers `@` path completion for one workspace from an in-memory list of its paths (docs/design/
// proposal-tui-protocol.md §3). The list is built by the first call, and a call on an old list answers from it while a
// single background walk replaces it, so no call waits on the disk after the first.
//
// It lists what a read could reach and a completion should offer: the walks' own skips (skipDir trees, secrets,
// symlinks) and, for completion only, what the workspace's .gitignore files ignore. A path left out here is still read by
// name; this is not access control.
type Completer struct {
	ws *Workspace

	first sync.Once // the first build; concurrent first calls share it

	mu         sync.Mutex
	list       []completeEntry // never nil once built, even when nothing is offered
	builtAt    time.Time
	refreshing bool

	// Test seams: buildHook runs at the start of every walk, entryHook for every path the walk visits.
	buildHook func()
	entryHook func(rel string)
}

type completeEntry struct {
	path, lower, base string
	dir               bool
}

// NewCompleter returns a Completer for ws. It reads nothing until the first Complete.
func NewCompleter(ws *Workspace) *Completer { return &Completer{ws: ws} }

// Complete returns up to limit paths whose letters contain query as a subsequence, ignoring case: those whose base name
// matches first, then shorter paths, then lexical order.
func (c *Completer) Complete(query string, limit int) ([]Completion, error) {
	limit = max(limit, 0)
	c.first.Do(func() {
		list := c.build()
		c.mu.Lock()
		c.list, c.builtAt = list, time.Now()
		c.mu.Unlock()
	})
	c.mu.Lock()
	if time.Since(c.builtAt) > completeRefresh && !c.refreshing {
		c.refreshing = true
		go func() {
			list := c.build()
			c.mu.Lock()
			c.list, c.builtAt, c.refreshing = list, time.Now(), false
			c.mu.Unlock()
		}()
	}
	list := c.list
	c.mu.Unlock()

	q := strings.ToLower(query)
	type hit struct {
		e    *completeEntry
		tier int
	}
	var hits []hit
	for i := range list {
		e := &list[i]
		switch {
		case subsequence(q, e.base):
			hits = append(hits, hit{e, 0})
		case subsequence(q, e.lower):
			hits = append(hits, hit{e, 1})
		}
	}
	slices.SortFunc(hits, func(a, b hit) int {
		switch {
		case a.tier != b.tier:
			return a.tier - b.tier
		case len(a.e.path) != len(b.e.path):
			return len(a.e.path) - len(b.e.path)
		}
		return strings.Compare(a.e.path, b.e.path)
	})
	out := make([]Completion, 0, min(len(hits), limit))
	for _, h := range hits[:min(len(hits), limit)] {
		kind := "file"
		if h.e.dir {
			kind = "dir"
		}
		out = append(out, Completion{Path: h.e.path, Kind: kind})
	}
	return out, nil
}

// subsequence reports whether the runes of q occur in s in order.
func subsequence(q, s string) bool {
	qr := []rune(q)
	i := 0
	for _, r := range s {
		if i < len(qr) && r == qr[i] {
			i++
		}
	}
	return i == len(qr)
}

// build walks the workspace once, outside the walks' skips. A directory's .gitignore is read as the walk enters it, so
// its patterns come after its parents' (a later pattern wins, as in git), and a path the patterns so far ignore is left
// out, an ignored directory without being entered.
func (c *Completer) build() []completeEntry {
	if c.buildHook != nil {
		c.buildHook()
	}
	root := c.ws.Root()
	out := []completeEntry{}
	var patterns []gitignore.Pattern
	var m gitignore.Matcher
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel := filepath.ToSlash(c.ws.Rel(p))
		if d.IsDir() {
			if p != root && (skipDir(d.Name()) || isSecret(p)) {
				return filepath.SkipDir
			}
			if p != root && m != nil && m.Match(strings.Split(rel, "/"), true) {
				return filepath.SkipDir
			}
			if ps := ignorePatterns(filepath.Join(p, ".gitignore"), dirSegments(rel)); len(ps) > 0 {
				patterns = append(patterns, ps...)
				m = gitignore.NewMatcher(patterns)
			}
			if p == root {
				return nil
			}
		} else if !d.Type().IsRegular() || isSecret(p) || m != nil && m.Match(strings.Split(rel, "/"), false) {
			return nil
		}
		if c.entryHook != nil {
			c.entryHook(rel)
		}
		out = append(out, completeEntry{path: rel, lower: strings.ToLower(rel), base: strings.ToLower(filepath.Base(rel)), dir: d.IsDir()})
		return nil
	})
	return out
}

// dirSegments is a workspace-relative directory's path segments, none for the root ("." or "").
func dirSegments(rel string) []string {
	if rel == "." || rel == "" {
		return nil
	}
	return strings.Split(rel, "/")
}

// ignorePatterns is the patterns of the .gitignore file at path, rooted at domain (its directory's segments; nil for the
// workspace root), none when it cannot be read.
func ignorePatterns(path string, domain []string) []gitignore.Pattern {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var ps []gitignore.Pattern
	for _, l := range strings.Split(string(b), "\n") {
		l = strings.TrimRight(l, "\r")
		if strings.TrimSpace(l) == "" || strings.HasPrefix(l, "#") {
			continue
		}
		ps = append(ps, gitignore.ParsePattern(l, domain))
	}
	return ps
}
