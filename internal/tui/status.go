package tui

import (
	"fmt"
	"strings"

	"github.com/townsendmerino/tack/internal/proto"
)

// Status is what the TUI's status line shows (DESIGN §10): the model and its server, a load in progress ("loading model
// on nobara"), the mode, and the window's fill with its reused share. Apply keeps it current from the session's
// events; Window, the served window, is the client's to set.
type Status struct {
	Model  string
	Server string
	Load   string // a load in progress or a move, as model.status says it; "" once the server serves
	Mode   proto.Mode
	Prompt int // the last request's prompt tokens
	Reused int // of them, the ones the server reused from its cache
	Window int
}

// Apply updates the status from one of the session's events.
func (s *Status) Apply(ev proto.Event) {
	v, err := ev.Decode()
	if err != nil {
		return
	}
	switch e := v.(type) {
	case proto.SessionCreated:
		s.Model, s.Mode = e.Model, e.Mode
	case proto.ModelStatus:
		s.Server, s.Load = e.Server, ""
		if e.State != "ready" { // loading, or moved to another server under memory pressure
			s.Load = e.Message
		}
	case proto.RequestSent:
		s.Model, s.Server = e.Model, e.Server
	case proto.Usage:
		s.Server, s.Prompt, s.Reused = e.Server, e.Prompt, 0
		if e.Reused != nil {
			s.Reused = *e.Reused
		}
	}
}

// Line is the status on one line, its parts joined by " · ".
func (s Status) Line() string {
	var parts []string
	who := s.Model
	if s.Server != "" {
		who += " @ " + s.Server
	}
	parts = append(parts, strings.TrimSpace(who))
	if s.Load != "" {
		parts = append(parts, s.Load)
	}
	if s.Mode != "" {
		parts = append(parts, string(s.Mode))
	}
	if s.Prompt > 0 {
		fill := kilo(s.Prompt)
		if s.Window > 0 {
			fill += "/" + kilo(s.Window)
		}
		parts = append(parts, fmt.Sprintf("%s (%.0f%% reused)", fill, 100*float64(s.Reused)/float64(s.Prompt)))
	}
	return strings.Join(parts, " · ")
}

// kilo writes a token count in thousands, to one decimal.
func kilo(n int) string { return fmt.Sprintf("%.1fk", float64(n)/1000) }
