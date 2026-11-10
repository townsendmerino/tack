package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"

	"github.com/townsendmerino/tack/internal/proto"
)

// View is the TUI's screen as a pure model (DESIGN §10): Apply folds in a session's events, in log order, and Render
// draws the transcript's tail above the status line. It does no I/O and keeps no clock, so a recorded event stream
// renders the same frame every time (the golden frames in testdata). The key hints on an open question are what the
// terminal layer binds; the view takes no input.
//
// Widths are counted in terminal cells (go-runewidth).
type View struct {
	Status Status
	// Scroll is how many lines the frame sits above the transcript's bottom; Render clamps it at the top.
	Scroll int

	entries []entry
	calls   map[string]int // tool call id → its index in entries
	// stream is the open response's blocks, by delta index, drawn below the entries until assistant.message replaces
	// them or the turn ends.
	stream    []entry
	streamIdx map[int]int
	unread    map[proto.EventType]bool // the event types already noted as unreadable
}

// entry is one item of the transcript: a head paragraph and the paragraphs under it, which Render wraps.
type entry struct {
	kind  string // "user", "text", "thinking", "tool", "note"
	head  string
	lines []string

	// a tool call's state
	asking   *proto.PermissionRequested
	resolved string // the decision, for a call that asked, or that was denied without asking
	started  bool
	result   string
}

// Apply folds one event into the view.
func (v *View) Apply(ev proto.Event) {
	v.Status.Apply(ev)
	p, err := ev.Decode()
	if err != nil { // a newer daemon's event, or a version skew: noted once per type
		if !v.unread[ev.Type] {
			if v.unread == nil {
				v.unread = map[proto.EventType]bool{}
			}
			v.unread[ev.Type] = true
			v.note("! unreadable event " + string(ev.Type) + ": " + err.Error())
		}
		return
	}
	if v.calls == nil {
		v.calls = map[string]int{}
	}
	switch e := p.(type) {
	case proto.SessionCreated:
		if e.Parent != "" {
			v.note("↳ a helper of " + string(e.Parent) + ", in " + string(e.Mode) + " mode")
		}
	case proto.UserMessage:
		v.endStream()
		var b strings.Builder
		for _, c := range e.Content {
			b.WriteString(c.Text)
		}
		v.entries = append(v.entries, entry{kind: "user", head: "> " + b.String()})
	case proto.AssistantDelta:
		if v.streamIdx == nil {
			v.streamIdx = map[int]int{}
		}
		i, ok := v.streamIdx[e.Index]
		if !ok {
			i = len(v.stream)
			v.streamIdx[e.Index] = i
			v.stream = append(v.stream, entry{kind: e.Kind})
		}
		v.stream[i].head += e.Text
	case proto.AssistantMessage:
		v.endStream()
		for _, b := range e.Content {
			switch b.Type {
			case "text":
				if strings.TrimSpace(b.Text) != "" {
					v.entries = append(v.entries, entry{kind: "text", head: b.Text})
				}
			case "thinking", "redacted_thinking":
				v.entries = append(v.entries, entry{kind: "thinking", head: fmt.Sprintf("· thought (%d chars)", utf8.RuneCountInString(b.Thinking))})
			}
		}
	case proto.ToolCall:
		v.calls[e.ID] = len(v.entries)
		v.entries = append(v.entries, entry{kind: "tool", head: "● " + e.Name + callArg(e.Input)})
	case proto.PermissionRequested:
		if t := v.call(e.ID); t != nil {
			q := e
			t.asking = &q
		}
	case proto.PermissionResolved:
		if t := v.call(e.ID); t != nil {
			asked := t.asking != nil
			t.asking = nil
			switch {
			case e.Decision == proto.DecisionDeny:
				t.resolved = "✗ denied by " + e.By
				if len(e.Reasons) > 0 {
					t.resolved += ": " + strings.Join(e.Reasons, "; ")
				}
			case asked:
				t.resolved = "✓ allowed by " + e.By
				if e.Scope == "session" {
					t.resolved += " for the session"
					if e.RuleAdded != "" {
						t.resolved += " (" + e.RuleAdded + ")"
					}
				}
			}
		}
	case proto.ToolStarted:
		if t := v.call(e.ID); t != nil {
			t.started = true
		}
	case proto.ToolFinished:
		if t := v.call(e.ID); t != nil {
			t.result = resultLine(e)
		}
	case proto.Checkpoint:
		if t := v.call(e.ID); t != nil && e.Unavailable != "" {
			t.lines = append(t.lines, "! can't be rewound: "+e.Unavailable)
		}
	case proto.Rewind:
		v.note(fmt.Sprintf("↺ rewound %d paths to the checkpoint at %d", e.Changed, e.To))
	case proto.Compaction:
		s := fmt.Sprintf("— compacted at %s/%s: %d results masked", kilo(e.Prompt), kilo(e.Window), len(e.Masked))
		if len(e.Summarized) > 0 {
			s += fmt.Sprintf(", %d events summarized", len(e.Summarized))
		}
		v.note(s + " —")
	case proto.ErrorEvent:
		s := "! " + e.Class + " error: " + e.Message
		if e.Retrying {
			s += " (retrying)"
		}
		v.note(s)
	case proto.BudgetExceeded:
		v.note(fmt.Sprintf("! the %s budget tripped: %g of %g", strings.ReplaceAll(e.Budget, "_", " "), e.Value, e.Limit))
	case proto.DaemonHalted:
		s := "■ halted by " + e.Trigger
		if e.Reason != "" {
			s += ": " + e.Reason
		}
		v.note(s)
	case proto.DaemonHaltRefused:
		v.note(fmt.Sprintf("! the halt killed nothing (%d processes): %s", len(e.Processes), e.Reason))
	case proto.TurnEnd:
		v.endStream()
		switch e.StopReason {
		case proto.StopEndTurn, proto.StopDone, proto.StopHalted: // a halt said so already, in daemon.halted
		default:
			v.note("· the turn ended: " + strings.ReplaceAll(e.StopReason, "_", " "))
		}
	}
}

func (v *View) call(id string) *entry {
	if i, ok := v.calls[id]; ok {
		return &v.entries[i]
	}
	return nil
}

func (v *View) note(s string) { v.entries = append(v.entries, entry{kind: "note", head: s}) }

// endStream drops the open response's streamed blocks: assistant.message replaces them, and a turn that ended or a new
// message abandons them.
func (v *View) endStream() { v.stream, v.streamIdx = nil, nil }

// callArg is the argument a call is best known by: its command, path, query, pattern, prompt or symbol, else its input
// as JSON.
func callArg(in json.RawMessage) string {
	var m map[string]any
	if json.Unmarshal(in, &m) != nil || len(m) == 0 {
		return ""
	}
	for _, k := range []string{"command", "path", "query", "pattern", "prompt", "symbol"} {
		if s, ok := m[k].(string); ok {
			return " " + s
		}
	}
	return " " + string(in)
}

// resultLine is a finished call's result on one line: its outcome unless ok, its first line, and how many more.
func resultLine(e proto.ToolFinished) string {
	first, rest, _ := strings.Cut(strings.TrimRight(e.Result, "\n"), "\n")
	s := first
	if e.Outcome != proto.OutcomeOK { // the model is told "error: …"; the outcome says it here
		s = e.Outcome + ": " + strings.TrimPrefix(first, "error: ")
	}
	if rest != "" {
		s += fmt.Sprintf(" (+%d lines)", strings.Count(rest, "\n")+1)
	}
	if e.Truncated {
		s += " · full output kept"
	}
	if e.Tokens > 0 {
		s += " · " + kilo(e.Tokens) + " tokens"
	}
	return s
}

// paragraphs is the entry's text, one paragraph per line of it, each with the indent it is drawn at.
func (e *entry) paragraphs() [][2]string {
	var out [][2]string
	add := func(indent, s string) {
		for _, l := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
			out = append(out, [2]string{indent, l})
		}
	}
	add("", e.head)
	for _, l := range e.lines {
		add("  ", l)
	}
	if e.kind != "tool" {
		return out
	}
	if q := e.asking; q != nil {
		if q.Command != "" {
			add("  ", "? run: "+q.Command)
			for _, p := range q.Parsed {
				add("    ", p)
			}
		} else {
			add("  ", "? allow "+q.Tool+" in "+string(q.Mode)+" mode")
		}
		for _, r := range q.Reasons {
			add("    ", "- "+r)
		}
		add("  ", "[a]llow once  [s] for the session  [d]eny")
	}
	if e.resolved != "" {
		add("  ", e.resolved)
	}
	switch {
	case e.result != "":
		add("  ", "⎿ "+e.result)
	case e.started:
		add("  ", "⎿ running")
	}
	return out
}

// lines is the entry wrapped to width, with a blank line before a user message that is not the first entry.
func (e *entry) wrapped(width int, first bool) []string {
	var lines []string
	if e.kind == "user" && !first {
		lines = append(lines, "")
	}
	for _, p := range e.paragraphs() {
		lines = append(lines, wrap(p[1], p[0], width)...)
	}
	return lines
}

// Render draws a frame of exactly height lines joined by newlines, none wider than width runes: the transcript's tail,
// Scroll lines up, then a rule and the status line (the status line alone below a height of 3).
func (v *View) Render(width, height int) string {
	if width < 1 || height < 1 {
		return ""
	}
	room := height - 2
	if height < 3 {
		room = height - 1
	}
	// Wrap from the bottom up, only as far as the frame reaches.
	need := room + max(v.Scroll, 0)
	var tail []string // bottom line first
	push := func(e *entry, first bool) {
		ls := e.wrapped(width, first)
		for j := len(ls) - 1; j >= 0; j-- {
			tail = append(tail, ls[j])
		}
	}
	for i := len(v.stream) - 1; i >= 0 && len(tail) < need; i-- {
		push(&v.stream[i], false)
	}
	for i := len(v.entries) - 1; i >= 0 && len(tail) < need; i-- {
		push(&v.entries[i], i == 0)
	}
	scroll := min(max(v.Scroll, 0), max(len(tail)-room, 0))
	frame := make([]string, 0, height)
	for len(frame) < room-(min(len(tail), scroll+room)-scroll) {
		frame = append(frame, "")
	}
	for i := min(len(tail), scroll+room) - 1; i >= scroll; i-- {
		frame = append(frame, tail[i])
	}
	if height >= 3 {
		frame = append(frame, strings.Repeat("─", width))
	}
	frame = append(frame, fit(v.Status.Line(), width))
	return strings.Join(frame, "\n")
}

// cells is s's width in terminal cells: a wide rune counts two and a combining mark none.
func cells(s string) int { return runewidth.StringWidth(s) }

// wrap breaks one paragraph into lines of at most width cells at spaces, breaking a longer word where it must, never
// inside a rune. Every line carries indent plus the paragraph's own leading space, kept so code stays indented; none ends
// in a space. An indent that leaves no room is dropped.
func wrap(s, indent string, width int) []string {
	s = visible(strings.ReplaceAll(s, "\t", "    "))
	body := strings.TrimLeftFunc(s, unicode.IsSpace)
	indent += s[:len(s)-len(body)]
	if cells(indent) >= width {
		indent = ""
	}
	avail := width - cells(indent)
	words := strings.Fields(body)
	if len(words) == 0 {
		return []string{""}
	}
	var out []string
	var line []rune
	lw := 0 // the cells of line
	flush := func() {
		out = append(out, strings.TrimRight(indent+string(line), " "))
		line, lw = line[:0], 0
	}
	for _, w := range words {
		r := []rune(w)
		if lw > 0 && lw+1+cells(w) > avail {
			flush()
		}
		if lw > 0 {
			line = append(line, ' ')
			lw++
		}
		for lw+cells(string(r)) > avail {
			n := 0 // the runes of r that fit in the room left, at least one on an empty line so the loop ends
			for n < len(r) && lw+cells(string(r[:n+1])) <= avail {
				n++
			}
			if n == 0 && lw > 0 {
				flush()
				continue
			}
			if n == 0 {
				n = 1 // a rune wider than the line: it overflows rather than loops
			}
			line = append(line, r[:n]...)
			r = r[n:]
			flush()
		}
		line = append(line, r...)
		lw += cells(string(r))
	}
	if len(line) > 0 {
		flush()
	}
	return out
}

// fit cuts s to width cells, marking a cut with an ellipsis, with no trailing space; controls are made visible first.
func fit(s string, width int) string {
	s = strings.TrimRight(visible(strings.ReplaceAll(s, "\t", " ")), " ")
	if cells(s) <= width {
		return s
	}
	if width <= 1 {
		return runewidth.Truncate(s, width, "")
	}
	return strings.TrimRight(runewidth.Truncate(s, width-1, ""), " ") + "…"
}

// visible makes every control character in s a visible one, one cell each, so text from the model, a tool or a file
// cannot drive the terminal (an OSC 52 clipboard write, a screen clear, a colour): ESC is ␛, another C0 control its
// Unicode Control Pictures symbol (␀…␟), DEL ␡, and a C1 control (U+0080–U+009F, which a terminal may read as ESC
// and a letter) or a byte that is not UTF-8 is �. Newlines are the caller's: wrap splits on them first, and fit's input
// is one line. Every string Render draws passes through here, in wrap or fit.
func visible(s string) string {
	clean := true
	for _, r := range s {
		if r < 0x20 || r == 0x7f || r >= 0x80 && r <= 0x9f || r == utf8.RuneError || hiddenRune(r) {
			clean = false
			break
		}
	}
	if clean {
		return s
	}
	var b strings.Builder
	for _, r := range s { // ranging over a string yields RuneError for each byte that is not UTF-8
		switch {
		case r < 0x20:
			b.WriteRune(0x2400 + r)
		case r == 0x7f:
			b.WriteRune('␡')
		case r >= 0x80 && r <= 0x9f, hiddenRune(r):
			b.WriteRune(utf8.RuneError)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// hiddenRune reports whether r is a bidi override or isolate (U+202A–202E, U+2066–2069) or a zero-width rune
// (U+200B–200F, U+FEFF): each can reorder or hide text on screen, so visible draws it as U+FFFD.
func hiddenRune(r rune) bool {
	return r >= 0x202A && r <= 0x202E || r >= 0x2066 && r <= 0x2069 || r >= 0x200B && r <= 0x200F || r == 0xFEFF
}
