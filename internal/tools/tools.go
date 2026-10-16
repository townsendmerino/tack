// Package tools implements the tools the model calls (DESIGN §6.5). P0 has the read-only set: read, glob, grep, and
// search when a Searcher (ken's engine, DESIGN §12 K1/K2) is supplied. Every schema and description counts against
// the fixed-prompt budget (DESIGN §2.3), so they are short.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/townsendmerino/tack/internal/server"
)

// Tool is one tool.
type Tool interface {
	Spec() server.Tool
	// ReadOnly reports whether the tool never changes anything.
	ReadOnly() bool
	// Run executes the call. An error the model should see (bad input, no such file) is a Result with IsError; a
	// returned error is the tool failing to run at all.
	Run(ctx context.Context, ws *Workspace, input json.RawMessage) (Result, error)
}

// Result is what the model receives.
type Result struct {
	Text    string
	IsError bool
}

func errorf(format string, args ...any) Result {
	return Result{Text: "error: " + fmt.Sprintf(format, args...), IsError: true}
}

// Set is the session's tool list. It is fixed for the session: changing it re-renders the prompt and costs the cached
// prefix (DESIGN §7.2).
type Set struct {
	byName map[string]Tool
	order  []string
}

// NewSet builds the read-only set. searcher may be nil; with one, the search tool is included.
func NewSet(searcher Searcher) *Set {
	ts := []Tool{readTool{}, globTool{}, grepTool{}}
	if searcher != nil {
		ts = append([]Tool{searchTool{s: searcher}}, ts...)
	}
	s := &Set{byName: map[string]Tool{}}
	for _, t := range ts {
		name := t.Spec().Name
		s.byName[name] = t
		s.order = append(s.order, name)
	}
	return s
}

// Specs is the tool list as sent to the model, in a fixed order.
func (s *Set) Specs() []server.Tool {
	out := make([]server.Tool, 0, len(s.order))
	for _, n := range s.order {
		out = append(out, s.byName[n].Spec())
	}
	return out
}

// Get returns the named tool.
func (s *Set) Get(name string) (Tool, bool) {
	t, ok := s.byName[name]
	return t, ok
}

// Workspace is the directory tree file tools are confined to (DESIGN §6.6, "Confinement").
type Workspace struct {
	root string // absolute, symlinks resolved
}

// NewWorkspace confines tools to root.
func NewWorkspace(root string) (*Workspace, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("tools: workspace %s is not a directory", root)
	}
	return &Workspace{root: abs}, nil
}

// Root is the workspace's absolute path.
func (w *Workspace) Root() string { return w.root }

// errOutside is returned for any path that leaves the workspace, by .. or by a symlink.
var errOutside = errors.New("path is outside the workspace")

// errSecret is returned for a path on the secrets deny-list.
var errSecret = errors.New("path is on the secrets deny-list")

// Resolve maps a model-supplied path (relative to the root, or absolute inside it) to an absolute path inside the
// workspace, refusing escapes through .. or symlinks, and secrets.
func (w *Workspace) Resolve(p string) (string, error) {
	if p == "" {
		p = "."
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(w.root, p)
	}
	p = filepath.Clean(p)
	if !within(w.root, p) {
		return "", errOutside
	}
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", err
	}
	if !within(w.root, real) {
		return "", errOutside
	}
	if isSecret(real) {
		return "", errSecret
	}
	return real, nil
}

// Rel is abs relative to the root, with forward slashes.
func (w *Workspace) Rel(abs string) string {
	r, err := filepath.Rel(w.root, abs)
	if err != nil {
		return abs
	}
	return filepath.ToSlash(r)
}

func within(root, p string) bool {
	return p == root || strings.HasPrefix(p, root+string(filepath.Separator))
}

// isSecret is the default read deny-list (DESIGN §6.6): .env files, keys and certificates, and the credential
// directories a workspace should never contain but might link to.
func isSecret(p string) bool {
	base := filepath.Base(p)
	if base == ".env" || strings.HasPrefix(base, ".env.") {
		return true
	}
	for _, ext := range []string{".pem", ".key", ".p12", ".pfx"} {
		if strings.HasSuffix(base, ext) {
			return true
		}
	}
	for _, seg := range strings.Split(filepath.ToSlash(p), "/") {
		if seg == ".ssh" || seg == ".aws" || seg == ".gnupg" {
			return true
		}
	}
	return false
}

// skipDir reports directories walks never enter: version-control internals and dependency trees.
func skipDir(name string) bool {
	switch name {
	case ".git", ".hg", ".svn", "node_modules", ".ken":
		return true
	}
	return false
}

// decode unmarshals a tool's input strictly.
func decode(input json.RawMessage, v any) error {
	dec := json.NewDecoder(strings.NewReader(string(input)))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func sortedUnique(s []string) []string {
	sort.Strings(s)
	out := s[:0]
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}
