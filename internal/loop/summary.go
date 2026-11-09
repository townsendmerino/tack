package loop

import (
	"encoding/json"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/townsendmerino/tack/internal/proto"
)

// The summarizing step's mechanical parts (DESIGN §7.6, "How the summary is written"): the anchor list, taken from the
// turns being compacted with no model call and copied exactly; the fallback summary, used when the summarizer fails or
// returns text without the fixed headings; and the check of those headings. They read only logged events, so a rebuilt
// session derives the same text.

// SummaryHeadings are the summary's fixed headings, each a "## " line, in this order.
var SummaryHeadings = []string{"Goal", "Constraints", "Done", "In progress", "Decisions", "Files", "Next steps"}

const (
	// anchorMax is how many anchors of each kind are kept, the first found.
	anchorMax = 40
	// anchorErrorBytes is how much of an error line is kept.
	anchorErrorBytes = 160
)

// Anchors are the exact strings a summary must not lose, by kind, each in order of first appearance and once.
type Anchors struct {
	Paths, Symbols, Commits, Errors []string
}

var (
	// A path: one with a slash, or a bare file name with a common extension.
	anchorPath = regexp.MustCompile(`(?:[A-Za-z0-9_.-]+/)+(?:[A-Za-z0-9_.-]*[A-Za-z0-9_])?|\b[A-Za-z0-9_-]+\.(?:go|mod|sum|md|toml|json|ya?ml|swift|sh|txt|c|h|rs|ts|js|py)\b`)
	// A symbol: a qualified name (pkg.Name, Type.Method) or a mixed-case identifier (NewBudget, TestFoo).
	anchorSymbol = regexp.MustCompile(`\b[A-Za-z_][A-Za-z0-9_]*\.[A-Z][A-Za-z0-9_]*\b|\b[A-Z][a-z0-9]+[A-Z][A-Za-z0-9_]*\b`)
	// A commit hash: 7 to 40 lower-case hex digits, with a digit and a letter among them.
	anchorCommit = regexp.MustCompile(`\b[0-9a-f]{7,40}\b`)
	// An error line: one naming an error, a failure or a panic.
	anchorError = regexp.MustCompile(`(?i)\b(?:error|errors|panic|fail|failed|failure)\b|^--- FAIL`)
)

// AnchorsOf is the anchor list of evs: user messages, the model's text, tool calls' inputs and tool results, in log
// order. A tool call's path arguments are taken whole; every other text is searched by pattern. A failed call's
// result gives its first line as an error.
func AnchorsOf(evs []proto.Event) Anchors {
	var a Anchors
	text := func(s string) {
		for _, p := range anchorPath.FindAllString(s, -1) {
			a.Paths = addAnchor(a.Paths, p)
		}
		for _, sym := range anchorSymbol.FindAllString(s, -1) {
			a.Symbols = addAnchor(a.Symbols, sym)
		}
		for _, h := range anchorCommit.FindAllString(s, -1) {
			if strings.ContainsAny(h, "0123456789") && strings.ContainsAny(h, "abcdef") {
				a.Commits = addAnchor(a.Commits, h)
			}
		}
	}
	errLine := func(l string) {
		if l = strings.TrimSpace(l); l != "" {
			a.Errors = addAnchor(a.Errors, cut(l, anchorErrorBytes))
		}
	}
	for _, ev := range evs {
		v, err := ev.Decode()
		if err != nil {
			continue
		}
		switch ev.Type {
		case proto.EvUserMessage:
			for _, b := range v.(proto.UserMessage).Content {
				text(b.Text)
			}
		case proto.EvAssistantMessage:
			for _, b := range v.(proto.AssistantMessage).Content {
				if b.Type == "text" {
					text(b.Text)
				}
			}
		case proto.EvToolCall:
			var in map[string]any
			_ = json.Unmarshal(v.(proto.ToolCall).Input, &in)
			for _, k := range []string{"path", "file_path"} {
				if p, ok := in[k].(string); ok && p != "" {
					a.Paths = addAnchor(a.Paths, p)
				}
			}
			for _, k := range slices.Sorted(maps.Keys(in)) {
				if s, ok := in[k].(string); ok {
					text(s)
				}
			}
		case proto.EvToolFinished:
			r := v.(proto.ToolFinished)
			text(r.Result)
			if r.Outcome != proto.OutcomeOK {
				errLine(firstLine(r.Result))
			}
			for l := range strings.SplitSeq(r.Result, "\n") {
				if anchorError.MatchString(l) {
					errLine(l)
				}
			}
		}
	}
	return a
}

func addAnchor(list []string, s string) []string {
	if len(list) == anchorMax || slices.Contains(list, s) {
		return list
	}
	return append(list, s)
}

// Text is the anchor list as a summary's last section, "" when there is none.
func (a Anchors) Text() string {
	var b strings.Builder
	line := func(name string, list []string) {
		if len(list) > 0 {
			b.WriteString(name + ": " + strings.Join(list, ", ") + "\n")
		}
	}
	line("Paths", a.Paths)
	line("Symbols", a.Symbols)
	line("Commits", a.Commits)
	if len(a.Errors) > 0 {
		b.WriteString("Errors:\n")
		for _, e := range a.Errors {
			b.WriteString("- " + e + "\n")
		}
	}
	if b.Len() == 0 {
		return ""
	}
	return "## Anchors (exact)\n\n" + strings.TrimRight(b.String(), "\n")
}

// FallbackSummary is the summary of evs built without the summarizer: every user message word for word, the task (the
// first) under its own heading, then the anchor list. It never drops a turn with nothing in its place.
func FallbackSummary(evs []proto.Event) string { return summaryText(evs, "") }

// HasSummaryHeadings reports whether text has each of SummaryHeadings as a "## " line, in order. A summarizer's text
// without them is not used; the fallback is.
func HasSummaryHeadings(text string) bool {
	next := 0
	for l := range strings.SplitSeq(text, "\n") {
		h, ok := strings.CutPrefix(strings.TrimRight(l, " \t\r"), "## ")
		if ok && next < len(SummaryHeadings) && strings.EqualFold(strings.TrimSpace(h), SummaryHeadings[next]) {
			next++
		}
	}
	return next == len(SummaryHeadings)
}
