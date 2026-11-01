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
// the history is rewritten: a read of a file that a later read of the same version covers, and a command's output older
// than the last maskKeepRequests requests, each become a one-line stub. A rewrite breaks the server's cached prefix
// after the first rewritten message, so after a compaction the next waits until the prompt has grown by
// maskRegrowShare of the window past the first prompt after it: each compaction costs one re-prefill, and history that
// cannot be masked is not rewritten every request.
const (
	// maskShare is the share of the window, in percent, the last prompt must reach before a compaction.
	maskShare = 75
	// maskRegrowShare is how far, in percent of the window, the prompt must grow past the first prompt after a
	// compaction before another may run.
	maskRegrowShare = 10
	// maskKeepRequests is how many of the latest requests keep their command output whole.
	maskKeepRequests = 3
	// maskMinBytes is the size up to which a result is kept whole: a stub would save little.
	maskMinBytes = 300
	// wholeReadLines is how many lines a read with no range returns when Config.ReadLines is 0 (tools.readMaxLines).
	wholeReadLines = 200
)

// observation is one tool result the conversation holds, as the masking rule reads it.
type observation struct {
	id, tool  string
	path      string // the call's path argument, if it has one
	key       string // a read's path; a command's text, on one line
	from, n   int    // a read's line range as asked: from line from, n lines; 0, 0 for the whole file
	ok        bool   // the call succeeded
	truncated bool   // the result was cut or replaced to fit (ToolFinished.Truncated)
	ver       int    // a read's file version (fileVersions.of), set by track
	whole     int    // the lines a read with no range returned (Config.ReadLines), set by track
	req       int    // the request whose response made the call
	bytes     int
	first     string // the result's first line, cut to a stub's length
	masked    bool
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
	o.path = strings.TrimPrefix(in.Path, "./")
	switch tool {
	case "read":
		o.key = o.path
		if in.Offset > 0 || in.Limit > 0 {
			o.from, o.n = max(in.Offset, 1), in.Limit
		}
	case "bash":
		o.key = strings.Join(strings.Fields(in.Command), " ")
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

// fileVersions counts the calls that may have changed a file: an edit or write changes its own path; any other call
// that is not known to only read (bash, and any tool not listed) may have changed every file. A file's version is the
// sum of the two counts, so it is equal at two points only if neither moved between them.
type fileVersions struct {
	all  int
	path map[string]int
}

func (v *fileVersions) of(p string) int { return v.all + v.path[p] }

// readOnly are the tools that change no file.
var readOnly = map[string]bool{"read": true, "glob": true, "grep": true, "search": true, "symbol": true,
	"read_output": true, "todo": true, "ask_user": true, "done": true}

// touch counts the call o, failed or not.
func (v *fileVersions) touch(o observation) {
	switch {
	case readOnly[o.tool]:
	case (o.tool == "edit" || o.tool == "write") && o.path != "":
		if v.path == nil {
			v.path = map[string]int{}
		}
		v.path[o.path]++
	default:
		v.all++
	}
}

// track adds a tool result to the observations, stamped with its file's version, and counts the call.
func (s *Session) track(o observation, truncated bool) {
	o.truncated, o.ver, o.whole = truncated, s.vers.of(o.path), s.cfg.ReadLines
	if o.whole <= 0 {
		o.whole = wholeReadLines
	}
	s.vers.touch(o)
	s.obs = append(s.obs, o)
}

// covers says whether the read later returned every line the read earlier did. A read with no range returns its first
// whole lines; a read with an offset and no limit is not compared, and covers nothing.
func (later observation) covers(earlier observation) bool {
	lf, ln, ef, en := later.from, later.n, earlier.from, earlier.n
	if lf == 0 && ln == 0 {
		lf, ln = 1, wholeOf(later)
	}
	if ef == 0 && en == 0 {
		ef, en = 1, wholeOf(earlier)
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
// each unmasked read that a later read covers (coveredLater), and each unmasked command output from before the last
// maskKeepRequests requests, over maskMinBytes. It marks them masked.
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

// coveredLater says whether a read after obs[i] covers it: of the same file at the same version, successful, and
// returned whole, not cut to fit.
func (s *Session) coveredLater(i int) bool {
	e := s.obs[i]
	for _, later := range s.obs[i+1:] {
		if later.tool == "read" && later.ok && !later.truncated && later.key == e.key && later.ver == e.ver && later.covers(e) {
			return true
		}
	}
	return false
}

// noteUsage takes a response's prompt tokens: what the next compaction is decided on, and, for the first response after
// a compaction, the floor the prompt must grow from before another.
func (s *Session) noteUsage(prompt int) {
	s.lastPrompt = prompt
	if s.compacted && s.maskFloor == 0 {
		s.maskFloor = prompt
	}
}

// due says whether a compaction may run before the next request: the last prompt has reached maskShare of window w,
// and either none has run or the prompt has grown maskRegrowShare of w past the floor since the last.
func (s *Session) due(w int) bool {
	if w <= 0 || s.lastPrompt*100 < maskShare*w {
		return false
	}
	return !s.compacted || (s.maskFloor > 0 && (s.lastPrompt-s.maskFloor)*100 >= maskRegrowShare*w)
}

// compact runs a compaction before request next when one is due and something is left to mask: it rewrites those
// results in the conversation, records a compaction event, and resets the floor.
func (s *Session) compact(next int, cause int64) error {
	w := s.cfg.Source.Window()
	if !s.due(w) {
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
	s.compacted, s.maskFloor = true, 0
	_, err := s.record(proto.EvCompaction, cause, proto.Compaction{Request: next, Trigger: "window", Prompt: s.lastPrompt, Window: w,
		Masked: masked, Before: before, After: after})
	return err
}

// wholeOf is the lines o returned if it is a read with no range: as track set it, or wholeReadLines.
func wholeOf(o observation) int {
	if o.whole > 0 {
		return o.whole
	}
	return wholeReadLines
}
