package core

import (
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
