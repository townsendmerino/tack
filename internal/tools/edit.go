package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/townsendmerino/tack/internal/server"
)

// editTool replaces an exact string in a file the model has read, and refuses when the part it would change differs
// from what the model last saw (DESIGN §6.5, §7.5): the model edits only text it has seen.
//
// With Symbols, edit also takes symbol in place of old_string (bySymbol), and a plain edit's result names the symbol
// the change is in. The schema without Symbols is unchanged, so a session without ken sends the same prefix.
type editTool struct {
	messages  bool    // Options.EditMessages
	refuseDup bool    // Options.RefuseDuplicateEdits
	sym       Symbols // Options.Symbols
}

func (editTool) ReadOnly() bool { return false }
func (t editTool) Spec() server.Tool {
	if t.sym != nil {
		return server.Tool{Name: "edit",
			Description: "Replace old_string with new_string in a file you have read. old_string must match exactly, and once unless replace_all. Or give symbol instead of old_string: new_string replaces that whole definition.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"relative to the repo root"},"old_string":{"type":"string"},"new_string":{"type":"string"},"replace_all":{"type":"boolean"},"symbol":{"type":"string","description":"a function, type or method in path"}},"required":["path","new_string"]}`)}
	}
	return server.Tool{Name: "edit",
		Description: "Replace old_string with new_string in a file you have read. old_string must match exactly, and once unless replace_all.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"relative to the repo root"},"old_string":{"type":"string"},"new_string":{"type":"string"},"replace_all":{"type":"boolean"}},"required":["path","old_string","new_string"]}`)}
}

func (t editTool) Run(ctx context.Context, ws *Workspace, input json.RawMessage) (Result, error) {
	r, err := t.run(ctx, ws, input)
	r.State = fileState(ws, input)
	return r, err
}

func (t editTool) run(ctx context.Context, ws *Workspace, input json.RawMessage) (Result, error) {
	var in struct {
		Path       string `json:"path"`
		OldString  string `json:"old_string"`
		NewString  string `json:"new_string"`
		ReplaceAll bool   `json:"replace_all"`
		Symbol     string `json:"symbol"`
	}
	if err := decode(input, &in); err != nil || in.Symbol != "" && t.sym == nil {
		if err == nil {
			err = errors.New("unknown field \"symbol\"")
		}
		return errorf("bad input: %v; edit takes path, old_string, new_string, and optionally replace_all", err), nil
	}
	if in.Symbol != "" {
		if in.OldString != "" {
			return errorf("give old_string or symbol, not both"), nil
		}
		return t.bySymbol(ctx, ws, in.Path, in.Symbol, in.NewString), nil
	}
	if in.OldString == "" {
		return errorf("old_string is empty; to create a file, use write"), nil
	}
	if in.OldString == in.NewString {
		if t.messages {
			return Result{Text: unchangedEdit, IsError: true}, nil
		}
		return errorf("old_string and new_string are the same; nothing to change"), nil
	}
	p, err := ws.Resolve(in.Path)
	if err != nil {
		if os.IsNotExist(err) {
			if t.messages {
				if near := nearestPath(ctx, ws, in.Path); near != "" {
					return errorf("no such file %q; %q exists. Paths are relative to the repo root, and write creates a new file",
						in.Path, near), nil
				}
			}
			return errorf("no such file %q; paths are relative to the repo root, and write creates a new file", in.Path), nil
		}
		return ws.pathError(in.Path, err), nil
	}
	if inGit(ws, p) {
		return errorf("%q is inside .git, which tools never change", in.Path), nil
	}
	seen, ok := ws.lastSeen(p)
	if !ok {
		return errorf("read %q before editing it", in.Path), nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return ws.pathError(in.Path, err), nil
	}
	cur := string(data)
	if t.refuseDup {
		if line, ok := ws.appliedEdit(p, in.OldString, in.NewString); ok {
			return errorf("this exact edit was already applied to %q at line %d earlier in this session; read %q to see it", in.Path,
				line, in.Path), nil
		}
	}
	n := strings.Count(cur, in.OldString)
	switch {
	case n == 0 && strings.Contains(seen, in.OldString):
		return errorf("%q changed since you last read it, in the part you are editing; read it again", in.Path), nil
	case n == 0 && t.messages:
		return notFound(ctx, ws, in.Path, p, cur, in.OldString), nil
	case n == 0:
		return errorf("this text isn't in %q; the edit may already have been made. Read the file again before editing: "+
			"old_string must match it exactly, whitespace included", in.Path), nil
	case n > 1 && !in.ReplaceAll:
		return errorf("old_string occurs %d times in %q; include more surrounding lines to make it unique, or set "+
			"replace_all", n, in.Path), nil
	case cur != seen && strings.Count(seen, in.OldString) != n:
		return errorf("%q changed since you last read it, in the part you are editing; read it again", in.Path), nil
	}
	first := strings.Index(cur, in.OldString)
	var out string
	if in.ReplaceAll {
		out = strings.ReplaceAll(cur, in.OldString, in.NewString)
	} else {
		out = cur[:first] + in.NewString + cur[first+len(in.OldString):]
	}
	if err := writeFile(p, out); err != nil {
		return errorf("%q: %s", in.Path, ws.scrub(err.Error())), nil
	}
	ws.saw(p, out)
	line := strings.Count(cur[:first], "\n") + 1
	ws.recordChange(p, cur, out, "edit", line, in.OldString, in.NewString)
	where := t.where(ctx, ws, p, line)
	if in.ReplaceAll && n > 1 {
		return Result{Text: fmt.Sprintf("edited %s: replaced %d occurrences, the first at line %d%s", in.Path, n, line, where)}, nil
	}
	return Result{Text: fmt.Sprintf("edited %s at line %d%s", in.Path, line, where)}, nil
}

// where syncs the symbol index with the edited file abs and names the innermost symbol holding line, as ", in Name", or
// "" without Symbols or when the line is at file scope. The edit has already succeeded, so an index error only drops
// the name.
func (t editTool) where(ctx context.Context, ws *Workspace, abs string, line int) string {
	if t.sym == nil {
		return ""
	}
	rel := ws.Rel(abs)
	if _, err := t.sym.Sync(ctx, []string{rel}); err != nil {
		return ""
	}
	syms, err := t.sym.SymbolAt(ctx, rel, line)
	if err != nil || len(syms) == 0 {
		return ""
	}
	return ", in " + syms[0].Name
}

// bySymbol replaces the definition of symbol in the file given with newText, as one edit: the same checks that the
// model has seen what it changes, the same record, and the same permission, since it is the edit tool. The lines
// replaced are those symbol shows: from the Go doc comment when newText starts with a comment, and from the
// declaration otherwise, so a definition written without its comment keeps the one it had.
func (t editTool) bySymbol(ctx context.Context, ws *Workspace, given, symbol, newText string) Result {
	p, err := ws.Resolve(given)
	if err != nil {
		return ws.pathError(given, err)
	}
	if inGit(ws, p) {
		return errorf("%q is inside .git, which tools never change", given)
	}
	if st, err := os.Stat(p); err == nil && st.IsDir() {
		return errorf("%q is a directory; edit needs the file that defines %s", given, symbol)
	}
	seen, ok := ws.lastSeen(p)
	if !ok {
		return errorf("read %q, or show %s with symbol, before editing it", given, symbol)
	}
	rel := ws.Rel(p)
	defs, err := definitions(ctx, ws, t.sym, symbol, rel)
	if err != nil {
		return symbolError(err)
	}
	switch {
	case len(defs) == 0:
		return errorf("no definition of %s in %q; symbol with path %q lists what is defined there", symbol, given, given)
	case len(defs) > 1:
		names := make([]string, len(defs))
		for i, d := range defs {
			names[i] = fmt.Sprintf("%s at line %d", d.Name, d.StartLine)
		}
		return errorf("%s has %d definitions in %q (%s); name one as Type.Method", symbol, len(defs), given,
			strings.Join(names, ", "))
	}
	d := defs[0]
	data, err := os.ReadFile(p)
	if err != nil {
		return ws.pathError(given, err)
	}
	cur := string(data)
	start := d.StartLine
	if d.DocLine > 0 && d.DocLine < start && strings.HasPrefix(strings.TrimSpace(newText), "//") {
		start = d.DocLine
	}
	a, b, ok := lineRange(cur, start, d.EndLine)
	if !ok {
		return errorf("the index puts %s at lines %d-%d, past the end of %q; read it again", d.Name, start, d.EndLine, given)
	}
	old := cur[a:b]
	// symbol shows at most readMaxLines lines and cuts long ones, and the model writes the replacement from what it was
	// shown: a definition it cannot have seen whole would lose the rest.
	oldLines := strings.Split(strings.TrimSuffix(old, "\n"), "\n")
	if len(oldLines) > readMaxLines {
		return errorf("%s is %d lines, more than symbol shows at once; change it with old_string", d.Name, len(oldLines))
	}
	for _, l := range oldLines {
		if cut(l) != l {
			return errorf("%s has a line longer than symbol shows whole; change it with old_string", d.Name)
		}
	}
	if cur != seen && !strings.Contains(seen, old) {
		return errorf("%q changed since you last read it, in %s; show it again with symbol", given, d.Name)
	}
	if strings.HasSuffix(old, "\n") && !strings.HasSuffix(newText, "\n") {
		newText += "\n"
	}
	if old == newText {
		if t.messages {
			return Result{Text: unchangedEdit, IsError: true}
		}
		return errorf("new_string is %s as it is; nothing to change", d.Name)
	}
	if t.refuseDup {
		if line, ok := ws.appliedEdit(p, old, newText); ok {
			return errorf("this exact edit was already applied to %q at line %d earlier in this session; read %q to see it", given,
				line, given)
		}
	}
	out := cur[:a] + newText + cur[b:]
	if err := writeFile(p, out); err != nil {
		return errorf("%q: %s", given, ws.scrub(err.Error()))
	}
	ws.saw(p, out)
	ws.recordChange(p, cur, out, "edit", start, old, newText)
	_, _ = t.sym.Sync(ctx, []string{rel}) // the edit is made; a later lookup syncs again
	end := start + strings.Count(strings.TrimSuffix(newText, "\n"), "\n")
	return Result{Text: fmt.Sprintf("edited %s: replaced %s, lines %d-%d, now lines %d-%d", given, d.Name, start, d.EndLine,
		start, end)}
}

// lineRange is the byte range of lines start..end (1-based, inclusive) in content, with the last line's newline, and
// false when the lines are not all there.
func lineRange(content string, start, end int) (int, int, bool) {
	if start < 1 || end < start {
		return 0, 0, false
	}
	a, line := 0, 1
	for line < start {
		i := strings.IndexByte(content[a:], '\n')
		if i < 0 {
			return 0, 0, false
		}
		a += i + 1
		line++
	}
	b := a
	for ; line <= end; line++ {
		if b >= len(content) {
			return 0, 0, false
		}
		i := strings.IndexByte(content[b:], '\n')
		if i < 0 {
			if line < end {
				return 0, 0, false
			}
			b = len(content)
			break
		}
		b += i + 1
	}
	return a, b, true
}

// writeTool creates a file, or overwrites one the model has read and that has not changed since.
type writeTool struct{ sym Symbols } // Options.Symbols: the index is synced after a write

func (writeTool) ReadOnly() bool { return false }
func (writeTool) Spec() server.Tool {
	return server.Tool{Name: "write",
		Description: "Create a file, or overwrite one you have read.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"relative to the repo root"},"content":{"type":"string"}},"required":["path","content"]}`)}
}

func (w writeTool) Run(ctx context.Context, ws *Workspace, input json.RawMessage) (Result, error) {
	r, err := w.run(ctx, ws, input)
	r.State = fileState(ws, input)
	return r, err
}

func (w writeTool) run(ctx context.Context, ws *Workspace, input json.RawMessage) (Result, error) {
	var in struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := decode(input, &in); err != nil {
		return errorf("bad input: %v; write takes path and content", err), nil
	}
	if in.Path == "" || strings.HasSuffix(in.Path, "/") {
		return errorf("write needs a file path, relative to the repo root"), nil
	}
	p, err := ws.resolveNew(in.Path)
	if err != nil {
		return ws.pathError(in.Path, err), nil
	}
	if inGit(ws, p) {
		return errorf("%q is inside .git, which tools never change", in.Path), nil
	}
	before := ""
	verb := "created"
	switch st, err := os.Lstat(p); {
	case err == nil && st.IsDir():
		return errorf("%q is a directory; write needs a file path", in.Path), nil
	case err == nil && st.Mode()&fs.ModeSymlink != 0:
		return errorf("%q is a symlink; write the file it points to by its own path", in.Path), nil
	case err == nil:
		seen, ok := ws.lastSeen(p)
		if !ok {
			return errorf("%q exists; read it before overwriting it", in.Path), nil
		}
		if data, err := os.ReadFile(p); err != nil || string(data) != seen {
			return errorf("%q changed since you last read it; read it again before overwriting it", in.Path), nil
		}
		before = seen
		verb = "wrote"
	case !os.IsNotExist(err):
		return ws.pathError(in.Path, err), nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return errorf("%q: %s", in.Path, ws.scrub(err.Error())), nil
	}
	if err := writeFile(p, in.Content); err != nil {
		return errorf("%q: %s", in.Path, ws.scrub(err.Error())), nil
	}
	ws.saw(p, in.Content)
	ws.recordChange(p, before, in.Content, "write", 1, "", "")
	if w.sym != nil {
		_, _ = w.sym.Sync(ctx, []string{ws.Rel(p)}) // the file is written; a later lookup syncs again
	}
	lines := strings.Count(in.Content, "\n")
	if in.Content != "" && !strings.HasSuffix(in.Content, "\n") {
		lines++
	}
	return Result{Text: fmt.Sprintf("%s %s (%d lines)", verb, in.Path, lines)}, nil
}

// writeFile replaces p's content through a temporary file in the same directory and a rename, so a reader never sees
// half a file, keeping an existing file's permissions.
func writeFile(p, content string) error {
	mode := os.FileMode(0o644)
	if st, err := os.Stat(p); err == nil {
		mode = st.Mode().Perm()
	}
	f, err := os.CreateTemp(filepath.Dir(p), ".tack-write-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// ContentState is a file content's identity for the repeat rule: a short hash of it.
func ContentState(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:8])
}

// fileState is ContentState of the file input's path names, as it is now, or "" when the path does not resolve to a file.
func fileState(ws *Workspace, input json.RawMessage) string {
	var in struct {
		Path string `json:"path"`
	}
	if json.Unmarshal(input, &in) != nil || in.Path == "" {
		return ""
	}
	p, err := ws.Resolve(in.Path)
	if err != nil {
		return ""
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return ContentState(string(b))
}
