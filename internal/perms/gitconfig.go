package perms

import (
	"slices"
	"strings"
)

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
	aliasSeen := false // an alias.* was set in -c or --config-env on this command line
	for i < len(args) && strings.HasPrefix(args[i], "-") {
		a := args[i]
		switch {
		case a == "-c" || a == "--config-env":
			if i+1 < len(args) {
				if k, v, ok := strings.Cut(args[i+1], "="); ok && strings.HasPrefix(strings.ToLower(k), "alias.") {
					aliasSeen = true
					if a == "-c" {
						aliases[strings.ToLower(k[len("alias."):])] = strings.Fields(v)
					}
				}
			}
			i++
		case strings.HasPrefix(a, "--config-env="):
			if k, _, ok := strings.Cut(a[len("--config-env="):], "="); ok && strings.HasPrefix(strings.ToLower(k), "alias.") {
				aliasSeen = true // its value is an environment variable's, which is not read here
			}
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
	if exp, ok := aliases[strings.ToLower(sub)]; ok && len(exp) > 0 && exp[0] == "config" {
		sub, rest = "config", append(append([]string(nil), exp[1:]...), rest...)
	}
	if sub != "config" {
		// An alias this code cannot follow (a chain, a quoted value, one from the environment or from the owner's own config file) or a
		// command that is not a git builtin (a stored alias, an external git-foo) may be config: the words after it are checked for a
		// counted key, whatever they are.
		if aliasSeen || !gitBuiltins[sub] {
			words := rest
			if unknownGlobal {
				words = args[i:] // the subcommand itself may be an option's value
			}
			return nil, words, true, true
		}
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
		case strings.HasPrefix(k, "alias.") && slices.ContainsFunc(words, aliasIsConfig):
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

// aliasIsConfig reports whether an alias's value can expand to git config: the word config appears anywhere in it, in any letter case,
// since git's own options come before the command (`-c a=b config`, `--no-pager config`) and the command word is not always the first.
// An alias that is only config's neighbour (configure, reconfig) is not.
func aliasIsConfig(val string) bool {
	return slices.ContainsFunc(strings.Fields(val), func(w string) bool { return strings.EqualFold(w, "config") })
}

// gitBuiltins is git's list of built-in commands, from `git --list-cmds=builtins` of git 2.53.0 and nothing else: a subcommand on it is that
// command and git does not let an alias shadow it. Any other word may be an alias a config file defines (also one named like a program git
// ships and does not build in: gui, citool, send-email, scalar, submodule, mergetool, filter-branch, request-pull, daemon, instaweb,
// imap-send, http-fetch, http-push, which an alias beats when the program is not installed) or an external git-<name>. A built-in
// command a newer git adds is missing from the list, and only over-counts. TestTheBuiltinListHoldsOnlyBuiltins checks the list against the
// installed git.
var gitBuiltins = func() map[string]bool {
	m := map[string]bool{}
	for _, c := range strings.Fields(`
		add am annotate apply archive backfill bisect blame branch bugreport bundle cat-file check-attr check-ignore check-mailmap
		check-ref-format checkout checkout--worker checkout-index cherry cherry-pick clean clone column commit commit-graph
		commit-tree config count-objects credential credential-cache credential-cache--daemon credential-store describe diagnose
		diff diff-files diff-index diff-pairs diff-tree difftool fast-export fast-import fetch fetch-pack fmt-merge-msg for-each-ref
		for-each-repo format-patch fsck fsck-objects fsmonitor--daemon gc get-tar-commit-id grep hash-object help hook index-pack
		init init-db interpret-trailers last-modified log ls-files ls-remote ls-tree mailinfo mailsplit maintenance merge merge-base
		merge-file merge-index merge-ours merge-recursive merge-recursive-ours merge-recursive-theirs merge-subtree merge-tree mktag
		mktree multi-pack-index mv name-rev notes pack-objects pack-redundant pack-refs patch-id pickaxe prune prune-packed pull
		push range-diff read-tree rebase receive-pack reflog refs remote remote-ext remote-fd repack replace replay repo rerere
		reset restore rev-list rev-parse revert rm send-pack shortlog show show-branch show-index show-ref sparse-checkout stage
		stash status stripspace submodule--helper switch symbolic-ref tag unpack-file unpack-objects update-index update-ref
		update-server-info upload-archive upload-archive--writer upload-pack var verify-commit verify-pack verify-tag version
		whatchanged worktree write-tree`) {
		m[c] = true
	}
	return m
}()
