package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/townsendmerino/tack/internal/server"
)

const (
	readMaxLines    = 200     // a whole-file read over this returns the first screen and a hint (DESIGN §7.3)
	readLeanLines   = 80      // readMaxLines under Options.LeanResults
	maxLineRunes    = 300     // longer lines are cut, in read and grep output
	maxFileBytes    = 2 << 20 // grep skips files larger than this
	globMaxResults  = 200
	grepMaxMatches  = 100
	grepLeanMatches = 30 // grepMaxMatches under Options.LeanResults
)

// readTool reads a file. A whole-file read is cut at lines (readMaxLines, or readLeanLines under LeanResults), with a
// hint; with symbols the hint also says how to list the file's symbols.
type readTool struct {
	symbols bool
	lines   int
}

func (readTool) ReadOnly() bool { return true }
func (t readTool) Spec() server.Tool {
	return server.Tool{Name: "read",
		Description: fmt.Sprintf("Read a file's lines with line numbers. Files over %d lines need offset and limit.", t.max()),
		InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"relative to the repo root"},"offset":{"type":"integer","description":"first line, from 1"},"limit":{"type":"integer"}},"required":["path"]}`)}
}

func (t readTool) Run(ctx context.Context, ws *Workspace, input json.RawMessage) (Result, error) {
	var in struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := decode(input, &in); err != nil {
		return errorf("bad input: %v; read takes path, and optionally offset and limit", err), nil
	}
	p, err := ws.Resolve(in.Path)
	if err != nil {
		return ws.pathError(in.Path, err), nil
	}
	if st, err := os.Stat(p); err == nil && st.IsDir() {
		return errorf("%q is a directory; glob lists its files, such as %s/*", in.Path, strings.TrimSuffix(in.Path, "/")), nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return ws.pathError(in.Path, err), nil
	}
	if bytes.IndexByte(data[:min(len(data), 8192)], 0) >= 0 {
		return errorf("%q is a binary file; read shows text files only", in.Path), nil
	}
	ws.saw(p, string(data))
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(data) == 0 {
		lines = nil
	}
	start, n := 1, len(lines)
	explicit := in.Offset > 0 || in.Limit > 0
	capped := widened(ctx, t.max())                         // 0: no cap (WithWiden)
	short := !explicit && capped > 0 && len(lines) > capped // the read is cut at the cap
	if in.Offset > 0 {
		start = in.Offset
	}
	if in.Limit > 0 {
		n = in.Limit
	} else if short {
		n = capped
	}
	if start > len(lines) && len(lines) > 0 {
		return errorf("%s has %d lines; offset %d is past the end", in.Path, len(lines), start), nil
	}
	end := min(start-1+n, len(lines))
	ws.showed(p, string(data), start, end)
	var b strings.Builder
	for i := start - 1; i < end; i++ {
		fmt.Fprintf(&b, "%6d\t%s\n", i+1, cut(lines[i]))
	}
	if short {
		fmt.Fprintf(&b, "… %s has %d lines; showing 1-%d. Read more with offset and limit.\n", in.Path, len(lines), capped)
		if t.symbols {
			fmt.Fprintf(&b, "symbol with path %q lists its functions and types with their lines.\n", in.Path)
		}
	}
	return Result{Text: b.String()}, nil
}

type globTool struct{}

func (globTool) ReadOnly() bool { return true }
func (globTool) Spec() server.Tool {
	return server.Tool{Name: "glob",
		Description: "List files matching a glob such as **/*.go or internal/*/doc.go.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"pattern":{"type":"string","description":"relative to the repo root"}},"required":["pattern"]}`)}
}

func (globTool) Run(ctx context.Context, ws *Workspace, input json.RawMessage) (Result, error) {
	var in struct {
		Pattern string `json:"pattern"`
	}
	if err := decode(input, &in); err != nil || in.Pattern == "" {
		return errorf("bad input: need a pattern, such as **/*.go"), nil
	}
	re, err := globRegexp(in.Pattern)
	if err != nil {
		return errorf("bad pattern %q: %v", in.Pattern, err), nil
	}
	var hits []string
	total := 0
	err = walk(ctx, ws, ws.root, func(rel string) {
		if re.MatchString(rel) {
			total++
			if len(hits) < globMaxResults {
				hits = append(hits, rel)
			}
		}
	})
	if err != nil {
		return Result{}, err
	}
	if total == 0 {
		return Result{Text: "no files match " + in.Pattern + "; patterns are relative to the repo root, and **/name finds a " +
			"name at any depth", Empty: true}, nil
	}
	out := strings.Join(sortedUnique(hits), "\n") + "\n"
	if total > len(hits) {
		out += fmt.Sprintf("… %d more; narrow the pattern\n", total-len(hits))
	}
	return Result{Text: out}, nil
}

// grepTool searches file contents, stopping at max matches (grepMaxMatches, or grepLeanMatches under LeanResults).
type grepTool struct{ max int }

func (grepTool) ReadOnly() bool { return true }
func (grepTool) Spec() server.Tool {
	return server.Tool{Name: "grep",
		Description: "Search file contents with a Go regular expression. Use for every occurrence of a name.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"pattern":{"type":"string"},"path":{"type":"string","description":"directory or file, relative to the repo root; . for all of it (the default)"},"glob":{"type":"string","description":"only files matching"}},"required":["pattern"]}`)}
}

func (t grepTool) Run(ctx context.Context, ws *Workspace, input json.RawMessage) (Result, error) {
	var in struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
		Glob    string `json:"glob"`
	}
	if err := decode(input, &in); err != nil || in.Pattern == "" {
		return errorf("bad input: need a pattern, a Go regular expression such as func Name"), nil
	}
	re, err := regexp.Compile(in.Pattern)
	if err != nil {
		return errorf("bad pattern: %v", err), nil
	}
	var only *regexp.Regexp
	if in.Glob != "" {
		if only, err = globRegexp(in.Glob); err != nil {
			return errorf("bad glob %q: %v", in.Glob, err), nil
		}
	}
	start, err := ws.Resolve(in.Path)
	if err != nil {
		return ws.pathError(in.Path, err), nil
	}
	var b strings.Builder
	matches := 0
	limit := widened(ctx, t.limit()) // 0: no limit (WithWiden)
	over := func() bool { return limit > 0 && matches > limit }
	search := func(rel string) {
		if (limit > 0 && matches >= limit) || (only != nil && !only.MatchString(rel) && !only.MatchString(filepath.Base(rel))) {
			return
		}
		p := filepath.Join(ws.root, rel)
		if st, err := os.Stat(p); err != nil || st.Size() > maxFileBytes {
			return
		}
		data, err := os.ReadFile(p)
		if err != nil || bytes.IndexByte(data[:min(len(data), 8192)], 0) >= 0 {
			return
		}
		sc := bufio.NewScanner(bytes.NewReader(data))
		sc.Buffer(make([]byte, 1<<16), maxFileBytes)
		for n := 1; sc.Scan(); n++ {
			if re.Match(sc.Bytes()) {
				matches++
				if over() {
					return
				}
				fmt.Fprintf(&b, "%s:%d:%s\n", rel, n, cut(sc.Text()))
			}
		}
	}
	if st, err := os.Stat(start); err == nil && !st.IsDir() {
		search(ws.Rel(start))
	} else if err := walk(ctx, ws, start, search); err != nil {
		return Result{}, err
	}
	if matches == 0 {
		return Result{Text: "no matches for " + in.Pattern, Empty: true}, nil
	}
	if over() {
		b.WriteString(fmt.Sprintf("… stopped at %d matches; narrow the pattern or the path\n", limit))
	}
	return Result{Text: b.String()}, nil
}

// walk calls fn with the workspace-relative path of every regular file under dir, in lexical order, skipping
// skipDir directories, secrets and symlinks (a symlink could lead outside the workspace).
func walk(ctx context.Context, ws *Workspace, dir string, fn func(rel string)) error {
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entries are skipped, not fatal
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if p != dir && skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || isSecret(p) {
			return nil
		}
		fn(ws.Rel(p))
		return nil
	})
}

// globRegexp compiles a glob over slash-separated relative paths: * and ? stay within a segment, ** spans segments,
// and a pattern with no slash matches the base name at any depth.
func globRegexp(g string) (*regexp.Regexp, error) {
	if !strings.Contains(g, "/") {
		g = "**/" + g
	}
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(g); i++ {
		c := g[i]
		switch {
		case c == '*' && i+1 < len(g) && g[i+1] == '*':
			i++
			if i+1 < len(g) && g[i+1] == '/' {
				i++
				b.WriteString("(?:.*/)?")
			} else {
				b.WriteString(".*")
			}
		case c == '*':
			b.WriteString("[^/]*")
		case c == '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

func cut(s string) string {
	r := []rune(s)
	if len(r) <= maxLineRunes {
		return s
	}
	return string(r[:maxLineRunes]) + "…"
}

// max is the lines a read with no range returns: t.lines, or readMaxLines for a zero-valued readTool.
func (t readTool) max() int {
	if t.lines > 0 {
		return t.lines
	}
	return readMaxLines
}

// limit is where grep stops: t.max, or grepMaxMatches for a zero-valued grepTool.
func (t grepTool) limit() int {
	if t.max > 0 {
		return t.max
	}
	return grepMaxMatches
}
