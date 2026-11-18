package perms

import "strings"

// git config's long options, by what they do to the check: a flag takes no value, a value option takes the next word unless it is
// attached (--file=x), and a read or removal option means the command plants nothing. git accepts any unique prefix of a long option
// (--fil is --file), and --no-<flag> negates one. The table is the union of git config's modes (the subcommands have fewer options than
// the legacy form), so a prefix that is unique to git in one mode can be ambiguous here: anything the table cannot resolve fails
// closed (gitConfigArgs).
var gitConfigLong = map[string]int{
	"global": optFlag, "system": optFlag, "local": optFlag, "worktree": optFlag, "add": optFlag, "replace-all": optFlag, "append": optFlag,
	"all": optFlag, "fixed-value": optFlag, "null": optFlag, "name-only": optFlag, "includes": optFlag, "no-includes": optFlag,
	"show-origin": optFlag, "show-scope": optFlag, "bool": optFlag, "int": optFlag, "bool-or-int": optFlag, "bool-or-str": optFlag,
	"path": optFlag, "expiry-date": optFlag, "no-type": optFlag, "no-comment": optFlag,
	"file": optValue, "blob": optValue, "default": optValue, "type": optValue, "comment": optValue, "value": optValue,
	"get": optRead, "get-all": optRead, "get-regexp": optRead, "get-urlmatch": optRead, "list": optRead, "unset": optRead,
	"unset-all": optRead, "edit": optRead, "remove-section": optRead, "rename-section": optRead, "get-color": optRead, "get-colorbool": optRead,
}

const (
	optFlag = iota
	optValue
	optRead
)

// gitConfigWords are git config's subcommands (git 2.46): words with that meaning only as the first word after config, and then
// only before any option. After an option, or after the key, the same word is a key or a value.
var gitConfigWords = []string{"get", "list", "unset", "edit", "rename-section", "remove-section"}

// gitGlobalValueOptions are git's own options, before the subcommand, whose value is the next word. (--exec-path and --super-prefix take
// one only attached.)
var gitGlobalValueOptions = []string{"-C", "--git-dir", "--work-tree", "--namespace", "--config-env", "--attr-source", "--shallow-file"}

// gitGlobalFlags are git's own options, before the subcommand, that take no value: a leading "-" word that is in neither list is one this
// code has not seen, and may take a value.
var gitGlobalFlags = []string{
	"--bare", "--no-pager", "-p", "--paginate", "-P", "--no-replace-objects", "--literal-pathspecs", "--glob-pathspecs", "--noglob-pathspecs",
	"--icase-pathspecs", "--no-optional-locks", "--no-lazy-fetch", "--no-advice", "--no-glob-pathspecs", "--exec-path", "--html-path", "--man-path",
	"--info-path", "--version", "--help", "-h", "-v", "--list-cmds",
}

// resolveGitConfigLong returns the option a long option's name stands for: the exact name, else the only option it is a prefix of, else
// (for --no-<name>) a negation of one, which takes no value. known is false for a name it cannot resolve to exactly one option.
func resolveGitConfigLong(name string) (kind int, known bool) {
	if k, found := gitConfigLong[name]; found {
		return k, true
	}
	if k, ok := uniquePrefix(name); ok {
		return k, true
	}
	if rest, neg := strings.CutPrefix(name, "no-"); neg && rest != "" {
		if _, ok := uniquePrefix(rest); ok {
			return optFlag, true
		}
		if _, found := gitConfigLong[rest]; found {
			return optFlag, true
		}
	}
	return 0, false
}

func uniquePrefix(name string) (kind int, ok bool) {
	n := 0
	for full, k := range gitConfigLong {
		if strings.HasPrefix(full, name) {
			kind, n = k, n+1
		}
	}
	return kind, n == 1
}

// gitConfigArgs reads the arguments of a git command line that may be a git config write: git's own options, the subcommand (or an alias
// -c names that expands to config), and git config's options. It returns the key and value words (pos), every word after config
// (words), and:
//
//	isConfig  the command is git config (or an alias of it): false means nothing to check;
//	closed    a spelling it does not know was met (an option, a prefix, a short letter, a global option), so pos cannot be trusted: the caller
//	          counts the command when a word in words names a counted key, whatever the value. Over-counting costs the owner a release;
//	          under-counting is the hole.
//
// A command that reads, removes or edits has isConfig set, no pos and closed false.
func gitConfigArgs(args []string) (pos, words []string, isConfig, closed bool) {
	aliases := map[string][]string{} // alias name -> its words, from -c alias.<name>=<value>
	i := 0
	unknownGlobal := false
	for i < len(args) && strings.HasPrefix(args[i], "-") {
		a := args[i]
		switch {
		case a == "-c":
			if i+1 < len(args) {
				if k, v, ok := strings.Cut(args[i+1], "="); ok && strings.HasPrefix(strings.ToLower(k), "alias.") {
					aliases[k[len("alias."):]] = strings.Fields(v)
				}
			}
			i++
		case containsString(gitGlobalValueOptions, a):
			i++ // the option's value is the next word
		case containsString(gitGlobalFlags, a), strings.Contains(a, "="):
		default:
			unknownGlobal = true // it may take a value
		}
		i++
	}
	if i >= len(args) {
		return nil, nil, false, false
	}
	sub, rest := args[i], args[i+1:]
	if exp, ok := aliases[sub]; ok && len(exp) > 0 && exp[0] == "config" {
		sub, rest = "config", append(append([]string(nil), exp[1:]...), rest...)
	}
	if sub != "config" {
		if !unknownGlobal {
			return nil, nil, false, false
		}
		// An option before the subcommand that this code has not seen may have taken the next word, which would have been the subcommand.
		for j, w := range args[i:] {
			if w == "config" {
				return gitConfigPositionals(args[i+j+1:], true)
			}
		}
		return nil, nil, false, false
	}
	return gitConfigPositionals(rest, unknownGlobal)
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// gitConfigPositionals is the part of gitConfigArgs after the config word. unknown starts it closed.
func gitConfigPositionals(rest []string, unknown bool) (pos, words []string, isConfig, closed bool) {
	words = rest
	if len(rest) > 0 {
		for _, w := range gitConfigWords {
			if rest[0] == w {
				return nil, nil, true, false
			}
		}
		if rest[0] == "set" {
			rest = rest[1:]
		}
	}
	for j := 0; j < len(rest); j++ {
		a := rest[j]
		switch {
		case a == "--":
			pos = append(pos, rest[j+1:]...)
			return pos, words, true, unknown
		case strings.HasPrefix(a, "--"):
			name, _, attached := strings.Cut(a[2:], "=")
			kind, known := resolveGitConfigLong(name)
			switch {
			case !known:
				unknown = true // not an option this table can resolve: it may take a value
			case kind == optRead:
				return nil, nil, true, false
			case kind == optValue && !attached:
				j++
			}
		case strings.HasPrefix(a, "-") && len(a) > 1:
			// a bundle of short options: -z is a flag, -f and -t take a value (the rest of the word if there is any, else the next word)
		bundle:
			for k := 1; k < len(a); k++ {
				switch a[k] {
				case 'f', 't':
					if k == len(a)-1 {
						j++
					}
					break bundle
				case 'z':
				case 'l', 'e':
					return nil, nil, true, false
				default:
					unknown = true
				}
			}
		default:
			pos = append(pos, a)
		}
	}
	return pos, words, true, unknown
}

// namesCountedKey reports whether any word is the name of a setting the tripwire counts a write of, in any letter case; an alias
// counts when some word starts with "!". It is the fail-closed test for a command whose key and value cannot be told apart.
func namesCountedKey(words []string) bool {
	shell := false
	for _, w := range words {
		if strings.HasPrefix(w, "!") {
			shell = true
		}
	}
	for _, w := range words {
		k := strings.ToLower(w)
		switch {
		case countedKey(k):
			return true
		case shell && strings.HasPrefix(k, "alias."):
			return true
		}
	}
	return false
}

// countedKey reports whether a lower-cased git config key is one whose write is counted whatever the value's shape needs further reading.
func countedKey(key string) bool {
	switch key {
	case "core.fsmonitor", "core.hookspath", "core.pager", "diff.external":
		return true
	}
	return strings.HasPrefix(key, "diff.") && strings.HasSuffix(key, ".textconv")
}
