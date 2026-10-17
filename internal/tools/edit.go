package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/townsendmerino/tack/internal/server"
)

// editTool replaces an exact string in a file the model has read, and refuses when the part it would change differs
// from what the model last saw (DESIGN §6.5, §7.5): the model edits only text it has seen.
type editTool struct{}

func (editTool) ReadOnly() bool { return false }
func (editTool) Spec() server.Tool {
	return server.Tool{Name: "edit",
		Description: "Replace old_string with new_string in a file you have read. old_string must match exactly, and once unless replace_all.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"relative to the repo root"},"old_string":{"type":"string"},"new_string":{"type":"string"},"replace_all":{"type":"boolean"}},"required":["path","old_string","new_string"]}`)}
}

func (editTool) Run(_ context.Context, ws *Workspace, input json.RawMessage) (Result, error) {
	var in struct {
		Path       string `json:"path"`
		OldString  string `json:"old_string"`
		NewString  string `json:"new_string"`
		ReplaceAll bool   `json:"replace_all"`
	}
	if err := decode(input, &in); err != nil {
		return errorf("bad input: %v; edit takes path, old_string, new_string, and optionally replace_all", err), nil
	}
	if in.OldString == "" {
		return errorf("old_string is empty; to create a file, use write"), nil
	}
	if in.OldString == in.NewString {
		return errorf("old_string and new_string are the same; nothing to change"), nil
	}
	p, err := ws.Resolve(in.Path)
	if err != nil {
		if os.IsNotExist(err) {
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
	n := strings.Count(cur, in.OldString)
	switch {
	case n == 0 && strings.Contains(seen, in.OldString):
		return errorf("%q changed since you last read it, in the part you are editing; read it again", in.Path), nil
	case n == 0:
		return errorf("old_string is not in %q; it must match the file exactly, including whitespace: read the lines you "+
			"want to change and copy them", in.Path), nil
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
	if in.ReplaceAll && n > 1 {
		return Result{Text: fmt.Sprintf("edited %s: replaced %d occurrences, the first at line %d", in.Path, n, line)}, nil
	}
	return Result{Text: fmt.Sprintf("edited %s at line %d", in.Path, line)}, nil
}

// writeTool creates a file, or overwrites one the model has read and that has not changed since.
type writeTool struct{}

func (writeTool) ReadOnly() bool { return false }
func (writeTool) Spec() server.Tool {
	return server.Tool{Name: "write",
		Description: "Create a file, or overwrite one you have read.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"relative to the repo root"},"content":{"type":"string"}},"required":["path","content"]}`)}
}

func (writeTool) Run(_ context.Context, ws *Workspace, input json.RawMessage) (Result, error) {
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
