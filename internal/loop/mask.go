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
	// stuckMax is how many stuck compactions a turn may make before a repeat loop at the cliff ends it
	// (Config.CompactAtCliff).
	stuckMax = 2
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
	pinned    bool   // it superseded an earlier read: kept whole (coveredLater)
	callKey   string // the call's CallKey, set by the loop: a compaction that stubs it clears its widening chain
}

// observe is the observation of one tool result.
func observe(id, tool string, input json.RawMessage, req int, result string, ok bool) observation {
	o := observation{id: id, tool: tool, req: req, bytes: len(result), first: cut(firstLine(result), 120), ok: ok}
	var in struct {
		Path    string `json:"path"`
		Offset  int    `json:"offset"`
		Limit   int    `json:"limit"`
		Command string `json:"command"`
		Pattern string `json:"pattern"`
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
	case "grep", "glob":
		o.key = strings.Join(strings.Fields(in.Pattern), " ")
		if o.path != "" && o.path != "." {
			o.key += " in " + o.path
		}
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

// track adds a tool result to the observations, stamped with its file's version, and counts the call. level is the
// call's widening (tools.WithWiden): a widened whole read covers 3 times the base lines, or the whole file.
func (s *Session) track(o observation, truncated bool, level int) {
	o.truncated, o.ver, o.whole = truncated, s.vers.of(o.path), s.cfg.ReadLines
	if o.whole <= 0 {
		o.whole = wholeReadLines
	}
	switch {
	case level == 1:
		o.whole *= 3
	case level >= 2:
		o.whole = 1 << 30 // every line
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

// maskStubs is what a compaction before request next would replace, each stub shorter than the result it replaces,
// over maskMinBytes: each unmasked read that a later read covers (coveredLater), and each unmasked command output from
// before the last maskKeepRequests requests. With wide (a compaction at the cliff, Config.CompactAtCliff), also each
// grep or glob output and each whole-file read from before the last maskKeepRequests requests. It marks them masked.
func (s *Session) maskStubs(next int, wide bool) []proto.MaskedResult {
	var out []proto.MaskedResult
	old := func(o *observation) bool { return o.req <= next-1-maskKeepRequests }
	for i := range s.obs {
		o := &s.obs[i]
		if o.masked || o.bytes <= maskMinBytes {
			continue
		}
		var stub string
		cover := -1 // the read that supersedes this one, pinned if the stub is kept
		if o.tool == "read" && o.key != "" {
			cover = s.coveredLater(i)
		}
		switch {
		case cover >= 0:
			stub = fmt.Sprintf("[masked to fit the window: this read of %s%s (request %d, %d bytes) is superseded by a later read of the same file]",
				o.key, o.span(), o.req, o.bytes)
		case o.tool == "bash" && old(o):
			stub = fmt.Sprintf("[masked to fit the window: the output of `%s` (request %d, %d bytes; its first line: %s). Run it again if you need it]",
				cut(o.key, 80), o.req, o.bytes, o.first)
		case wide && (o.tool == "grep" || o.tool == "glob") && old(o):
			stub = fmt.Sprintf("[masked to fit the window: the output of %s for `%s` (request %d, %d bytes). Run it again, narrower, if you need it]",
				o.tool, cut(o.key, 80), o.req, o.bytes)
		case wide && o.tool == "read" && o.key != "" && o.from == 0 && o.n == 0 && !o.pinned && old(o):
			stub = fmt.Sprintf("[masked to fit the window: this whole read of %s (request %d, %d bytes). Read a range with offset and limit if you need it]",
				o.key, o.req, o.bytes)
		default:
			continue
		}
		if len(stub) >= o.bytes {
			continue
		}
		o.masked = true
		if cover >= 0 && (o.from != 0 || o.n != 0 || o.pinned) { // a range read, or a kept one, superseded: keep its coverer whole
			s.obs[cover].pinned = true
		}
		out = append(out, proto.MaskedResult{ID: o.id, Stub: stub})
	}
	return out
}

// coveredLater is the index of a read after obs[i] that covers it, or -1: of the same file at the same version,
// successful, returned whole (not cut to fit), and not itself masked. maskStubs pins it when it stubs a range read
// obs[i], or a pinned one, so the pin carries along a chain of superseders and one compaction never stubs every read
// of the file; a whole read that superseded only another whole read is not pinned, so large reads stay stubbable.
func (s *Session) coveredLater(i int) int {
	e := s.obs[i]
	for j := i + 1; j < len(s.obs); j++ {
		later := &s.obs[j]
		if later.tool == "read" && later.ok && !later.truncated && !later.masked && later.key == e.key && later.ver == e.ver && later.covers(e) {
			return j
		}
	}
	return -1
}

// noteUsage takes a response's prompt tokens: what the next compaction is decided on, and, for the first response after
// a compaction, the floor the prompt must grow from before another.
func (s *Session) noteUsage(prompt int) {
	s.lastPrompt = prompt
	if s.compacted && s.maskFloor == 0 {
		s.maskFloor = prompt
	}
}

// regrown says whether another compaction may run in window w: none has run, or the prompt has grown maskRegrowShare
// of w past the floor since the last.
func (s *Session) regrown(w int) bool {
	return !s.compacted || (s.maskFloor > 0 && (s.lastPrompt-s.maskFloor)*100 >= maskRegrowShare*w)
}

// due says whether a compaction may run before the next request for mask_observations: the last prompt has reached
// maskShare of window w, and it has regrown.
func (s *Session) due(w int) bool {
	return w > 0 && s.lastPrompt*100 >= maskShare*w && s.regrown(w)
}

// compact runs a compaction before request next when one is due. The cliff comes first (Config.CompactAtCliff): a
// result got fit_window's nearly-full note in the last response, and no cliff compaction has masked anything this
// turn. It has its own arming and does not read mask_observations' regrowth floor, so a compaction that frees little
// cannot keep the cliff from firing. Then mask_observations' 75% trigger (Config.MaskObservations).
func (s *Session) compact(next int, cause int64) error {
	note := s.cliffNote
	s.cliffNote = false
	if s.cfg.CompactAtCliff && note && !s.cliffDone {
		m, err := s.compactNow(next, cause, "cliff")
		s.cliffDone = m > 0
		return err
	}
	if s.cfg.MaskObservations && s.due(s.cfg.Source.Window()) {
		_, err := s.compactNow(next, cause, "window")
		return err
	}
	return nil
}

// compactNow stubs what a compaction with trigger ("window", "cliff" or "stuck") masks before request next. It rewrites
// those results in the conversation, records a compaction event, and returns how many it masked. Every trigger moves
// mask_observations' regrowth floor, so its window trigger waits for regrowth after any compaction; "cliff" and
// "stuck" are wide (maskStubs) and never read the floor.
func (s *Session) compactNow(next int, cause int64, trigger string) (int, error) {
	masked := s.maskStubs(next, trigger != "window")
	if len(masked) == 0 {
		return 0, nil
	}
	before, after := 0, 0
	for _, m := range masked {
		for _, o := range s.obs {
			if o.id == m.ID {
				before += o.bytes
				delete(s.shown, o.callKey) // its next call starts again at the base cap
			}
		}
		after += len(m.Stub)
		s.conv.ReplaceResult(m.ID, m.Stub)
	}
	s.compacted, s.maskFloor = true, 0 // every trigger moves mask_observations' floor; only "window" reads it (due)
	_, err := s.record(proto.EvCompaction, cause, proto.Compaction{Request: next, Trigger: trigger, Prompt: s.lastPrompt,
		Window: s.cfg.Source.Window(), Masked: masked, Before: before, After: after})
	return len(masked), err
}

// shownChain is progressive widening's state, per call key this turn (Config.CompactAtCliff; calibration §16.2): the
// base-cap output the call last showed, and the level its next call is served at. The level only goes up, and only on
// an unchanged result shown whole. A result cut to fit, or answered with fit_window's nearly-full note, freezes the
// chain at the last level that fit whole. A changed output starts it again, and a compaction that stubs the call
// clears it (compactNow).
type shownChain map[string]shownLink

type shownLink struct {
	last   string // the base-cap output last shown
	level  int    // the widening the next call is served at: 0, 1 or 2 (tools.WithWiden)
	frozen bool   // a result at a higher level did not fit whole
}

// level is the widening the next call with key is served at.
func (c shownChain) level(key string) int { return c[key].level }

// same says whether base is the base-cap output the call with key last showed.
func (c shownChain) same(key, base string) bool {
	l, ok := c[key]
	return ok && l.last == base
}

// record takes a call's base-cap output, the level it was served at, and whether its result was shown whole. A first
// call, or a changed output, starts the chain at the base cap; a frozen chain stays; an unchanged result shown whole
// goes up a level; one not shown whole freezes the chain at the level below, the last that fit.
func (c shownChain) record(key, base string, served int, whole bool) {
	l, ok := c[key]
	switch {
	case !ok || l.last != base:
		l = shownLink{last: base}
		if whole {
			l.level = 1
		} else {
			l.frozen = true
		}
	case l.frozen:
	case whole:
		l.level = min(served+1, 2)
	default:
		l.level, l.frozen = max(served-1, 0), true
	}
	c[key] = l
}

// wholeOf is the lines o returned if it is a read with no range: as track set it, or wholeReadLines.
func wholeOf(o observation) int {
	if o.whole > 0 {
		return o.whole
	}
	return wholeReadLines
}
