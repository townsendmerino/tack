package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
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

// errNoUsageDaemon is daemonUsage getting no table from a daemon: none runs, one from before the method, or one that
// failed to answer. The caller then reads the table itself; the logs are the truth either way.
var errNoUsageDaemon = errors.New("no daemon answers usage/summary")

// daemonUsage asks the daemon in stateDir for the table (usage/summary), without starting one: a running daemon answers
// from its own reading of the logs, counting this client's days (localZoneName). Any failure is errNoUsageDaemon.
func daemonUsage(stateDir string, since time.Time, utc bool) ([]usagetable.Row, error) {
	c, err := daemon.Dial(stateDir, cliClient, nil, 0)
	if err != nil {
		return nil, errNoUsageDaemon
	}
	defer c.Close()
	p := proto.UsageSummaryParams{UTC: utc}
	if !utc {
		p.TZ = localZoneName() // the daemon counts this client's days, not its own
	}
	if !since.IsZero() {
		p.Since = &since
	}
	var res proto.UsageSummaryResult
	if err := c.Call(proto.MethodUsageSummary, p, &res); err != nil {
		return nil, errNoUsageDaemon // an older daemon (method not found), an unknown zone, or anything else
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

// localZoneName is the IANA name of this process's time zone, which time.Local is but does not say: TZ when it is set
// (the OS's variable, which time.Local itself reads; empty means UTC), else what /etc/localtime links to under
// zoneinfo/. It is "" when neither names one; the daemon then counts its own days, which on this machine are the same.
func localZoneName() string {
	// TZ is the OS's own variable, read only to name the zone time.Local already uses; it is not a tack setting.
	if tz, ok := os.LookupEnv("TZ"); ok {
		if tz = strings.TrimPrefix(tz, ":"); tz == "" {
			return "UTC"
		} else if !strings.HasPrefix(tz, "/") {
			return tz
		}
		return ""
	}
	target, err := os.Readlink("/etc/localtime")
	if err != nil {
		return ""
	}
	if _, name, ok := strings.Cut(target, "zoneinfo/"); ok {
		return name
	}
	return ""
}
