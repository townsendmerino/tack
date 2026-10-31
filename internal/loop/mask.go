package loop

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/townsendmerino/tack/internal/proto"
)

// Observation masking (Config.MaskObservations; e1-calibration-preregistration-2026-10-18.md §15.2): tack's first
// compaction point (DESIGN §7.6). Before a request, once the last prompt has reached maskShare of the served window,
// the history is rewritten: a read of a file that a later successful read covers, and a command's output older than the
// last maskKeepRequests requests, each become a one-line stub. A rewrite breaks the server's cached prefix after the
// first rewritten message, so it is one-shot: after a compaction the rule is disarmed until the prompt falls below
// maskRearmShare, and history that cannot be masked costs one rewrite, not one per request.
const (
	// maskShare is the share of the window, in percent, the last prompt must reach before a compaction.
	maskShare = 75
	// maskRearmShare is the share, in percent, the prompt must fall below before another compaction may run.
	maskRearmShare = 60
	// maskKeepRequests is how many of the latest requests keep their command output whole.
	maskKeepRequests = 3
	// maskMinBytes is the size up to which a result is kept whole: a stub would save little.
	maskMinBytes = 300
	// wholeReadLines is how many lines a read with no range returns (tools.readMaxLines).
	wholeReadLines = 200
)

// observation is one tool result the conversation holds, as the masking rule reads it.
type observation struct {
	id, tool string
	key      string // a read's path; a command's text
	from, n  int    // a read's line range as asked: from line from, n lines; 0, 0 for the whole file
	ok       bool   // the call succeeded
	req      int    // the request whose response made the call
	bytes    int
	first    string // the result's first line, cut to a stub's length
	masked   bool
}

// observe is the observation of one tool result.
func observe(id, tool string, input json.RawMessage, req int, result string, ok bool) observation {
	o := observation{id: id, tool: tool, req: req, bytes: len(result), first: cut(firstLine(result), 120), ok: ok}
	var in struct {
		Path    string `json:"path"`
		Offset  int    `json:"offset"`
		Limit   int    `json:"limit"`
		Command string `json:"command"`
	}
	_ = json.Unmarshal(input, &in)
	switch tool {
	case "read":
		o.key = strings.TrimPrefix(in.Path, "./")
		if in.Offset > 0 || in.Limit > 0 {
			o.from, o.n = max(in.Offset, 1), in.Limit
		}
	case "bash":
		o.key = in.Command
	}
	return o
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return s
}

// cut is s cut to at most n bytes at a rune boundary, marked when cut.
func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}

// covers says whether the read later returned every line the read earlier did. A read with no range returns its first
// wholeReadLines lines; a read with an offset and no limit is not compared, and covers nothing.
func (later observation) covers(earlier observation) bool {
	lf, ln, ef, en := later.from, later.n, earlier.from, earlier.n
	if lf == 0 && ln == 0 {
		lf, ln = 1, wholeReadLines
	}
	if ef == 0 && en == 0 {
		ef, en = 1, wholeReadLines
	}
	if ln == 0 || en == 0 {
		return false
	}
	return lf <= ef && lf+ln >= ef+en
}

func (o observation) span() string {
	if o.from == 0 && o.n == 0 {
		return ""
	}
	return fmt.Sprintf(" from line %d, %d lines", o.from, o.n)
}

// maskStubs is what a compaction before request next would replace, each stub shorter than the result it replaces:
// each unmasked read that a later successful read of the same file covers, and each unmasked command output from
// before the last maskKeepRequests requests, over maskMinBytes. It marks them masked.
func (s *Session) maskStubs(next int) []proto.MaskedResult {
	var out []proto.MaskedResult
	for i := range s.obs {
		o := &s.obs[i]
		if o.masked || o.bytes <= maskMinBytes {
			continue
		}
		var stub string
		switch o.tool {
		case "read":
			if o.key == "" || !s.coveredLater(i) {
				continue
			}
			stub = fmt.Sprintf("[masked to fit the window: this read of %s%s (request %d, %d bytes) is superseded by a later read of the same file]",
				o.key, o.span(), o.req, o.bytes)
		case "bash":
			if o.req > next-1-maskKeepRequests {
				continue
			}
			stub = fmt.Sprintf("[masked to fit the window: the output of `%s` (request %d, %d bytes; its first line: %s). Run it again if you need it]",
				cut(o.key, 80), o.req, o.bytes, o.first)
		default:
			continue
		}
		if len(stub) >= o.bytes {
			continue
		}
		o.masked = true
		out = append(out, proto.MaskedResult{ID: o.id, Stub: stub})
	}
	return out
}

// coveredLater says whether a successful read after obs[i] of the same file covers it.
func (s *Session) coveredLater(i int) bool {
	for _, later := range s.obs[i+1:] {
		if later.tool == "read" && later.ok && later.key == s.obs[i].key && later.covers(s.obs[i]) {
			return true
		}
	}
	return false
}

// noteUsage takes a response's prompt tokens: what the next compaction is decided on, and what re-arms the rule once
// the prompt has fallen below maskRearmShare.
func (s *Session) noteUsage(prompt int) {
	s.lastPrompt = prompt
	if w := s.cfg.Source.Window(); w > 0 && prompt*100 < maskRearmShare*w {
		s.maskSpent = false
	}
}

// compact runs a compaction before request next when the rule is armed, the last prompt has reached maskShare of the
// served window, and something is left to mask: it rewrites those results in the conversation, records a compaction
// event, and disarms the rule.
func (s *Session) compact(next int, cause int64) error {
	w := s.cfg.Source.Window()
	if s.maskSpent || w <= 0 || s.lastPrompt*100 < maskShare*w {
		return nil
	}
	masked := s.maskStubs(next)
	if len(masked) == 0 {
		return nil
	}
	before, after := 0, 0
	for _, m := range masked {
		for _, o := range s.obs {
			if o.id == m.ID {
				before += o.bytes
			}
		}
		after += len(m.Stub)
		s.conv.ReplaceResult(m.ID, m.Stub)
	}
	s.maskSpent = true
	_, err := s.record(proto.EvCompaction, cause, proto.Compaction{Request: next, Trigger: "window", Prompt: s.lastPrompt, Window: w,
		Masked: masked, Before: before, After: after})
	return err
}
