// Package tools implements the tools the model calls (DESIGN §6.5): read, glob, grep, and search when a Searcher (ken's
// engine, DESIGN §12 K1/K2) is supplied, and symbol when Symbols (K11–K15) is; edit and write; read_output, todo and
// ask_user. Every schema and description
// counts against the fixed-prompt budget (DESIGN §2.3), so they are short.
//
// Paths are relative to the repository root, and every path parameter says so. An error the model sees says what to do
// instead and names paths as the model gave them, never by the host's absolute path (docs/design/p1-notes.md, "Tool
// paths"): small models learn from error text.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/townsendmerino/tack/internal/perms"
	"github.com/townsendmerino/tack/internal/server"
	"github.com/townsendmerino/tack/internal/shell"
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
	// Empty is set by a search (glob, grep) that matched nothing, so the loop can say what to do (loop.Config.ResultNotes).
	Empty bool
	// State is set by edit and write: the file's content after the call, as ContentState, or "" when there is no file. The
	// repeat rule compares it with the text, so a call that changed the file is never an unchanged repeat
	// (loop.RepeatResult).
	State string
	// Note is set by edit with Options.CurrentSymbol: a note the loop appends to Text after the repeat rule has compared
	// the result, so the rule sees what the tool said (loop.StripNotes removes it again).
	Note string
	// Base is set by a read or grep run with WithWiden: the same call's output at its base cap, from the same pass, so
	// the loop steps widening on it, or shows it instead when the output changed (loop.Config.CompactAtCliff).
	Base string
	// Tokens is what the server computed for requests made on the call's behalf (task's child session): the loop counts
	// it toward the turn's and the session's budgets, and records it on the call's tool.finished.
	Tokens int
	// ShowWide is set with Base by a widened read: the loop calls it when it shows the widened view, so the workspace
	// records those lines as shown (it records the base view's lines itself).
	ShowWide func()
	// Ran and Wrote are set by bash: what the shell's own record says ran and what its redirections wrote (shell.Result).
	Ran   []shell.Ran
	Wrote []string
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

// Options choose a session's tools. read, glob and grep are always present.
type Options struct {
	Searcher Searcher // adds search
	// Symbols adds symbol, gives edit its symbol form, and keeps the index current after every edit and write.
	Symbols Symbols
	// Write adds edit and write. Set it only where the permission engine decides each call (DESIGN §6.6).
	Write bool
	// Shell adds bash, which runs command lines in it. Like Write, only where the permission engine decides each call.
	Shell Shell
	Blobs BlobReader // adds read_output, which pages outputs the loop truncated into the blob store
	// Plan adds todo and ask_user; Ask answers ask_user, and nil answers that no one is attached.
	Plan bool
	Ask  Asker
	// Done adds done, which ends an edit task (loop.Config.EditTask).
	Done bool
	// EditMessages makes a failed edit say where the text or the file is instead (edit_messages.go).
	EditMessages bool
	// RefuseDuplicateEdits refuses an edit identical to one already applied to the same file this session.
	RefuseDuplicateEdits bool
	// LineEdits gives edit its line form: start_line and end_line in place of old_string (edit_lines.go).
	LineEdits bool
	// CurrentSymbol, when set, gives a repeated edit whose text is not in the file a note with the current text of the
	// symbol it aimed at (current_symbol.go). Only its spans are read: unlike Symbols it adds no tool and no schema field.
	CurrentSymbol Symbols
	// EditTolerances lets an edit of a .go file whose old_string has no exact match apply where one tolerance places it
	// uniquely, when the file still parses (tolerance.go).
	EditTolerances bool
	// LeanResults cuts a whole-file read at readLeanLines and grep at grepLeanMatches, each with its narrowing hint, so
	// exploring takes less of the window (calibration §16.1).
	LeanResults bool
	// RecentlyChanged adds recently_changed: the workspace's uncommitted changes and recent commits, from git (recent.go).
	RecentlyChanged bool
	// Subagent, when set, adds task, which runs it (task.go). Give it only to a parent session: a child gets none.
	Subagent Subagent
}

// NewSet builds the read-only set. searcher may be nil; with one, the search tool is included.
func NewSet(searcher Searcher) *Set { return New(Options{Searcher: searcher}) }

// New builds the set Options choose, in a fixed order.
func New(o Options) *Set {
	var ts []Tool
	if o.Searcher != nil {
		ts = append(ts, searchTool{s: o.Searcher})
	}
	if o.Symbols != nil {
		ts = append(ts, symbolTool{sym: o.Symbols})
	}
	read, grep := readMaxLines, grepMaxMatches
	if o.LeanResults {
		read, grep = readLeanLines, grepLeanMatches
	}
	ts = append(ts, readTool{symbols: o.Symbols != nil, lines: read}, globTool{}, grepTool{max: grep})
	if o.RecentlyChanged {
		ts = append(ts, recentTool{})
	}
	if o.Write {
		ts = append(ts, editTool{messages: o.EditMessages, refuseDup: o.RefuseDuplicateEdits, sym: o.Symbols, lines: o.LineEdits,
			cur: o.CurrentSymbol, tolerant: o.EditTolerances},
			writeTool{sym: o.Symbols})
	}
	if o.Shell != nil {
		ts = append(ts, bashTool{sh: o.Shell})
	}
	if o.Blobs != nil {
		ts = append(ts, readOutputTool{blobs: o.Blobs})
	}
	if o.Plan {
		ts = append(ts, todoTool{}, askUserTool{ask: o.Ask})
	}
	if o.Done {
		ts = append(ts, doneTool{})
	}
	if o.Subagent != nil {
		ts = append(ts, taskTool{run: o.Subagent})
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

// Workspace is the directory tree file tools are confined to (DESIGN §6.6, "Confinement"), and what the model has seen
// of it this session. It is safe for concurrent use.
type Workspace struct {
	root string // absolute, symlinks resolved

	mu sync.Mutex
	// seen is each file's content as the model last saw it: after it read any part of the file, or wrote or edited it.
	// edit and write check a file against it (DESIGN §7.5). It starts empty, so after a restart the model reads again.
	seen map[string]string
	// changes is the session's successful edits and writes, in order (edit_messages.go).
	changes []change
	// shown is, for each file read showed with line numbers (read, symbol), its content then and the line ranges shown of
	// that content. A line edit checks against it (edit_lines.go). Unlike seen, edit and write do not update it: the
	// model has not seen the new line numbers.
	shown map[string]shownLines
	// editCalls is how many edit calls named each file, whatever their outcome (current_symbol.go).
	editCalls map[string]int
}

// saw records that the model has seen abs with this content.
func (w *Workspace) saw(abs, content string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.seen == nil {
		w.seen = map[string]string{}
	}
	w.seen[abs] = content
}

// lastSeen is the content the model last saw of abs, and whether it has seen it at all.
func (w *Workspace) lastSeen(abs string) (string, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	c, ok := w.seen[abs]
	return c, ok
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

// resolveNew is Resolve for a path that may not exist yet, for write: its nearest existing ancestor is resolved
// through symlinks and must stay inside the workspace; the components below it do not exist, so no symlink can move
// them.
func (w *Workspace) resolveNew(p string) (string, error) {
	if !filepath.IsAbs(p) {
		p = filepath.Join(w.root, p)
	}
	p = filepath.Clean(p)
	if !within(w.root, p) {
		return "", errOutside
	}
	dir, rest := p, []string(nil)
	for {
		if _, err := os.Lstat(dir); err == nil {
			break
		}
		rest = append([]string{filepath.Base(dir)}, rest...)
		dir = filepath.Dir(dir)
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	out := filepath.Join(append([]string{real}, rest...)...)
	if !within(w.root, out) {
		return "", errOutside
	}
	if isSecret(out) {
		return "", errSecret
	}
	return out, nil
}

// pathError is the result for a path the model gave that cannot be used: it says what to do instead, and names the
// path as given, never by the host's absolute path.
func (w *Workspace) pathError(given string, err error) Result {
	switch {
	case errors.Is(err, errOutside):
		return errorf("%q is outside the repository; paths are relative to the repo root", given)
	case errors.Is(err, errSecret):
		return errorf("%q is on the secrets deny-list and cannot be used", given)
	case errors.Is(err, fs.ErrNotExist):
		return errorf("no such file or directory %q; paths are relative to the repo root, use '.' for all of it, "+
			"and glob finds files by name", given)
	}
	return errorf("%q: %s", given, w.scrub(err.Error()))
}

// scrub replaces the workspace's absolute root in s with ".", so no error the model sees carries a host path.
func (w *Workspace) scrub(s string) string {
	return strings.ReplaceAll(s, w.root, ".")
}

// inGit reports whether abs is inside a .git directory, in any letter case, since macOS's file system ignores it: tools
// never write there (DESIGN §6.6).
func inGit(w *Workspace, abs string) bool {
	for seg := range strings.SplitSeq(w.Rel(abs), "/") {
		if strings.EqualFold(seg, ".git") {
			return true
		}
	}
	return false
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

// isSecret is perms.IsSecret, the default read deny-list (DESIGN §6.6).
func isSecret(p string) bool { return perms.IsSecret(p) }

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

// WholeReadLines is how many lines a read with no range returns under o: what such a read covers.
func (o Options) WholeReadLines() int {
	if o.LeanResults {
		return readLeanLines
	}
	return readMaxLines
}

type widenKey struct{}

// WithWiden asks the read and grep run with ctx for a wider result than their cap (loop.Config.CompactAtCliff's
// progressive widening, calibration §16.2): level 1 is 3 times the cap; 2 or more is no cap on a read and 10 times the cap on a
// grep, whose hits are held in memory before any cut. A read with a range is never widened.
func WithWiden(ctx context.Context, level int) context.Context {
	if level <= 0 {
		return ctx
	}
	return context.WithValue(ctx, widenKey{}, level)
}

// widened is the cap base becomes under ctx's widening: base, 3 times base, or 0 for none.
func widened(ctx context.Context, base int) int {
	switch level, _ := ctx.Value(widenKey{}).(int); {
	case level <= 0:
		return base
	case level == 1:
		return 3 * base
	}
	return 0
}

// Widenable says whether a call to tool name with input is one progressive widening applies to: a read with no range,
// or a grep.
func Widenable(name string, input []byte) bool {
	switch name {
	case "grep":
		return true
	case "read":
		var in struct {
			Offset int `json:"offset"`
			Limit  int `json:"limit"`
		}
		return json.Unmarshal(input, &in) == nil && in.Offset <= 0 && in.Limit <= 0
	}
	return false
}
