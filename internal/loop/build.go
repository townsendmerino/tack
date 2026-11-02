package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mvdan.cc/sh/v3/syntax"

	"github.com/townsendmerino/tack/internal/perms"
	"github.com/townsendmerino/tack/internal/shell"
)

// Build after edit (Config.BuildAfterEdit; the C6 candidate "A build after every edit",
// docs/measurements/e1-calibration-c6-status-2026-10-21.md): after a successful edit or write of a .go file, the loop
// builds that file's package in the session's shell, and when it does not build, appends the compiler's first
// BuildErrorLines lines to the tool's result as a note (buildOpen). It adds nothing when the package builds, when the
// file is in no module inside the workspace, when the build fails to finish within BuildTimeout or is stopped, or when
// the permission engine would not run the command without asking: the command is tack's, not the model's, so it runs
// only where the same bash call would run unasked (in auto mode, inside the sandbox). It is not a verification for
// VerifyDone, which counts only the model's own builds and tests.
const (
	BuildErrorLines = 10
	BuildTimeout    = time.Minute
	buildLineRunes  = 200
)

// buildOpen starts a build note. Unlike the one-line notes it spans lines, so StripNotes finds it by this prefix.
const buildOpen = "\n\n[tack: this change does not build ("

// buildNote is the note for a successful edit or write with input: the build's errors when the file's package does not
// build, and "" otherwise.
func (s *Session) buildNote(ctx context.Context, input json.RawMessage) string {
	if s.cfg.Shell == nil || s.cfg.Perms == nil || s.cfg.WS == nil {
		return ""
	}
	var in struct {
		Path string `json:"path"`
	}
	if json.Unmarshal(input, &in) != nil || !strings.HasSuffix(in.Path, ".go") {
		return ""
	}
	abs, err := s.cfg.WS.Resolve(in.Path)
	if err != nil {
		return ""
	}
	mod := moduleDir(s.cfg.WS.Root(), filepath.Dir(abs))
	if mod == "" {
		return ""
	}
	rel, err := filepath.Rel(mod, filepath.Dir(abs))
	if err != nil {
		return ""
	}
	pkg := "./" + filepath.ToSlash(rel)
	if rel == "." {
		pkg = "."
	}
	verb, shown := "build -o /dev/null", "go build "+pkg
	if strings.HasSuffix(abs, "_test.go") {
		verb, shown = "test -c -o /dev/null", "go test -c "+pkg
	}
	q := func(s string) string {
		out, err := syntax.Quote(s, syntax.LangBash)
		if err != nil {
			return s
		}
		return out
	}
	command := "go -C " + q(mod) + " " + verb + " " + q(pkg)
	cmdInput, _ := json.Marshal(struct {
		Command string `json:"command"`
	}{command})
	v := s.cfg.Perms.Decide(s.cfg.Mode, "bash", false, cmdInput)
	if v.Action != perms.Allow {
		return ""
	}
	res, err := s.cfg.Shell.Run(shell.WithApproval(ctx, v.By), command, BuildTimeout)
	if err != nil || res.Stopped != "" || res.Exit == 0 {
		return ""
	}
	out, _ := perms.Redact(res.Output)
	// Escaped last: a line ending the note reshapes is not a line ending any more.
	return escapeControl(buildErrors(shown, out, mod))
}

// buildErrors is the note for a build that failed with output: its lines, without go's "# package" headers and with
// mod's absolute path cut, the first BuildErrorLines of them, each cut at buildLineRunes.
func buildErrors(shown, output, mod string) string {
	var lines []string
	for _, l := range strings.Split(output, "\n") {
		if strings.TrimSpace(l) == "" || strings.HasPrefix(l, "# ") {
			continue
		}
		l = strings.ReplaceAll(l, mod+string(filepath.Separator), "")
		if r := []rune(l); len(r) > buildLineRunes {
			l = string(r[:buildLineRunes]) + "…"
		}
		lines = append(lines, l)
	}
	if len(lines) == 0 {
		lines = []string{"(go printed no error lines)"}
	}
	more := ""
	if len(lines) > BuildErrorLines {
		more = fmt.Sprintf("\n… %d more lines", len(lines)-BuildErrorLines)
		lines = lines[:BuildErrorLines]
	}
	return buildOpen + shown + "):\n" + strings.Join(lines, "\n") + more + "]"
}

// moduleDir is the nearest directory from dir up to root that holds a go.mod, or "".
func moduleDir(root, dir string) string {
	for {
		if st, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil && st.Mode().IsRegular() {
			return dir
		}
		if dir == root || !strings.HasPrefix(dir, root+string(filepath.Separator)) {
			return ""
		}
		dir = filepath.Dir(dir)
	}
}
