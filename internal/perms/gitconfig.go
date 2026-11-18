package perms

import "strings"

// git config's long options, by what they do to the check: a flag takes no value, a value option takes the next word unless it is
// attached (--file=x), and a read or removal option means the command plants nothing. git accepts any unique prefix of a long option
// (--fil is --file); a prefix that fits two is an error in git and writes nothing.
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

// resolveGitConfigLong returns the option a long option's name stands for: the exact name, else the only option it is a prefix of.
func resolveGitConfigLong(name string) (kind int, ok bool) {
	if k, found := gitConfigLong[name]; found {
		return k, true
	}
	kind, n := 0, 0
	for full, k := range gitConfigLong {
		if strings.HasPrefix(full, name) {
			kind, n = k, n+1
		}
	}
	return kind, n == 1
}

// gitConfigPositionals returns the words of a git config command that are not options or an option's value: the key and the value, in
// order. ok is false when the command reads, removes or edits, when git would refuse an option, or when it is `get`, `list`, `unset`
// and the like as the subcommand: nothing is planted.
func gitConfigPositionals(rest []string) (pos []string, ok bool) {
	if len(rest) > 0 {
		for _, w := range gitConfigWords {
			if rest[0] == w {
				return nil, false
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
			return append(pos, rest[j+1:]...), true
		case strings.HasPrefix(a, "--"):
			name, _, attached := strings.Cut(a[2:], "=")
			kind, known := resolveGitConfigLong(name)
			switch {
			case !known:
				return nil, false // ambiguous or unknown: git refuses it
			case kind == optRead:
				return nil, false
			case kind == optValue && !attached:
				j++
			}
		case strings.HasPrefix(a, "-") && len(a) > 1:
			// a bundle of short options: -z is a flag, -f takes a value (the rest of the word if there is any, else the next word)
			for k := 1; k < len(a); k++ {
				switch a[k] {
				case 'f':
					if k == len(a)-1 {
						j++
					}
					k = len(a)
				case 'l', 'e':
					return nil, false
				}
			}
		default:
			pos = append(pos, a)
		}
	}
	return pos, true
}
