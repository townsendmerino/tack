// Package usage is the usage table (DESIGN §7.7, §10's `tack usage`): what each day's requests cost, by model server,
// summed from the usage events in the session logs. FromStore reads the logs each time; Cache keeps the usage events
// in index.db and reads each log line once. index.db is derived, so deleting it loses nothing (DESIGN §6.2, "Derived
// state"). The daemon answers usage/summary with it, and tack usage reads it when no daemon runs.
package usage

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/store"
)

// Row is one day's requests to one server: the protocol's row (proto.UsageRow), which usage/summary answers with.
// Reused and Prefilled sum only the requests whose server reported them (Known of Requests): a server that does not
// say how much of a prompt came from its cache leaves them unknown, never zero. PrefillMS likewise sums the requests
// that report it (PrefillKnown).
type Row = proto.UsageRow

// Table sums the usage events of each session's log at or after since, by day in loc and server, sorted by day then
// server. logs maps a session to its events.
func Table(logs map[proto.SessionID][]proto.Event, since time.Time, loc *time.Location) ([]Row, error) {
	acc := NewAcc(since, loc)
	for id, evs := range logs {
		for _, ev := range evs {
			if err := acc.Add(id, ev); err != nil {
				return nil, err
			}
		}
	}
	return acc.Rows(), nil
}

type key struct{ day, server string }

// Acc builds the table one event at a time, so a caller can stream the logs through it (store.Scan) instead of
// holding them.
type Acc struct {
	since time.Time
	loc   *time.Location
	rows  map[key]*Row
	seen  map[key]map[proto.SessionID]bool
	// child is the sessions a task call made (SessionCreated.Parent): their usage counts, but they are not sessions the
	// user had, so Sessions leaves them out.
	child map[proto.SessionID]bool
}

// NewAcc is an empty table counting usage events at or after since, by day in loc.
func NewAcc(since time.Time, loc *time.Location) *Acc {
	return &Acc{since: since, loc: loc, rows: map[key]*Row{}, seen: map[key]map[proto.SessionID]bool{}, child: map[proto.SessionID]bool{}}
}

// Add counts ev, an event of session id, if it is a usage event in range; it ignores every other event but a
// session.created, which says whether id is a subagent's (counted, but not as a session). A session's events come in
// log order, so its session.created comes first.
func (a *Acc) Add(id proto.SessionID, ev proto.Event) error {
	if ev.Type == proto.EvSessionCreated {
		if v, err := ev.Decode(); err == nil && v.(proto.SessionCreated).Parent != "" {
			a.child[id] = true
		}
		return nil
	}
	if ev.Type != proto.EvUsage || ev.Time.Before(a.since) {
		return nil
	}
	v, err := ev.Decode()
	if err != nil {
		return fmt.Errorf("usage: session %s seq %d: %w", id, ev.Seq, err)
	}
	a.add(id, ev.Time, v.(proto.Usage))
	return nil
}

// add counts u, a usage event of session id at at, in range.
func (a *Acc) add(id proto.SessionID, at time.Time, u proto.Usage) {
	k := key{at.In(a.loc).Format(time.DateOnly), u.Server}
	r := a.rows[k]
	if r == nil {
		r = &Row{Day: k.day, Server: k.server}
		a.rows[k], a.seen[k] = r, map[proto.SessionID]bool{}
	}
	if !a.seen[k][id] {
		a.seen[k][id] = true
		if !a.child[id] {
			r.Sessions++
		}
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

// Rows is the table so far, sorted by day then server.
func (a *Acc) Rows() []Row {
	out := make([]Row, 0, len(a.rows))
	for _, r := range a.rows {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Day != out[j].Day {
			return out[i].Day < out[j].Day
		}
		return out[i].Server < out[j].Server
	})
	return out
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
// The reuse share and the prefill time are each over the requests that reported them, and each column says how many
// of the row's requests did.
func Print(w io.Writer, rows []Row) {
	fmt.Fprintf(w, "%-10s  %-22s  %8s  %8s  %10s  %8s  %16s  %18s  %9s\n", "day", "server", "sessions", "requests", "prompt",
		"output", "reused (known)", "prefill s (known)", "wall s")
	line := func(r Row) {
		reuse := "—"
		if s, ok := r.ReuseShare(); ok {
			reuse = fmt.Sprintf("%.0f%% (%d/%d)", 100*s, r.Known, r.Requests)
		}
		prefill := "—"
		if r.PrefillKnown > 0 {
			prefill = fmt.Sprintf("%.1f (%d/%d)", float64(r.PrefillMS)/1000, r.PrefillKnown, r.Requests)
		}
		fmt.Fprintf(w, "%-10s  %-22s  %8d  %8d  %10d  %8d  %16s  %18s  %9.1f\n", r.Day, r.Server, r.Sessions, r.Requests,
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

// listed, when set (tests), runs in FromStore between listing the sessions and reading them.
var listed func()

// FromStore is the table over every session in st: each log streams through it one event at a time (store.Scan), so
// only the table is held, never a log. Both tack usage, when no daemon runs, and the daemon's usage/summary read it.
func FromStore(st *store.Store, since time.Time, loc *time.Location) ([]Row, error) {
	ids, err := st.Sessions()
	if err != nil {
		return nil, err
	}
	if listed != nil {
		listed()
	}
	acc := NewAcc(since, loc)
	for _, id := range ids {
		err := st.Scan(id, func(ev proto.Event) error { return acc.Add(id, ev) })
		if errors.Is(err, fs.ErrNotExist) { // deleted since it was listed: not in the table, as if deleted before
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("usage: session %s: %w", id, err)
		}
	}
	return acc.Rows(), nil
}

// markChild says session id is a subagent (its session.created has a Parent). It must precede id's usage events, as
// session.created does in a log.
func (a *Acc) markChild(id proto.SessionID) { a.child[id] = true }
