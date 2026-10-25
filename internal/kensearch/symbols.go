package kensearch

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/townsendmerino/ken/engine"

	"github.com/townsendmerino/tack/internal/tools"
)

// testsDepth is how far TestsFor looks: tests that call the symbol, and tests that call a function that calls it
// (engine.Workspace.TestsFor).
const testsDepth = 2

// Definition implements tools.Symbols (K11, K14).
func (s *Searcher) Definition(ctx context.Context, name string) ([]tools.Def, error) {
	sites, err := s.ws.Definition(ctx, name)
	if err != nil {
		return nil, symbolsErr(err)
	}
	out := make([]tools.Def, 0, len(sites))
	for _, d := range sites {
		if d.Line == 0 {
			continue // no span recorded: nothing to show or edit
		}
		n := d.QName
		if n == "" {
			n = name
			if d.Kind != engine.DefinitionKindMethod {
				n = leaf(name)
			}
		}
		out = append(out, tools.Def{File: d.File, Name: n, Kind: kind(d.Kind), StartLine: d.Line, EndLine: d.EndLine,
			DocLine: docLine(d.DocLine, d.Line)})
	}
	return out, nil
}

// Outline implements tools.Symbols (K11). A method's Name is Container.Name.
func (s *Searcher) Outline(ctx context.Context, path string) ([]tools.Def, error) {
	files, err := s.ws.Outline(ctx, path)
	if err != nil {
		return nil, symbolsErr(err)
	}
	var out []tools.Def
	for _, f := range files {
		for _, e := range f.Entries {
			d := tools.Def{File: f.File, Name: e.Name, Kind: kind(e.Kind), StartLine: e.StartLine, EndLine: e.EndLine,
				DocLine: docLine(e.DocLine, e.StartLine)}
			if e.Container != "" {
				d.Name, d.Kind = e.Container+"."+e.Name, "method"
			}
			out = append(out, d)
		}
	}
	return out, nil
}

// Callers implements tools.Symbols (K11's CallSite.Calls).
func (s *Searcher) Callers(ctx context.Context, name string) ([]tools.Site, error) {
	files, err := s.ws.Callers(ctx, name)
	if err != nil {
		return nil, symbolsErr(err)
	}
	var out []tools.Site
	for _, f := range files {
		for _, c := range f.Calls {
			out = append(out, tools.Site{File: f.File, Line: c.Line, Name: c.Caller})
		}
	}
	return out, nil
}

// Callees implements tools.Symbols (K15).
func (s *Searcher) Callees(ctx context.Context, name string) ([]tools.Site, error) {
	cs, err := s.ws.Callees(ctx, name)
	if err != nil {
		return nil, symbolsErr(err)
	}
	out := make([]tools.Site, len(cs))
	for i, c := range cs {
		n := c.Name
		if c.Receiver != "" {
			n = c.Receiver + "." + c.Name
		}
		out[i] = tools.Site{File: c.File, Line: c.Line, Name: n}
	}
	return out, nil
}

// TestsFor implements tools.Symbols (K15).
func (s *Searcher) TestsFor(ctx context.Context, name string) ([]tools.TestSite, error) {
	ts, err := s.ws.TestsFor(ctx, name, testsDepth)
	if err != nil {
		return nil, symbolsErr(err)
	}
	out := make([]tools.TestSite, len(ts))
	for i, t := range ts {
		out[i] = tools.TestSite{File: t.File, Name: t.QName, Line: t.StartLine, Dir: t.Dir, Via: t.Via}
	}
	return out, nil
}

// SymbolAt implements tools.Symbols (K12).
func (s *Searcher) SymbolAt(ctx context.Context, path string, line int) ([]tools.Def, error) {
	spans, err := s.ws.SymbolAt(ctx, path, line)
	if err != nil {
		return nil, symbolsErr(err)
	}
	out := make([]tools.Def, len(spans))
	for i, sp := range spans {
		out[i] = tools.Def{File: path, Name: sp.QName, Kind: kind(sp.Kind), StartLine: sp.StartLine, EndLine: sp.EndLine}
	}
	return out, nil
}

// Sync implements tools.Symbols over engine.Workspace.Refresh (K13). A file is refreshed when its content differs from
// what the last Sync of it read or, for a file never synced, when it was modified after Open began: ken's index holds
// it as it was at Open, and a change since (bash, the user's editor) is not in it. Refresh republishes the whole search
// index even for a file whose content did not change, so Sync refreshes nothing it can show is current.
func (s *Searcher) Sync(ctx context.Context, paths []string) (bool, error) {
	var stale []string
	states := map[string]string{}
	s.mu.Lock()
	for _, p := range paths {
		st := s.state(p)
		have, ok := s.synced[p]
		if !ok && st != "" && s.unmodified(p) {
			s.synced[p] = st
			continue
		}
		if !ok || have != st {
			stale = append(stale, p)
			states[p] = st
		}
	}
	s.mu.Unlock()
	if len(stale) == 0 {
		return false, nil
	}
	if err := s.ws.Refresh(ctx, stale, nil); err != nil {
		return false, err
	}
	s.mu.Lock()
	for p, st := range states {
		s.synced[p] = st
	}
	s.mu.Unlock()
	return true, nil
}

// unmodified reports whether rel was last modified before Open began.
func (s *Searcher) unmodified(rel string) bool {
	st, err := os.Lstat(filepath.Join(s.root, filepath.FromSlash(rel)))
	return err == nil && st.ModTime().Before(s.opened)
}

// state is a file's identity for Sync: a hash of its content, or "" when it cannot be read (gone, or never there).
func (s *Searcher) state(rel string) string {
	b, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(rel)))
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return string(sum[:])
}

func symbolsErr(err error) error {
	switch {
	case errors.Is(err, engine.ErrStructuralBuilding):
		return fmt.Errorf("%w: %v", tools.ErrSymbolsBuilding, err)
	case errors.Is(err, engine.ErrNoStructuralIndex):
		return fmt.Errorf("%w: %v", tools.ErrNoSymbols, err)
	case errors.Is(err, engine.ErrFileNotIndexed):
		return fmt.Errorf("%w: %v", tools.ErrNotIndexed, err)
	}
	return err
}

func kind(k engine.DefinitionKind) string {
	switch k {
	case engine.DefinitionKindClass:
		return "type"
	case engine.DefinitionKindMethod:
		return "method"
	}
	return "func"
}

// docLine is ken's DocLine as tools.Def takes it: 0 unless a comment starts above the declaration.
func docLine(doc, start int) int {
	if doc > 0 && doc < start {
		return doc
	}
	return 0
}

// leaf is a qualified name's last part: "Select" for "pull.Select".
func leaf(name string) string {
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '.' {
			return name[i+1:]
		}
	}
	return name
}

var _ tools.Symbols = (*Searcher)(nil)
