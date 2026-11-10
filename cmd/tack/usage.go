package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"strconv"
	"strings"
	"time"

	"github.com/townsendmerino/tack/internal/config"
	"github.com/townsendmerino/tack/internal/daemon"
	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/store"
	usagetable "github.com/townsendmerino/tack/internal/usage"
)

// usageNow is the clock usageCmd reads; tests replace it.
var usageNow = time.Now

// usageCmd is `tack usage` (DESIGN §10, §7.7): the usage table over every session in the state directory, by day and
// model server. A running daemon answers it (usage/summary); with none, or one from before that method, it reads the
// table itself (localUsage). It changes nothing but the derived index.db, and starts no daemon. A config that is there but wrong fails loudly; with none, the default state directory
// is read.
func usageCmd(args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("usage", flag.ContinueOnError)
	fl.SetOutput(stderr)
	cfgPath := fl.String("config", "", "config file (default ~/.config/tack/config.toml)")
	sinceFlag := fl.String("since", "7d", `the first day counted: a date (2006-01-02) or a number of days back ("7d"); "all" for everything`)
	utc := fl.Bool("utc", false, "group by UTC day instead of the local one")
	if err := fl.Parse(args); err != nil || fl.NArg() != 0 {
		return exitUsage
	}
	loc := time.Local
	if *utc {
		loc = time.UTC
	}
	since, err := parseSince(*sinceFlag, usageNow().In(loc))
	if err != nil {
		fmt.Fprintln(stderr, "tack usage:", err)
		return exitUsage
	}
	dir, err := usageStateDir(*cfgPath)
	if err != nil {
		fmt.Fprintln(stderr, "tack usage:", err)
		return exitError
	}
	rows, err := daemonUsage(dir, since, *utc)
	if errors.Is(err, errNoUsageDaemon) {
		rows, err = localUsage(dir, since, loc)
	}
	if err != nil {
		fmt.Fprintln(stderr, "tack usage:", err)
		return exitError
	}
	usagetable.Print(stdout, rows)
	return exitOK
}

// errNoUsageDaemon is daemonUsage finding no daemon that answers usage/summary: none runs, or one from before the method.
var errNoUsageDaemon = errors.New("no daemon answers usage/summary")

// daemonUsage asks the daemon in stateDir for the table (usage/summary), without starting one: a running daemon answers
// from its own reading of the logs. It is errNoUsageDaemon when none runs or the one running predates the method; then
// the caller reads the logs itself.
func daemonUsage(stateDir string, since time.Time, utc bool) ([]usagetable.Row, error) {
	c, err := daemon.Dial(stateDir, cliClient, nil, 0)
	if errors.Is(err, daemon.ErrNotRunning) {
		return nil, errNoUsageDaemon
	}
	if err != nil {
		return nil, err
	}
	defer c.Close()
	p := proto.UsageSummaryParams{UTC: utc}
	if !since.IsZero() {
		p.Since = &since
	}
	var res proto.UsageSummaryResult
	err = c.Call(proto.MethodUsageSummary, p, &res)
	if pe, ok := errors.AsType[*proto.Error](err); ok && pe.Code == proto.CodeMethodNotFound {
		return nil, errNoUsageDaemon
	}
	if err != nil {
		return nil, fmt.Errorf("asking the daemon: %w", err)
	}
	return res.Rows, nil
}

// parseSince is the start of the first day counted: a date, a number of days back from today ("1d" is today alone),
// or "all".
func parseSince(s string, now time.Time) (time.Time, error) {
	if s == "all" {
		return time.Time{}, nil
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	if d, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(d)
		if err != nil || n < 1 {
			return time.Time{}, fmt.Errorf("-since %q: want a date, a number of days like 7d, or all", s)
		}
		return today.AddDate(0, 0, 1-n), nil
	}
	t, err := time.ParseInLocation(time.DateOnly, s, now.Location())
	if err != nil {
		return time.Time{}, fmt.Errorf("-since %q: want a date, a number of days like 7d, or all", s)
	}
	return t, nil
}

// usageStateDir is the config's state directory, or the default one when no config file exists at the default path.
func usageStateDir(cfgPath string) (string, error) {
	if cfgPath == "" {
		p, err := config.Path()
		if err != nil {
			return "", err
		}
		cfg, err := config.Load(p)
		if errors.Is(err, fs.ErrNotExist) {
			return config.StateDir()
		}
		if err != nil {
			return "", err
		}
		if cfg.StateDir != "" {
			return cfg.StateDir, nil
		}
		return config.StateDir()
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return "", err
	}
	if cfg.StateDir != "" {
		return cfg.StateDir, nil
	}
	return config.StateDir()
}

// localUsage is the table read in this process: from index.db, caught up from the logs (usage.Cache), or from the
// logs alone when index.db cannot be opened or cannot give the table. Deleting index.db loses nothing; the next read rebuilds it.
func localUsage(dir string, since time.Time, loc *time.Location) ([]usagetable.Row, error) {
	st, err := store.Open(dir)
	if err != nil {
		return nil, err
	}
	c, err := usagetable.OpenCache(dir, st)
	if err != nil {
		return usagetable.FromStore(st, since, loc)
	}
	defer c.Close()
	if rows, err := c.Table(since, loc); err == nil {
		return rows, nil
	}
	return usagetable.FromStore(st, since, loc) // the index failed (a lock held past its timeout, say); the logs are the truth
}
