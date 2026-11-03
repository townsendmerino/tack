package loop

import (
	"fmt"
	"strings"

	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/store"
	"unicode/utf8"
)

// Fitting tool results to the window (Config.FitWindow; calibration pre-registration §4.6).
const (
	// fitMarginTokens is kept free beyond the next reply's max_tokens.
	fitMarginTokens = 512
	// nearlyFullOpen opens the note a result becomes when the window is nearly full: the cliff (Config.CompactAtCliff).
	nearlyFullOpen = "[window nearly full"
	// fitMinBytes is the least room a cut is worth: with less, a result longer than this becomes the nearly-full note.
	fitMinBytes = 1 << 10
	// fitDefaultRate is the bytes per token before a response has reported its prompt.
	fitDefaultRate = 3.0
	// fitRateScale shrinks the measured rate: it is the request's JSON bytes per token, and JSON's escapes and keys
	// inflate it, which would let a result overflow the window (owner, 2026-10-19).
	fitRateScale = 0.85
	// The scaled rate is held to [fitMinRate, fitMaxRate], so an odd usage report cannot open the cap wide.
	fitMinRate, fitMaxRate = 2.0, 6.0
)

// fitRoom is the bytes this response's tool results may take: the window, less the tokens the request and its reply
// used, less the next reply's room (max_tokens plus fitMarginTokens), at the request's own bytes-per-token rate scaled
// by fitRateScale. ok is false when the server does not say its window.
func (s *Session) fitRoom(u proto.Usage, reqBytes int) (room int, ok bool) {
	w := s.cfg.Source.Window()
	if w <= 0 {
		return 0, false
	}
	rate := fitDefaultRate
	if u.Prompt > 0 && reqBytes > 0 {
		rate = min(max(fitRateScale*float64(reqBytes)/float64(u.Prompt), fitMinRate), fitMaxRate)
	}
	left := w - u.Prompt - u.Output - s.conv.MaxTokens - fitMarginTokens
	return max(0, int(float64(left)*rate)), true
}

// fit is runTool's cap when the window is fitted. A result within both MaxResultBytes and room passes whole, as does one
// under fitMinBytes; a longer one is cut to the smaller of the two, head and tail kept, with a marker saying how much
// was cut and how to narrow it; with less than fitMinBytes of room, it becomes the nearly-full note. Whatever is not
// passed whole is stored, and full holds it for the caller. The outcome stays the tool's: a cut or a note is not a
// failure, so neither counts toward Budget.MaxErrors.
func (s *Session) fit(fin proto.ToolFinished, text string, room int) (proto.ToolFinished, []byte, error) {
	limit, why := s.cfg.MaxResultBytes, fmt.Sprintf("over the %d-byte result cap", s.cfg.MaxResultBytes)
	if room < limit {
		limit, why = room, "to fit the window"
	}
	if len(text) <= limit || len(text) <= fitMinBytes {
		fin.Result = text
		return fin, nil, nil
	}
	full := []byte(text)
	handle := store.HandleOf(full)
	fin.Truncated, fin.Blob = true, handle
	if limit < fitMinBytes {
		finish := "give your answer now."
		if s.cfg.EditTask {
			finish = "make your last edit and call done."
		}
		fin.Result = fmt.Sprintf(nearlyFullOpen+": this output (%d bytes, %d lines) is not shown. Finish up: %s]",
			len(text), lineCount(text), finish)
		s.cliffNote = true // the cliff: Config.CompactAtCliff compacts before the next request
		return fin, full, nil
	}
	head, tail := wholeCut(text, limit*3/4, limit/4)
	pages := ""
	if _, ok := s.cfg.Tools.Get("read_output"); ok {
		pages = fmt.Sprintf("; read_output with handle %q pages the rest", handle)
	}
	cutLines := lineCount(text) - lineCount(head) - lineCount(tail)
	fin.Result = head + fmt.Sprintf("\n[… cut %d of %d bytes (%d of %d lines) %s; narrow it with grep, or read a line range with offset and limit%s …]\n",
		len(text)-len(head)-len(tail), len(text), max(0, cutLines), lineCount(text), why, pages) + tail
	return fin, full, nil
}

// wholeCut returns about the first head and last tail bytes of text, each short of a character it would divide. Each cut
// is followed by a marker starting with a newline, so the CR of a CRLF it divides is still a CRLF.
func wholeCut(text string, head, tail int) (string, string) {
	for i := 0; i < utf8.UTFMax-1 && head > 0 && !utf8.RuneStart(text[head]); i++ {
		head--
	}
	start := len(text) - tail
	for i := 0; i < utf8.UTFMax-1 && start < len(text) && !utf8.RuneStart(text[start]); i++ {
		start++
	}
	return text[:head], text[start:]
}

// lineCount is the lines in s, a final line without its newline included.
func lineCount(s string) int {
	n := strings.Count(s, "\n")
	if s != "" && !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}
