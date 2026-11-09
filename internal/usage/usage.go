// Package usage is the usage table (DESIGN §7.7, §10's `tack usage`): what each day's requests cost, by model server,
// summed from the usage events in the session logs. It reads the logs each time; nothing is cached, so nothing is
// lost if the state directory's derived files go (DESIGN §6.2, "Derived state").
package usage

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/townsendmerino/tack/internal/proto"
)

// Row is one day's requests to one server. Reused and Prefilled sum only the requests whose server reported them
// (Known of Requests): a server that does not say how much of a prompt came from its cache leaves them unknown, never
// zero. PrefillMS likewise sums the requests that report it (PrefillKnown).
type Row struct {
	Day          string // YYYY-MM-DD in the table's time zone
	Server       string
	Sessions     int
	Requests     int
	Prompt       int // prompt tokens, as the server counted them
	Output       int
	Known        int // requests that reported reuse
	KnownPrompt  int // the prompt tokens of those requests
	Reused       int
	Prefilled    int
	PrefillKnown int
	PrefillMS    int
	WallMS       int
}

// ReuseShare is the share of the known requests' prompt tokens served from the cached prefix, and false when no
// request said.
func (r Row) ReuseShare() (float64, bool) {
	if r.Known == 0 || r.KnownPrompt == 0 {
		return 0, false
	}
	return float64(r.Reused) / float64(r.KnownPrompt), true
}

// Table sums the usage events of each session's log at or after since, by day in loc and server, sorted by day then
// server. logs maps a session to its events.
func Table(logs map[proto.SessionID][]proto.Event, since time.Time, loc *time.Location) ([]Row, error) {
	type key struct{ day, server string }
	rows := map[key]*Row{}
	seen := map[key]map[proto.SessionID]bool{}
	for id, evs := range logs {
		for _, ev := range evs {
			if ev.Type != proto.EvUsage || ev.Time.Before(since) {
				continue
			}
			v, err := ev.Decode()
			if err != nil {
				return nil, fmt.Errorf("usage: session %s seq %d: %w", id, ev.Seq, err)
			}
			u := v.(proto.Usage)
			k := key{ev.Time.In(loc).Format(time.DateOnly), u.Server}
			r := rows[k]
			if r == nil {
				r = &Row{Day: k.day, Server: k.server}
				rows[k], seen[k] = r, map[proto.SessionID]bool{}
			}
			if !seen[k][id] {
				seen[k][id] = true
				r.Sessions++
			}
			r.Requests++
			r.Prompt += u.Prompt
			r.Output += u.Output
			r.WallMS += u.WallMS
			if u.Reused != nil {
				r.Known++
				r.KnownPrompt += u.Prompt
				r.Reused += *u.Reused
				if u.Prefilled != nil {
					r.Prefilled += *u.Prefilled
				} else {
					r.Prefilled += u.Prompt - *u.Reused
				}
			}
			if u.PrefillMS != nil {
				r.PrefillKnown++
				r.PrefillMS += *u.PrefillMS
			}
		}
	}
	out := make([]Row, 0, len(rows))
	for _, r := range rows {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Day != out[j].Day {
			return out[i].Day < out[j].Day
		}
		return out[i].Server < out[j].Server
	})
	return out, nil
}

// Total sums rows into one, with Day "total" and Server "" (Sessions sums the rows' and so counts a session once per
// day and server it used).
func Total(rows []Row) Row {
	t := Row{Day: "total"}
	for _, r := range rows {
		t.Sessions += r.Sessions
		t.Requests += r.Requests
		t.Prompt += r.Prompt
		t.Output += r.Output
		t.Known += r.Known
		t.KnownPrompt += r.KnownPrompt
		t.Reused += r.Reused
		t.Prefilled += r.Prefilled
		t.PrefillKnown += r.PrefillKnown
		t.PrefillMS += r.PrefillMS
		t.WallMS += r.WallMS
	}
	return t
}

// Print writes rows as a table with a total line: tokens as the servers counted them, and "—" where no request said.
// The reuse share is over the requests that reported reuse, and the column says how many of the day's did.
func Print(w io.Writer, rows []Row) {
	fmt.Fprintf(w, "%-10s  %-22s  %8s  %8s  %10s  %8s  %16s  %10s  %9s\n", "day", "server", "sessions", "requests", "prompt",
		"output", "reused (known)", "prefill s", "wall s")
	line := func(r Row) {
		reuse := "—"
		if s, ok := r.ReuseShare(); ok {
			reuse = fmt.Sprintf("%.0f%% (%d/%d)", 100*s, r.Known, r.Requests)
		}
		prefill := "—"
		if r.PrefillKnown > 0 {
			prefill = fmt.Sprintf("%.1f", float64(r.PrefillMS)/1000)
		}
		fmt.Fprintf(w, "%-10s  %-22s  %8d  %8d  %10d  %8d  %16s  %10s  %9.1f\n", r.Day, r.Server, r.Sessions, r.Requests,
			r.Prompt, r.Output, reuse, prefill, float64(r.WallMS)/1000)
	}
	for _, r := range rows {
		line(r)
	}
	if len(rows) == 0 {
		fmt.Fprintln(w, "(no requests in range)")
		return
	}
	fmt.Fprintln(w, strings.Repeat("-", 10))
	line(Total(rows))
}
