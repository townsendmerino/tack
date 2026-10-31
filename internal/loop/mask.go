package loop

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/townsendmerino/tack/internal/proto"
)

// Observation masking (Config.MaskObservations; e1-calibration-preregistration-2026-10-18.md §15.2): tack's first
// compaction point (DESIGN §7.6). Before a request, once the last prompt has reached maskShare of the served window,
// the history is rewritten once: a read of a file that is read again later, and a command's output older than the last
// maskKeepRequests requests, each become a one-line stub. A rewrite breaks the server's cached prefix after the first
// rewritten message, so it runs only past the threshold, and each result is rewritten at most once.
const (
	// maskShare is the share of the window, in percent, the last prompt must reach before a compaction.
	maskShare = 75
	// maskKeepRequests is how many of the latest requests keep their command output whole.
	maskKeepRequests = 3
	// maskMinBytes is the size under which a result is kept whole: a stub would save little.
	maskMinBytes = 300
)

// observation is one tool result the conversation holds, as the masking rule reads it.
type observation struct {
	id, tool string
	key      string // a read's path; a command's text
	span     string // a read's line range, as asked
	req      int    // the request whose response made the call
	bytes    int
	first    string // the result's first line
	masked   bool
}

// observe is the observation of one tool result.
func observe(id, tool string, input json.RawMessage, req int, result string) observation {
	o := observation{id: id, tool: tool, req: req, bytes: len(result), first: firstLine(result)}
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
			o.span = fmt.Sprintf(" from line %d, %d lines", max(in.Offset, 1), in.Limit)
		}
	case "bash":
		o.key = in.Command
	}
	return o
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	if len(s) > 120 {
		s = s[:120] + "…"
	}
	return s
}

// maskStubs is what a compaction before request next would replace: each unmasked read of a file read again later,
// and each unmasked command output from before the last maskKeepRequests requests, if longer than maskMinBytes. It
// marks them masked.
func (s *Session) maskStubs(next int) []proto.MaskedResult {
	lastRead := map[string]int{} // path → index of its latest read
	for i, o := range s.obs {
		if o.tool == "read" && o.key != "" {
			lastRead[o.key] = i
		}
	}
	var out []proto.MaskedResult
	for i := range s.obs {
		o := &s.obs[i]
		if o.masked || o.bytes <= maskMinBytes {
			continue
		}
		var stub string
		switch {
		case o.tool == "read" && o.key != "" && lastRead[o.key] > i:
			stub = fmt.Sprintf("[masked to fit the window: this read of %s%s (request %d, %d bytes) is superseded by a later read of the same file]",
				o.key, o.span, o.req, o.bytes)
		case o.tool == "bash" && o.req <= next-1-maskKeepRequests:
			cmd := o.key
			if len(cmd) > 80 {
				cmd = cmd[:80] + "…"
			}
			stub = fmt.Sprintf("[masked to fit the window: the output of `%s` (request %d, %d bytes; its first line: %s). Run it again if you need it]",
				cmd, o.req, o.bytes, o.first)
		default:
			continue
		}
		o.masked = true
		out = append(out, proto.MaskedResult{ID: o.id, Stub: stub})
	}
	return out
}

// compact runs a compaction before request next when the last prompt has reached maskShare of the served window and
// something is left to mask: it rewrites those results in the conversation and records a compaction event.
func (s *Session) compact(next int, cause int64) error {
	w := s.cfg.Source.Window()
	if w <= 0 || s.lastPrompt*100 < maskShare*w {
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
	_, err := s.record(proto.EvCompaction, cause, proto.Compaction{Request: next, Trigger: "window", Prompt: s.lastPrompt, Window: w,
		Masked: masked, Before: before, After: after})
	return err
}
