package perms

import (
	"regexp"
	"sort"
)

// secretFormats are well-known credential formats, each specific enough that ordinary code and prose do not match it.
// Order matters where one format is a prefix of another (an Anthropic key starts sk-, as OpenAI's do).
var secretFormats = []struct {
	kind string
	re   *regexp.Regexp
}{
	{"private key", regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z0-9 ]*PRIVATE KEY-----`)},
	{"AWS access key id", regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)},
	{"AWS secret access key", regexp.MustCompile(`(?i)aws_secret_access_key\s*[=:]\s*["']?[A-Za-z0-9/+=]{40}`)},
	{"GitHub token", regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{36,}|github_pat_[A-Za-z0-9_]{60,})`)},
	{"Slack token", regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`)},
	{"Anthropic API key", regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{20,}`)},
	{"OpenAI API key", regexp.MustCompile(`\bsk-(?:proj-)?[A-Za-z0-9_-]{32,}`)},
	{"Google API key", regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}`)},
	{"Stripe key", regexp.MustCompile(`\b[rs]k_live_[0-9A-Za-z]{20,}`)},
	{"JSON web token", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`)},
}

// Redact replaces every well-known credential in s with "[redacted: <kind>]" before the model sees it (DESIGN §6.6,
// "Secrets"), and returns the kinds it replaced, sorted, or nil. It is a second line behind the sandbox's read-deny
// list: it catches a key that was in a file the list does not name, or in a command's output.
func Redact(s string) (string, []string) {
	found := map[string]bool{}
	for _, f := range secretFormats {
		if f.re.MatchString(s) {
			found[f.kind] = true
			s = f.re.ReplaceAllLiteralString(s, "[redacted: "+f.kind+"]")
		}
	}
	if len(found) == 0 {
		return s, nil
	}
	kinds := make([]string, 0, len(found))
	for k := range found {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	return s, kinds
}
