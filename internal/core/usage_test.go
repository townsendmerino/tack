package core

import (
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/store"
	"github.com/townsendmerino/tack/internal/usage"
)

// TestUsageSummaryIsTheTableOverTheStore: usage/summary answers with the usage table over every session in the
// daemon's store (internal/usage), from since, by UTC day when asked: the same rows tack usage prints.
func TestUsageSummaryIsTheTableOverTheStore(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []proto.SessionID{"s_a", "s_b"} {
		l, err := st.Create(id)
		if err != nil {
			t.Fatal(err)
		}
		r := 700
		if _, err := l.Append(proto.EvUsage, 0, proto.Usage{Request: 1, Server: "goinfer@nobara", Prompt: 1000, Reused: &r, Output: 40, WallMS: 1200}); err != nil {
			t.Fatal(err)
		}
		l.Close()
	}
	ts := httptest.NewServer(stuckModel())
	t.Cleanup(ts.Close)
	_, cl, _ := daemonOn(t, st, ts.URL)
	var got proto.UsageSummaryResult
	if err := cl.Call(proto.MethodUsageSummary, proto.UsageSummaryParams{UTC: true}, &got); err != nil {
		t.Fatal(err)
	}
	want, err := usage.FromStore(st, time.Time{}, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 1 || len(want) != 1 || got.Rows[0] != want[0] || got.Rows[0].Sessions != 2 || got.Rows[0].Prompt != 2000 {
		t.Fatalf("usage/summary: %+v; want the store's table %+v, one row of two sessions", got.Rows, want)
	}
	future := time.Now().Add(time.Hour)
	if err := cl.Call(proto.MethodUsageSummary, proto.UsageSummaryParams{Since: &future, UTC: true}, &got); err != nil || len(got.Rows) != 0 {
		t.Fatalf("usage/summary since an hour from now: %+v, %v; want no rows", got.Rows, err)
	}
}

// fixedUsage is a usage table with rows of its own.
type fixedUsage []proto.UsageRow

func (f fixedUsage) Table(time.Time, *time.Location) ([]proto.UsageRow, error) { return f, nil }

// TestUsageSummaryComesFromTheIndexWhenTheCoreHasOne: a core given a usage table (the daemon's index.db) answers from
// it, not from the logs.
func TestUsageSummaryComesFromTheIndexWhenTheCoreHasOne(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(stuckModel())
	t.Cleanup(ts.Close)
	index := fixedUsage{{Day: "2026-11-09", Server: "from-the-index", Requests: 4}}
	_, cl, _ := daemonOn(t, st, ts.URL, func(o *Options) { o.Usage = index })
	var got proto.UsageSummaryResult
	if err := cl.Call(proto.MethodUsageSummary, proto.UsageSummaryParams{}, &got); err != nil || len(got.Rows) != 1 || got.Rows[0].Server != "from-the-index" {
		t.Fatalf("usage/summary with an index: %+v, %v; want the index's row", got.Rows, err)
	}
}

// failingUsage is a usage table that cannot give its rows.
type failingUsage struct{}

func (failingUsage) Table(time.Time, *time.Location) ([]proto.UsageRow, error) {
	return nil, errors.New("database is locked")
}

// TestUsageSummaryReadsTheLogsWhenTheIndexFails: an index that fails (a lock held past its timeout) does not fail
// usage/summary: the core reads the logs.
func TestUsageSummaryReadsTheLogsWhenTheIndexFails(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	l, err := st.Create("s_a")
	if err != nil {
		t.Fatal(err)
	}
	l.Append(proto.EvUsage, 0, proto.Usage{Request: 1, Server: "from-the-logs", Prompt: 5})
	l.Close()
	ts := httptest.NewServer(stuckModel())
	t.Cleanup(ts.Close)
	_, cl, _ := daemonOn(t, st, ts.URL, func(o *Options) { o.Usage = failingUsage{} })
	var got proto.UsageSummaryResult
	if err := cl.Call(proto.MethodUsageSummary, proto.UsageSummaryParams{}, &got); err != nil || len(got.Rows) != 1 || got.Rows[0].Server != "from-the-logs" {
		t.Fatalf("usage/summary with a failing index: %+v, %v; want the logs' row", got.Rows, err)
	}
}

// TestUsageSummaryCountsTheClientsDays: a client names its time zone, and the daemon counts days in it, not in its
// own; zones 25 hours apart put the same request on different dates.
func TestUsageSummaryCountsTheClientsDays(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	l, err := st.Create("s_a")
	if err != nil {
		t.Fatal(err)
	}
	ev, _ := l.Append(proto.EvUsage, 0, proto.Usage{Request: 1, Server: "goinfer@local", Prompt: 5})
	l.Close()
	ts := httptest.NewServer(stuckModel())
	t.Cleanup(ts.Close)
	_, cl, _ := daemonOn(t, st, ts.URL)
	for _, zone := range []string{"Pacific/Kiritimati", "Pacific/Pago_Pago"} {
		loc, err := time.LoadLocation(zone)
		if err != nil {
			t.Skip("no zone database here:", err)
		}
		var got proto.UsageSummaryResult
		if err := cl.Call(proto.MethodUsageSummary, proto.UsageSummaryParams{TZ: zone}, &got); err != nil {
			t.Fatal(err)
		}
		if want := ev.Time.In(loc).Format(time.DateOnly); len(got.Rows) != 1 || got.Rows[0].Day != want {
			t.Fatalf("in %s: %+v; want the request on %s", zone, got.Rows, want)
		}
	}
	var got proto.UsageSummaryResult
	if err := cl.Call(proto.MethodUsageSummary, proto.UsageSummaryParams{TZ: "Not/AZone"}, &got); err == nil {
		t.Fatalf("an unknown zone: %+v; want refused, so the client counts itself", got.Rows)
	}
}
