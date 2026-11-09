package usage

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // index.db's driver: pure Go, no cgo (DESIGN §5.3)

	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/store"
)

// IndexName is the derived index in tack's state directory (DESIGN §5.3, §6.2 "Derived state"): deleting it loses
// nothing, and the next read rebuilds what it held.
const IndexName = "index.db"

// cacheSchema is the version of the usage tables; a database at another version has them dropped and rebuilt.
const cacheSchema = 1

// Cache is the usage table kept in index.db: one row per usage event, and how far each session's log has been read.
// A Table catches each log up from where it was last read (store.ScanFrom), so a log line is read once, whoever wrote
// it: this daemon, another tack process, or a tack run without the daemon. Days are counted at query time, so any time
// zone gives its own days. More than one process may use the file at once; SQLite's locking serializes the writes.
type Cache struct {
	db *sql.DB
	st *store.Store
}

// OpenCache opens (or makes) index.db in stateDir for the sessions in st.
func OpenCache(stateDir string, st *store.Store) (*Cache, error) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(stateDir, IndexName)+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(wal)")
	if err != nil {
		return nil, err
	}
	c := &Cache{db: db, st: st}
	if err := c.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("usage: %s: %w", IndexName, err)
	}
	return c, nil
}

// Close closes the database.
func (c *Cache) Close() error { return c.db.Close() }

func (c *Cache) migrate() error {
	var v int
	if err := c.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return err
	}
	if v == cacheSchema {
		return nil
	}
	_, err := c.db.Exec(`
DROP TABLE IF EXISTS usage_read;
DROP TABLE IF EXISTS usage_events;
CREATE TABLE usage_read (session TEXT PRIMARY KEY, offset INTEGER NOT NULL, next_seq INTEGER NOT NULL);
CREATE TABLE usage_events (
	session TEXT NOT NULL, seq INTEGER NOT NULL, at INTEGER NOT NULL, server TEXT NOT NULL,
	prompt INTEGER NOT NULL, output INTEGER NOT NULL, reused INTEGER, prefilled INTEGER, prefill_ms INTEGER,
	wall_ms INTEGER NOT NULL,
	PRIMARY KEY (session, seq));
CREATE INDEX usage_events_at ON usage_events (at);
PRAGMA user_version = ` + fmt.Sprint(cacheSchema))
	return err
}

// scanned, when set (tests), is told how many events each catch-up read from a log.
var scanned func(int)

// Table is the usage table from since, by day in loc: what FromStore gives, from index.db once each log is caught up.
func (c *Cache) Table(since time.Time, loc *time.Location) ([]Row, error) {
	if err := c.catchUp(); err != nil {
		return nil, err
	}
	from := int64(math.MinInt64) // since's zero is before every event; its UnixNano is not
	if !since.IsZero() {
		from = since.UnixNano()
	}
	rows, err := c.db.Query(`SELECT session, at, server, prompt, output, reused, prefilled, prefill_ms, wall_ms
		FROM usage_events WHERE at >= ? ORDER BY at, session, seq`, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	acc := NewAcc(since, loc)
	for rows.Next() {
		var id string
		var at int64
		var u proto.Usage
		var reused, prefilled, prefillMS sql.NullInt64
		if err := rows.Scan(&id, &at, &u.Server, &u.Prompt, &u.Output, &reused, &prefilled, &prefillMS, &u.WallMS); err != nil {
			return nil, err
		}
		u.Reused, u.Prefilled, u.PrefillMS = intOrNil(reused), intOrNil(prefilled), intOrNil(prefillMS)
		acc.add(proto.SessionID(id), time.Unix(0, at), u)
	}
	return acc.Rows(), rows.Err()
}

func intOrNil(n sql.NullInt64) *int {
	if !n.Valid {
		return nil
	}
	v := int(n.Int64)
	return &v
}

func nullInt(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

// catchUp reads each session's log from where index.db last read it, recording its usage events and the new place in
// one transaction per session. A log that no longer matches what was read (shorter, or its seqs off) is read again
// from the start.
func (c *Cache) catchUp() error {
	ids, err := c.st.Sessions()
	if err != nil {
		return err
	}
	live := map[string]bool{}
	for _, id := range ids {
		live[string(id)] = true
		err := c.catchUpSession(id)
		if errors.Is(err, fs.ErrNotExist) { // deleted since it was listed: dropped below, as it is from the logs' table
			delete(live, string(id))
			continue
		}
		if err != nil {
			return fmt.Errorf("usage: session %s: %w", id, err)
		}
	}
	return c.forgetExcept(live)
}

// forgetExcept drops what index.db holds of every session not in live: a session whose log is gone is gone from the
// table, as it is from the logs'.
func (c *Cache) forgetExcept(live map[string]bool) error {
	rows, err := c.db.Query(`SELECT session FROM usage_read`)
	if err != nil {
		return err
	}
	var gone []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		if !live[id] {
			gone = append(gone, id)
		}
	}
	rows.Close()
	for _, id := range gone {
		if _, err := c.db.Exec(`DELETE FROM usage_events WHERE session = ?`, id); err != nil {
			return err
		}
		if _, err := c.db.Exec(`DELETE FROM usage_read WHERE session = ?`, id); err != nil {
			return err
		}
	}
	return nil
}

func (c *Cache) catchUpSession(id proto.SessionID) error {
	var off, next int64 = 0, 1
	err := c.db.QueryRow(`SELECT offset, next_seq FROM usage_read WHERE session = ?`, string(id)).Scan(&off, &next)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	n := 0
	read := func(ev proto.Event) error {
		n++
		if ev.Type != proto.EvUsage {
			return nil
		}
		v, err := ev.Decode()
		if err != nil {
			return fmt.Errorf("seq %d: %w", ev.Seq, err)
		}
		u := v.(proto.Usage)
		_, err = tx.Exec(`INSERT OR REPLACE INTO usage_events VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, string(id), ev.Seq,
			ev.Time.UnixNano(), u.Server, u.Prompt, u.Output, nullInt(u.Reused), nullInt(u.Prefilled), nullInt(u.PrefillMS), u.WallMS)
		return err
	}
	end, nextSeq, err := c.st.ScanFrom(id, off, next, read)
	if err != nil && off > 0 { // what was read no longer matches the log: read it again, whole
		if _, derr := tx.Exec(`DELETE FROM usage_events WHERE session = ?`, string(id)); derr != nil {
			return derr
		}
		n = 0
		end, nextSeq, err = c.st.ScanFrom(id, 0, 1, read)
	}
	if err != nil {
		return err
	}
	if scanned != nil {
		scanned(n)
	}
	if end == off {
		return nil
	}
	if _, err := tx.Exec(`INSERT OR REPLACE INTO usage_read VALUES (?, ?, ?)`, string(id), end, nextSeq); err != nil {
		return err
	}
	return tx.Commit()
}
