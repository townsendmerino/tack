package perms

import (
	"strings"
	"testing"
)

// The values below are made up in each format's shape; none is a real credential.
func TestRedact(t *testing.T) {
	cases := map[string]string{
		"private key":           "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEAAAAA\n-----END OPENSSH PRIVATE KEY-----",
		"AWS access key id":     "key=AKIAIOSFODNN7EXAMPLE",
		"AWS secret access key": "aws_secret_access_key = wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		"GitHub token":          "token: ghp_" + strings.Repeat("a1B2", 9),
		"Slack token":           "xoxb-123456789012-abcdefABCDEF",
		"Anthropic API key":     "ANTHROPIC_API_KEY=sk-ant-api03-" + strings.Repeat("x", 30),
		"OpenAI API key":        "OPENAI_API_KEY=sk-proj-" + strings.Repeat("Ab3", 12),
		"Google API key":        "AIza" + strings.Repeat("A", 35),
		"Stripe key":            "sk_live_" + strings.Repeat("9", 24),
		"JSON web token":        "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV",
	}
	for kind, in := range cases {
		out, kinds := Redact("before " + in + " after")
		if len(kinds) != 1 || kinds[0] != kind || !strings.Contains(out, "[redacted: "+kind+"]") ||
			!strings.HasPrefix(out, "before ") || !strings.HasSuffix(out, " after") {
			t.Errorf("%s: %q, kinds %v", kind, out, kinds)
		}
	}
	// Ordinary code and prose are left alone.
	for _, plain := range []string{
		`if strings.HasPrefix(name, "sk-") { return }`,
		"AKIA is the prefix AWS uses",
		"-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----",
		"ghp_short",
		"func eyJ() {}",
	} {
		if out, kinds := Redact(plain); out != plain || kinds != nil {
			t.Errorf("%q was changed: %q %v", plain, out, kinds)
		}
	}
}
