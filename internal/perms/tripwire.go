package perms

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"mvdan.cc/sh/v3/syntax"
)

// The tripwire counts shapes that have no honest use: a command written so that it hides a run-a-program flag from the analysis, or one
// that plants a setting which runs a program later. A plain dangerous flag (`find . -exec`, `go test -exec`) is not one: it just asks,
// as before. A caller counts a call once however many shapes it holds, and what it does with the count (refusing, holding the session) is
// the loop's. The names of the shapes stay in this package: nothing shown to a model or in a refusal says what was caught.

// Tripwire returns the counted shapes in a shell command, none for an honest one.
func (e *Engine) Tripwire(command string) []string {
	var shapes []string
	add := func(s string) {
		for _, have := range shapes {
			if have == s {
				return
			}
		}
		shapes = append(shapes, s)
	}
	for _, c := range e.Analyze(command) {
		if c.Program == "" {
			continue
		}
		base := baseName(c.Program)
		for i, a := range c.Args {
			if c.Literal[i] {
				if hiddenToolFlag(base, a) {
					add("tool-flag")
				}
				// the flag and its value as two words: -ldflags '-extld=x'
				if i+1 < len(c.Args) && c.Literal[i+1] && base == "go" && isToolFlagName(a) && hasRunWord(c.Args[i+1]) {
					add("tool-flag")
				}
				continue
			}
			// A word bash decodes: its decoded text may be a flag the analysis could not see. Counted only when the decoding was needed.
			cands, ok := decodeCandidates(a)
			if !ok {
				continue
			}
			for _, cand := range cands {
				if cand != a && (runFlag(c.Program, cand) != "" || hiddenToolFlag(base, cand)) {
					add("escaped-flag")
				}
			}
		}
		if configWriteRuns(base, c) {
			add("config-write")
		}
	}
	if f, err := syntax.NewParser().Parse(strings.NewReader(command), ""); err == nil {
		syntax.Walk(f, func(n syntax.Node) bool {
			var assigns []*syntax.Assign
			switch n := n.(type) {
			case *syntax.CallExpr:
				assigns = n.Assigns
			case *syntax.DeclClause:
				assigns = n.Args
			}
			for _, a := range assigns {
				if a.Name != nil && a.Name.Value == "GOFLAGS" && a.Value != nil {
					if v, ok := literal(a.Value); ok && hasRunWord(v) {
						add("goflags")
					}
				}
			}
			return true
		})
	}
	return shapes
}

func baseName(prog string) string {
	if i := strings.LastIndexByte(prog, '/'); i >= 0 {
		return prog[i+1:]
	}
	return prog
}

// hasRunWord reports whether a flags string holds a word that makes the toolchain run a program: -extld (the linker's external linker,
// not -extldflags, which has honest uses such as -static), -toolexec, or -exec, bare or with =value.
func hasRunWord(v string) bool {
	words, ok := splitWords(v)
	if !ok {
		words = strings.Fields(v)
	}
	for _, w := range words {
		name, _, _ := strings.Cut(strings.TrimPrefix(w, "all="), "=")
		if name == "-extld" || name == "-toolexec" || name == "-exec" {
			return true
		}
	}
	return false
}

// isToolFlagName: a bare -ldflags, -gcflags, -asmflags or -gccgoflags, whose value is the next word.
func isToolFlagName(arg string) bool {
	name, _, has := flagParts(arg)
	return !has && (name == "ldflags" || name == "gcflags" || name == "asmflags" || name == "gccgoflags")
}

// hiddenToolFlag: a go -ldflags, -gcflags, -asmflags or -gccgoflags argument whose value holds a run word.
func hiddenToolFlag(prog, arg string) bool {
	if prog != "go" {
		return false
	}
	name, val, has := flagParts(arg)
	if !has || (name != "ldflags" && name != "gcflags" && name != "asmflags" && name != "gccgoflags") {
		return false
	}
	return hasRunWord(val)
}

// configWriteRuns: `git config` writing a key that makes git run a program later (a filesystem monitor, a hooks path, an ssh command,
// an external diff, a shell alias), or `go env -w` of a variable that changes what go runs (GOFLAGS, GOCACHEPROG, CC, CXX).
func configWriteRuns(prog string, c Simple) bool {
	args := c.Args
	switch prog {
	case "git":
		var rest []string
		skipped := false
		for _, a := range args {
			if !skipped && !strings.HasPrefix(a, "-") && a != "" {
				skipped = true
				if a != "config" {
					return false
				}
				continue
			}
			if skipped {
				rest = append(rest, a)
			}
		}
		if !skipped {
			return false
		}
		var pos []string
		for _, a := range rest {
			if a == "--get" || a == "--get-all" || a == "--get-regexp" || a == "--list" || a == "-l" || a == "--unset" || a == "--unset-all" {
				return false // reading, or removing, is not planting
			}
			if !strings.HasPrefix(a, "-") {
				pos = append(pos, a)
			}
		}
		if len(pos) < 2 {
			return false
		}
		key, val := strings.ToLower(pos[0]), pos[1]
		switch {
		case key == "core.fsmonitor", key == "core.hookspath", key == "core.sshcommand", key == "diff.external", key == "core.askpass":
			return true
		case strings.HasPrefix(key, "alias.") && strings.HasPrefix(val, "!"):
			return true
		}
	case "go":
		if len(args) == 0 || args[0] != "env" {
			return false
		}
		write := false
		for _, a := range args[1:] {
			if a == "-w" {
				write = true
				continue
			}
			if write {
				name, _, _ := strings.Cut(a, "=")
				switch name {
				case "GOFLAGS", "GOCACHEPROG", "CC", "CXX":
					return true
				}
			}
		}
	}
	return false
}

// decodeCandidates returns the texts bash would give the word src after it removes quotes, applies backslash escapes, decodes $'...'
// and expands braces (a comma list or a range of letters or numbers, up to 64 results). ok is false for a word it cannot decode.
func decodeCandidates(src string) ([]string, bool) {
	f, err := syntax.NewParser().Parse(strings.NewReader("x "+src), "")
	if err != nil || len(f.Stmts) != 1 {
		return nil, false
	}
	call, ok := f.Stmts[0].Cmd.(*syntax.CallExpr)
	if !ok || len(call.Args) != 2 {
		return nil, false
	}
	out := []string{""}
	for _, part := range call.Args[1].Parts {
		var piece []string
		switch p := part.(type) {
		case *syntax.Lit:
			s, ok := unescapeUnquoted(p.Value)
			if !ok {
				return nil, false
			}
			piece = braceExpand(s)
		case *syntax.SglQuoted:
			if p.Dollar {
				s, ok := ansiC(p.Value)
				if !ok {
					return nil, false
				}
				piece = []string{s}
			} else {
				piece = []string{p.Value}
			}
		case *syntax.DblQuoted:
			var b strings.Builder
			for _, q := range p.Parts {
				lit, ok := q.(*syntax.Lit)
				if !ok {
					return nil, false
				}
				b.WriteString(unescapeDouble(lit.Value))
			}
			piece = []string{b.String()}
		default:
			return nil, false
		}
		var next []string
		for _, o := range out {
			for _, pc := range piece {
				next = append(next, o+pc)
			}
		}
		if len(next) > 64 {
			return nil, false
		}
		out = next
	}
	return out, true
}

// unescapeUnquoted removes the backslashes of an unquoted word: \c is c.
func unescapeUnquoted(s string) (string, bool) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' {
			if i+1 >= len(s) {
				return "", false
			}
			i++
		}
		b.WriteByte(s[i])
	}
	return b.String(), true
}

// unescapeDouble applies a double-quoted word's escapes: a backslash before $, `, ", \ or a newline goes; elsewhere it stays.
func unescapeDouble(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) && strings.IndexByte("$`\"\\\n", s[i+1]) >= 0 {
			i++
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// ansiC decodes the inside of $'...'.
func ansiC(s string) (string, bool) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' {
			b.WriteByte(s[i])
			continue
		}
		i++
		if i >= len(s) {
			return "", false
		}
		switch c := s[i]; {
		case c == 'a':
			b.WriteByte(7)
		case c == 'b':
			b.WriteByte(8)
		case c == 'e' || c == 'E':
			b.WriteByte(27)
		case c == 'f':
			b.WriteByte(12)
		case c == 'n':
			b.WriteByte(10)
		case c == 'r':
			b.WriteByte(13)
		case c == 't':
			b.WriteByte(9)
		case c == 'v':
			b.WriteByte(11)
		case c == '\\' || c == '\'' || c == '"' || c == '?':
			b.WriteByte(c)
		case c >= '0' && c <= '7':
			j := i
			for j < len(s) && j < i+3 && s[j] >= '0' && s[j] <= '7' {
				j++
			}
			n, _ := strconv.ParseUint(s[i:j], 8, 16)
			b.WriteByte(byte(n))
			i = j - 1
		case c == 'x':
			j := i + 1
			for j < len(s) && j < i+3 && isHex(s[j]) {
				j++
			}
			if j == i+1 {
				return "", false
			}
			n, _ := strconv.ParseUint(s[i+1:j], 16, 8)
			b.WriteByte(byte(n))
			i = j - 1
		case c == 'u' || c == 'U':
			width := 4
			if c == 'U' {
				width = 8
			}
			j := i + 1
			for j < len(s) && j < i+1+width && isHex(s[j]) {
				j++
			}
			if j == i+1 {
				return "", false
			}
			n, _ := strconv.ParseUint(s[i+1:j], 16, 32)
			var buf [4]byte
			b.Write(buf[:utf8.EncodeRune(buf[:], rune(n))])
			i = j - 1
		default:
			return "", false
		}
	}
	return b.String(), true
}

func isHex(c byte) bool { return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' }

// braceExpand expands one level of comma braces, {a,b}, recursively, as bash does; a brace with no comma stays as it is.
func braceExpand(s string) []string {
	open := strings.IndexByte(s, '{')
	if open < 0 {
		return []string{s}
	}
	depth, comma, close := 0, []int{}, -1
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				close = i
			}
		case ',':
			if depth == 1 {
				comma = append(comma, i)
			}
		}
		if close >= 0 {
			break
		}
	}
	if close < 0 || len(comma) == 0 {
		return []string{s}
	}
	var alts []string
	prev := open + 1
	for _, c := range append(comma, close) {
		alts = append(alts, s[prev:c])
		prev = c + 1
	}
	var out []string
	for _, alt := range alts {
		for _, tail := range braceExpand(s[close+1:]) {
			for _, head := range braceExpand(alt) {
				out = append(out, s[:open]+head+tail)
			}
		}
		if len(out) > 64 {
			return out[:64]
		}
	}
	return out
}
