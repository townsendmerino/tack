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
	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/store"
	usagetable "github.com/townsendmerino/tack/internal/usage"
)

// usageNow is the clock usageCmd reads; tests replace it.
var usageNow = time.Now

// usageCmd is `tack usage` (DESIGN §10, §7.7): the usage table over every session in the state directory, by day and
// model server. It only reads. A config that is there but wrong fails loudly; with none, the default state directory
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
	st, err := store.Open(dir)
	if err != nil {
		fmt.Fprintln(stderr, "tack usage:", err)
		return exitError
	}
	ids, err := st.Sessions()
	if err != nil {
		fmt.Fprintln(stderr, "tack usage:", err)
		return exitError
	}
	logs := map[proto.SessionID][]proto.Event{}
	for _, id := range ids {
		evs, err := st.Events(id, 0)
		if err != nil {
			fmt.Fprintln(stderr, "tack usage:", err)
			return exitError
		}
		logs[id] = evs
	}
	rows, err := usagetable.Table(logs, since, loc)
	if err != nil {
		fmt.Fprintln(stderr, "tack usage:", err)
		return exitError
	}
	usagetable.Print(stdout, rows)
	return exitOK
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
