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

	mu         sync.Mutex
	list       []completeEntry
	builtAt    time.Time
	refreshing bool
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
	c.mu.Lock()
	if c.list == nil {
		c.mu.Unlock()
		list := c.build()
		c.mu.Lock()
		if c.list == nil {
			c.list, c.builtAt = list, time.Now()
		}
	} else if time.Since(c.builtAt) > completeRefresh && !c.refreshing {
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

// subsequence reports whether the letters of q occur in s in order.
func subsequence(q, s string) bool {
	i := 0
	for j := 0; j < len(s) && i < len(q); j++ {
		if s[j] == q[i] {
			i++
		}
	}
	return i == len(q)
}

// build walks the workspace once: every directory and regular file outside the walks' skips, then the .gitignore filter.
func (c *Completer) build() []completeEntry {
	root := c.ws.Root()
	var all []completeEntry
	var patterns []gitignore.Pattern
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == root {
			return nil
		}
		if d.IsDir() {
			if skipDir(d.Name()) || isSecret(p) {
				return filepath.SkipDir
			}
		} else if !d.Type().IsRegular() || isSecret(p) {
			return nil
		}
		rel := filepath.ToSlash(c.ws.Rel(p))
		if d.Name() == ".gitignore" && !d.IsDir() {
			patterns = append(patterns, ignorePatterns(p, strings.Split(filepath.ToSlash(filepath.Dir(rel)), "/"))...)
		}
		all = append(all, completeEntry{path: rel, lower: strings.ToLower(rel), base: strings.ToLower(filepath.Base(rel)), dir: d.IsDir()})
		return nil
	})
	if len(patterns) == 0 {
		return all
	}
	m := gitignore.NewMatcher(patterns)
	ignoredDir := map[string]bool{}
	var out []completeEntry
	for _, e := range all {
		segs := strings.Split(e.path, "/")
		skip := false
		for i := 1; i < len(segs) && !skip; i++ { // an ignored directory takes everything under it
			d := strings.Join(segs[:i], "/")
			ig, ok := ignoredDir[d]
			if !ok {
				ig = m.Match(segs[:i], true)
				ignoredDir[d] = ig
			}
			skip = ig
		}
		if !skip && !m.Match(segs, e.dir) {
			out = append(out, e)
		}
	}
	return out
}

// ignorePatterns is the patterns of the .gitignore file at path, rooted at domain (its directory's segments; "." for the
// workspace root), none when it cannot be read.
func ignorePatterns(path string, domain []string) []gitignore.Pattern {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	if len(domain) == 1 && domain[0] == "." {
		domain = nil
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
