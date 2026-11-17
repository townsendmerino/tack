package perms

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// An allowlist of invocations for the programs a rule is most likely to cover (DESIGN §6.6, "What a rule may cover"). A rule matches a
// program and its leading arguments, so `go test` and `git status` matched anything after: -ldflags='-extld=./evil' made the linker
// run a program, `git grep -O id`, `git difftool -x id` and `git bisect run id` ran one, and no list of dangerous flags is ever
// complete. For a program with an entry here, a rule covers an invocation only if every subcommand and flag of it is one known not to
// run a program or change configuration. Anything else is not covered, so it asks. A program with no entry behaves as before.

// UnsafeInvocation says why a rule must not cover prog run with args (the arguments after the program, already expanded), or "" when it
// may, or when prog has no allowlist. The same function decides at approval and at run time, so the two cannot disagree.
func UnsafeInvocation(prog string, args []string) string {
	switch filepath.Base(prog) {
	case "go":
		return goUnsafe(args)
	case "git":
		return gitUnsafe(args)
	case "find":
		return findUnsafe(args)
	}
	return ""
}

// HasAllowlist reports whether prog has an allowlist: a rule for it covers only the invocations UnsafeInvocation accepts.
func HasAllowlist(prog string) bool {
	switch filepath.Base(prog) {
	case "go", "git", "find":
		return true
	}
	return false
}

func not(prog string, what string) string {
	return fmt.Sprintf("%s %s is not one a rule covers: it could run a program or change configuration", prog, what)
}

// flagParts splits a flag token into its name (dashes, and go's "test." prefix, removed) and value after "=".
func flagParts(tok string) (name, val string, has bool) {
	t := strings.TrimPrefix(strings.TrimPrefix(tok, "-"), "-")
	t = strings.TrimPrefix(t, "test.")
	name, val, has = strings.Cut(t, "=")
	return
}

func set(words ...string) map[string]bool {
	m := make(map[string]bool, len(words))
	for _, w := range words {
		m[w] = true
	}
	return m
}

// splitWords splits a flag value on spaces, honouring single and double quotes, as go does for -ldflags and the like. A backslash or an
// unbalanced quote is not understood, so the value is refused.
func splitWords(s string) ([]string, bool) {
	var words []string
	var cur strings.Builder
	in, quote := false, byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\\':
			return nil, false
		case quote != 0:
			if c == quote {
				quote = 0
			} else {
				cur.WriteByte(c)
			}
		case c == '\'' || c == '"':
			quote, in = c, true
		case c == ' ' || c == '\t':
			if in {
				words = append(words, cur.String())
				cur.Reset()
				in = false
			}
		default:
			cur.WriteByte(c)
			in = true
		}
	}
	if quote != 0 {
		return nil, false
	}
	if in {
		words = append(words, cur.String())
	}
	return words, true
}

// ---- go ----

var (
	goBoolFlags = set("a", "n", "x", "v", "work", "race", "msan", "asan", "trimpath", "modcacherw", "linkshared", "c", "short", "failfast",
		"json", "cover", "benchmem", "e", "deps", "m", "find", "versions", "compiled", "export", "test", "buildvcs", "i", "changed")
	goValueFlags = set("p", "tags", "o", "mod", "buildmode", "compiler", "pgo", "run", "bench", "benchtime", "count", "timeout", "parallel",
		"cpu", "coverprofile", "covermode", "coverpkg", "skip", "shuffle", "list", "fuzz", "fuzztime", "f", "vet")
	goToolFlags = set("ldflags", "gcflags", "asmflags")
)

func goUnsafe(args []string) string {
	if len(args) == 0 {
		return ""
	}
	sub := args[0]
	switch sub {
	case "version":
		return ""
	case "env":
		for _, a := range args[1:] {
			name, _, _ := flagParts(a)
			switch {
			case strings.HasPrefix(a, "-"):
				if name == "w" || name == "u" {
					return "go env -" + name + " writes the go environment, which persists into later go runs, and no rule covers it"
				}
				if name != "json" && name != "changed" {
					return not("go", "env "+a)
				}
			case !regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`).MatchString(a):
				return not("go", "env "+a) // NAME=value is a write too
			}
		}
		return ""
	case "build", "test", "vet", "run", "list":
	default:
		return not("go", sub)
	}
	rest := args[1:]
	for i := 0; i < len(rest); i++ {
		tok := rest[i]
		if (sub == "test" && tok == "-args") || tok == "--" {
			return "" // what follows belongs to the program being run, not to go
		}
		if !strings.HasPrefix(tok, "-") || tok == "-" {
			if sub == "run" {
				return "" // the first package or file ends go's flags; the rest are the program's
			}
			continue
		}
		name, val, has := flagParts(tok)
		switch {
		case goBoolFlags[name]:
			if has && val != "true" && val != "false" && !(name == "buildvcs" && val == "auto") {
				return not("go", sub+" "+tok)
			}
		case goValueFlags[name]:
			if !has {
				if i++; i >= len(rest) {
					return not("go", sub+" "+tok)
				}
				val = rest[i]
			}
			if (name == "mod" && val != "readonly" && val != "vendor" && val != "mod") || (name == "compiler" && val != "gc") {
				return not("go", sub+" "+tok+" "+val)
			}
		case goToolFlags[name]:
			if !has {
				if i++; i >= len(rest) {
					return not("go", sub+" "+tok)
				}
				val = rest[i]
			}
			if r := goToolFlagValue(name, val); r != "" {
				return r
			}
		default:
			return not("go", sub+" "+tok)
		}
	}
	return ""
}

// goToolFlagValue looks inside -ldflags, -gcflags and -asmflags: each word of the value must be one that cannot make the toolchain run a
// program (-extld, -extldflags, -r and the rest of the linker's reach are not on the list). -gccgoflags is never covered.
func goToolFlagValue(flag, val string) string {
	words, ok := splitWords(val)
	if !ok {
		return fmt.Sprintf("go -%s has a value this check cannot read, which no rule covers", flag)
	}
	bad := func(w string) string {
		return fmt.Sprintf("go -%s %s is not one a rule covers: it could make the toolchain run a program", flag, w)
	}
	for i := 0; i < len(words); i++ {
		w := words[i]
		if flag != "ldflags" && !strings.HasPrefix(w, "-") { // gcflags and asmflags may start with a package pattern: all=-N
			if _, rest, found := strings.Cut(w, "="); found {
				w = rest
			} else {
				return bad(w)
			}
		}
		name, _, _ := strings.Cut(w, "=")
		switch flag {
		case "ldflags":
			switch {
			case w == "-s" || w == "-w" || w == "-v":
			case w == "-X":
				if i++; i >= len(words) {
					return bad(w)
				}
			case strings.HasPrefix(w, "-X=") || strings.HasPrefix(w, "-buildid=") || strings.HasPrefix(w, "-compressdwarf=") || w == "-linkmode=internal":
			default:
				return bad(w)
			}
		case "gcflags":
			switch {
			case w == "-N" || w == "-l" || w == "-m" || w == "-S" || w == "-e" || w == "-B" || w == "-shared" || w == "-dwarf":
			case name == "-m" || name == "-d" || name == "-dwarf" || name == "-c":
			default:
				return bad(w)
			}
		case "asmflags":
			switch {
			case w == "-shared" || w == "-S" || w == "-e":
			case name == "-trimpath":
			case w == "-D" || w == "-I":
				if i++; i >= len(words) {
					return bad(w)
				}
			default:
				return bad(w)
			}
		}
	}
	return ""
}

// ---- git ----

var (
	gitGlobalOK = set("--no-pager", "--no-optional-locks", "-P")
	// flags shared by the commands that show history and changes
	gitShowBool = set("-p", "-u", "--patch", "--no-patch", "-s", "--stat", "--shortstat", "--numstat", "--dirstat", "--name-only", "--name-status",
		"--raw", "--summary", "--oneline", "--graph", "--decorate", "--no-decorate", "--abbrev-commit", "--no-abbrev-commit", "--color", "--no-color",
		"--cached", "--staged", "-w", "--ignore-all-space", "-b", "--ignore-space-change", "--ignore-space-at-eol", "-R", "--reverse", "--follow",
		"--first-parent", "--no-merges", "--merges", "--all", "--branches", "--tags", "--remotes", "--full-history", "--simplify-merges",
		"--date-order", "--topo-order", "--author-date-order", "--no-ext-diff", "--no-textconv", "--minimal", "--patience", "--histogram",
		"--check", "--left-right", "--cherry-pick", "--cherry", "--boundary", "--no-walk", "--source", "--show-signature", "--no-color-moved",
		"-M", "-C", "--find-renames", "--find-copies", "--no-renames", "--merge-base", "--ignore-blank-lines", "--exit-code", "--quiet", "--full-index",
		"--abbrev", "-z", "--root", "--no-abbrev", "--stdin")
	gitShowVal = set("--format", "--pretty", "--date", "--since", "--until", "--after", "--before", "--author", "--committer", "--grep",
		"--max-count", "-n", "-S", "-G", "--diff-filter", "--unified", "-U", "--relative", "--stat-width", "--skip", "--abbrev", "--color-moved",
		"--word-diff", "--word-diff-regex", "--diff-algorithm", "--find-object", "--since-as-filter", "--min-parents", "--max-parents")
	gitStatusBool = set("-s", "--short", "-b", "--branch", "--porcelain", "--long", "--ignored", "-u", "--untracked-files", "--no-renames", "-z",
		"--ahead-behind", "--no-ahead-behind", "--column", "--no-column", "--show-stash", "-v", "--verbose")
	gitGrepBool = set("-n", "-i", "-v", "-w", "-l", "-L", "-c", "-E", "-F", "-P", "-G", "--cached", "--untracked", "-h", "-H", "-I", "--count",
		"--files-with-matches", "--files-without-match", "-a", "--heading", "--break", "--full-name", "--and", "--or", "--not", "--no-index",
		"--exclude-standard", "-p", "--show-function", "-W", "--function-context", "-o", "--only-matching", "--no-color", "--color", "-q", "--quiet",
		"-z", "--null", "--line-number", "--column", "--ignore-case", "--invert-match", "--word-regexp", "--fixed-strings", "--extended-regexp",
		"--perl-regexp", "--basic-regexp", "-r", "--recursive", "--no-recursive", "--text", "--no-textconv", "-m", "--max-count", "-A", "-B", "-C")
	gitGrepVal  = set("-e", "-A", "-B", "-C", "--max-depth", "-m", "--max-count")
	gitListBool = set("-a", "--all", "-r", "--remotes", "-v", "-vv", "--verbose", "--list", "-l", "--show-current", "--color", "--no-color",
		"--merged", "--no-merged", "--contains", "--no-contains", "--abbrev", "--no-abbrev", "-i", "--ignore-case", "--column", "--no-column", "--points-at",
		"--sort", "--format", "-n", "--omit-empty", "--no-merged")
	gitListVal      = set("--sort", "--format", "--merged", "--no-merged", "--contains", "--no-contains", "--points-at", "--abbrev", "-n")
	gitMiscBool     = set("--short", "--verify", "-q", "--quiet", "--abbrev-ref", "--symbolic-full-name", "--show-toplevel", "--git-dir", "--is-inside-work-tree", "--show-prefix", "--show-cdup", "--absolute-git-dir", "--git-common-dir", "--tags", "--all", "--branches", "--remotes", "--no-merges", "--count", "--max-count", "-n", "--oneline", "--first-parent", "--reverse", "--format", "--pretty", "--name-only", "--full-name", "--cached", "-c", "--others", "-o", "--ignored", "--exclude-standard", "-s", "--stage", "--deleted", "-d", "--modified", "-m", "--unmerged", "-u", "-z", "-t", "-v", "--long", "--abbrev", "--dirty", "--always", "--exact-match", "--candidates", "--first-parent", "--contains", "--match", "--exclude", "--hash", "-p", "-e", "--batch", "--batch-check", "--batch-all-objects", "--is-ancestor", "--fork-point", "--octopus", "--independent", "--name-status", "--refs", "--head", "-d", "--dereference", "-r", "--full-tree", "--object-only", "--no-abbrev")
	gitConfigReadOK = set("--get", "--get-all", "--get-regexp", "--list", "-l", "--get-urlmatch", "--name-only", "--show-origin", "--show-scope", "-z", "--null", "--local", "--global", "--system", "--worktree", "--type", "--includes", "--no-includes", "--default", "--bool", "--int", "--path")
)

var gitSubShow = set("diff", "show", "log", "shortlog", "diff-tree", "blame", "whatchanged")
var gitSubMisc = set("rev-parse", "rev-list", "ls-files", "ls-tree", "describe", "name-rev", "merge-base", "show-ref", "cat-file", "for-each-ref", "count-objects", "var", "version", "check-ignore", "diff-index", "diff-files", "ls-remote")

func gitUnsafe(args []string) string {
	i := 0
	for i < len(args) && strings.HasPrefix(args[i], "-") {
		if !gitGlobalOK[args[i]] {
			return not("git", args[i]+" before the subcommand")
		}
		i++
	}
	if i >= len(args) {
		return ""
	}
	sub, rest := args[i], args[i+1:]
	bad := func(tok string) string { return not("git", sub+" "+tok) }
	switch {
	case sub == "status":
		return gitFlags(sub, rest, gitStatusBool, nil, false)
	case sub == "grep":
		return gitFlags(sub, rest, gitGrepBool, gitGrepVal, false)
	case gitSubShow[sub]:
		return gitFlags(sub, rest, gitShowBool, gitShowVal, false)
	case gitSubMisc[sub]:
		return gitFlags(sub, rest, gitMiscBool, gitMiscBool, false)
	case sub == "branch", sub == "tag":
		// a bare name creates one: names are allowed only with a listing flag
		listing := false
		for _, a := range rest {
			if a == "--list" || a == "-l" || strings.HasPrefix(a, "--contains") || strings.HasPrefix(a, "--merged") || strings.HasPrefix(a, "--no-merged") || a == "--show-current" || strings.HasPrefix(a, "--points-at") {
				listing = true
			}
		}
		return gitFlags(sub, rest, gitListBool, gitListVal, !listing)
	case sub == "remote":
		if len(rest) == 0 || (len(rest) == 1 && (rest[0] == "-v" || rest[0] == "--verbose")) {
			return ""
		}
		if rest[0] == "show" || rest[0] == "get-url" {
			for _, a := range rest[1:] {
				if strings.HasPrefix(a, "-") && a != "-n" {
					return bad(a)
				}
			}
			return ""
		}
		return bad(rest[0])
	case sub == "stash":
		if len(rest) > 0 && (rest[0] == "list" || rest[0] == "show") {
			return gitFlags(sub, rest[1:], gitShowBool, gitShowVal, false)
		}
		return not("git", "stash (only list and show)")
	case sub == "reflog":
		if len(rest) == 0 || rest[0] == "show" {
			if len(rest) > 0 {
				rest = rest[1:]
			}
			return gitFlags(sub, rest, gitShowBool, gitShowVal, false)
		}
		return bad(rest[0])
	case sub == "config":
		read := false
		for _, a := range rest {
			if strings.HasPrefix(a, "-") {
				name, _, _ := strings.Cut(a, "=")
				if !gitConfigReadOK[name] {
					return "git config " + a + " could write configuration, which can run a program later, and no rule covers it"
				}
				if name == "--get" || name == "--get-all" || name == "--get-regexp" || name == "--list" || name == "-l" || name == "--get-urlmatch" {
					read = true
				}
			}
		}
		if !read {
			return "git config without --get or --list could write configuration, which can run a program later, and no rule covers it"
		}
		return ""
	}
	return not("git", sub)
}

// gitFlags checks the flags of one git subcommand against its lists. Names (positional) are refused when noNames is set. A flag in val
// takes a value, after "=" or as the next word; a single-dash bundle (-sb, -nU3) is allowed only if every letter is a known bool flag.
func gitFlags(sub string, rest []string, bools, vals map[string]bool, noNames bool) string {
	for i := 0; i < len(rest); i++ {
		tok := rest[i]
		if tok == "--" {
			return "" // what follows are paths
		}
		if !strings.HasPrefix(tok, "-") || tok == "-" {
			if noNames {
				return not("git", sub+" with a name: it would create one")
			}
			continue
		}
		name, val, has := strings.Cut(tok, "=")
		switch {
		case vals[name]:
			if !has {
				if i+1 < len(rest) && !strings.HasPrefix(rest[i+1], "-") {
					i++
				}
			}
			_ = val
		case bools[name]:
		case isCountFlag(tok):
		case !strings.HasPrefix(tok, "--") && len(tok) > 2 && bundleOK(tok, bools):
		default:
			return not("git", sub+" "+tok)
		}
	}
	return ""
}

var countFlag = regexp.MustCompile(`^(-[0-9]+|-U[0-9]+|-n[0-9]+|-M[0-9]*%?|-C[0-9]*%?|-[ABC][0-9]+)$`)

func isCountFlag(tok string) bool { return countFlag.MatchString(tok) }

func bundleOK(tok string, bools map[string]bool) bool {
	for _, c := range tok[1:] {
		if !bools["-"+string(c)] {
			return false
		}
	}
	return true
}

// ---- find ----

var (
	findBool = set("-print", "-print0", "-prune", "-empty", "-readable", "-writable", "-executable", "-true", "-false", "-depth", "-mount", "-xdev",
		"-nogroup", "-nouser", "-quit", "-ls", "-follow", "-L", "-H", "-P", "-o", "-a", "-not", "!", "(", ")", "-and", "-or", "-d", "-noleaf")
	findVal = set("-name", "-iname", "-path", "-ipath", "-wholename", "-iwholename", "-regex", "-iregex", "-lname", "-ilname", "-type", "-xtype",
		"-maxdepth", "-mindepth", "-mtime", "-atime", "-ctime", "-mmin", "-amin", "-cmin", "-newer", "-anewer", "-cnewer", "-size", "-perm", "-user",
		"-group", "-uid", "-gid", "-links", "-inum", "-samefile", "-regextype", "-fstype", "-used")
)

func findUnsafe(args []string) string {
	for i := 0; i < len(args); i++ {
		tok := args[i]
		switch {
		case findVal[tok]:
			i++ // its value, whatever it looks like
		case findBool[tok]:
		case regexp.MustCompile(`^-newer[acBmt][acBmt]$`).MatchString(tok):
			i++
		case strings.HasPrefix(tok, "-") && len(tok) > 1:
			return not("find", tok)
		}
	}
	return ""
}
