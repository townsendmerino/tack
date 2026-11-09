package usage

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/store"
)

// appendUsage writes n usage events to session id, some with reuse reported and some without.
func appendUsage(t *testing.T, st *store.Store, id proto.SessionID, n int, server string) {
	t.Helper()
	l, err := st.OpenLog(id)
	if err != nil { // a new session
		if l, err = st.Create(id); err != nil {
			t.Fatal(err)
		}
	}
	defer l.Close()
	for i := range n {
		u := proto.Usage{Request: i + 1, Server: server, Prompt: 1000 + i, Output: 10 + i, WallMS: 500}
		if i%2 == 0 {
			r, ms := 600+i, 40
			u.Reused, u.PrefillMS = &r, &ms
		}
		if _, err := l.Append(proto.EvUsage, 0, u); err != nil {
			t.Fatal(err)
		}
		if _, err := l.Append(proto.EvRequestSent, 0, proto.RequestSent{Request: i + 1, Server: server, Model: "m"}); err != nil { // not a usage event
			t.Fatal(err)
		}
	}
}

// TestTheCacheIsTheTableOverTheLogs: the table from index.db is the table from the logs, at every step: first
// read, after more usage is written (it catches up from where it read), and after index.db is deleted (rebuilt).
func TestTheCacheIsTheTableOverTheLogs(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	appendUsage(t, st, "s_a", 3, "goinfer@local")
	appendUsage(t, st, "s_b", 2, "goinfer@nobara")
	check := func(step string) {
		t.Helper()
		c, err := OpenCache(dir, st)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		for _, loc := range []*time.Location{time.UTC, time.Local} {
			got, err := c.Table(time.Time{}, loc)
			if err != nil {
				t.Fatal(err)
			}
			want, err := FromStore(st, time.Time{}, loc)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) || len(want) == 0 {
				t.Fatalf("%s (%s): the cache's table\n%+v\nwant the logs'\n%+v", step, loc, got, want)
			}
		}
	}
	check("first read")
	appendUsage(t, st, "s_a", 2, "goinfer@local")
	appendUsage(t, st, "s_c", 1, "goinfer@nobara")
	check("after more usage")
	if err := os.Remove(filepath.Join(dir, IndexName)); err != nil {
		t.Fatal(err)
	}
	check("after index.db is deleted")
}

// TestTheCacheReadsEachLogOnce: a second table reads no log line it has read: what it has is in index.db.
func TestTheCacheReadsEachLogOnce(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	appendUsage(t, st, "s_a", 3, "goinfer@local")
	c, err := OpenCache(dir, st)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Table(time.Time{}, time.UTC); err != nil {
		t.Fatal(err)
	}
	read := 0
	scanned = func(n int) { read += n }
	t.Cleanup(func() { scanned = nil })
	if _, err := c.Table(time.Time{}, time.UTC); err != nil || read != 0 {
		t.Fatalf("a second table read %d events (%v); want none", read, err)
	}
	appendUsage(t, st, "s_a", 1, "goinfer@local")
	if _, err := c.Table(time.Time{}, time.UTC); err != nil || read != 2 {
		t.Fatalf("after one more usage and turn: read %d events (%v); want the 2 new ones", read, err)
	}
}

// TestTheCacheCountsFromSince: a since between two events counts only the later ones, as FromStore does.
func TestTheCacheCountsFromSince(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	appendUsage(t, st, "s_a", 2, "goinfer@local")
	evs, err := st.Events("s_a", 0)
	if err != nil {
		t.Fatal(err)
	}
	since := evs[len(evs)-1].Time.Add(time.Nanosecond) // after them all
	time.Sleep(2 * time.Millisecond)
	appendUsage(t, st, "s_a", 1, "goinfer@local")
	c, err := OpenCache(dir, st)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	got, err := c.Table(since, time.UTC)
	want, _ := FromStore(st, since, time.UTC)
	if err != nil || !reflect.DeepEqual(got, want) || len(got) != 1 || got[0].Requests != 1 {
		t.Fatalf("from since: %+v (%v); want the one later request, as the logs give %+v", got, err, want)
	}
}

// TestAStaleReadPositionIsReadAgain: a recorded place a log no longer has (index.db from elsewhere, or a log put
// back from a backup) is not trusted: the log is read again from the start.
func TestAStaleReadPositionIsReadAgain(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	appendUsage(t, st, "s_a", 2, "goinfer@local")
	c, err := OpenCache(dir, st)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Table(time.Time{}, time.UTC); err != nil {
		t.Fatal(err)
	}
	if _, err := c.db.Exec(`UPDATE usage_read SET offset = offset + 100000, next_seq = next_seq + 7`); err != nil {
		t.Fatal(err)
	}
	got, err := c.Table(time.Time{}, time.UTC)
	want, _ := FromStore(st, time.Time{}, time.UTC)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("after a stale place: %+v (%v); want the logs' %+v", got, err, want)
	}
}

// TestADeletedSessionLeavesTheTable: the index derives from the logs, so a session whose log is gone is gone from the
// table too, as it is from the logs' table.
func TestADeletedSessionLeavesTheTable(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	appendUsage(t, st, "s_a", 1, "goinfer@local")
	appendUsage(t, st, "s_b", 1, "goinfer@nobara")
	c, err := OpenCache(dir, st)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Table(time.Time{}, time.UTC); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(dir, "sessions", "s_b")); err != nil {
		t.Fatal(err)
	}
	got, err := c.Table(time.Time{}, time.UTC)
	want, _ := FromStore(st, time.Time{}, time.UTC)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("after a session is deleted: %+v (%v); want the logs' %+v", got, err, want)
	}
}

// TestTablesAtOnce: the daemon answers clients at once; tables read together, while usage is written, each match the
// logs once the writes are done, with no row counted twice.
func TestTablesAtOnce(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	appendUsage(t, st, "s_a", 2, "goinfer@local")
	c, err := OpenCache(dir, st)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	errs := make(chan error, 8)
	for range 8 {
		go func() { _, err := c.Table(time.Time{}, time.UTC); errs <- err }()
	}
	appendUsage(t, st, "s_b", 2, "goinfer@nobara")
	for range 8 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	got, err := c.Table(time.Time{}, time.UTC)
	want, _ := FromStore(st, time.Time{}, time.UTC)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("after tables at once: %+v (%v); want the logs' %+v", got, err, want)
	}
}
