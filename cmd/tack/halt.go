package main

import (
	"flag"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/townsendmerino/tack/internal/config"
	"github.com/townsendmerino/tack/internal/halt"
	"github.com/townsendmerino/tack/internal/sandbox"
)

// sweep is sweepTarget; tests replace it, so that no test signals a real process.
var sweep = sweepTarget

// haltCmd is `tack halt` (DESIGN §6.6). It creates the HALT file, which every running tack watches, then sweeps each
// registered session's sandbox (internal/halt.Targets): only processes carrying that session's mark that started after
// it did, and nothing at all when the selection looks wrong (sandbox.Plan). With -dry-run it changes nothing and lists
// what each sweep would kill. It must work when nothing else does, so a config it cannot read falls back to the
// default state directory, with a warning.
func haltCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("halt", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "config file (default ~/.config/tack/config.toml)")
	reason := fs.String("reason", "", "why, recorded in each halted session's log")
	dryRun := fs.Bool("dry-run", false, "halt nothing and kill nothing: list what each session's sweep would kill")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return exitUsage
	}
	dir, err := haltStateDir(*cfgPath, stderr)
	if err != nil {
		fmt.Fprintln(stderr, "tack halt:", err)
		return exitError
	}
	code := exitOK
	if !*dryRun {
		was, err := halt.Halt(dir, "tack halt", *reason)
		if err != nil {
			fmt.Fprintln(stderr, "tack halt:", err)
			return exitError
		}
		state := "halted"
		if was {
			state = "already halted"
		}
		fmt.Fprintf(stdout, "tack is %s (%s); tack resume lifts it.\n", state, halt.Path(dir))
	}
	targets, err := halt.Targets(dir)
	if err != nil {
		fmt.Fprintln(stderr, "tack halt: reading the sessions to sweep:", err)
		return exitError
	}
	sessions := make([]string, 0, len(targets))
	for s := range targets {
		sessions = append(sessions, s)
	}
	sort.Strings(sessions)
	if len(sessions) == 0 {
		fmt.Fprintln(stdout, "no sessions are registered, so there is nothing to sweep")
	}
	for _, s := range sessions {
		t := targets[s]
		r, err := sweep(t, *dryRun)
		switch {
		case err != nil:
			fmt.Fprintf(stderr, "tack halt: session %s: the sweep failed: %v\n", s, err)
			code = exitError
		case r.Refused != "":
			fmt.Fprintf(stdout, "session %s: REFUSED, nothing killed: %s\n", s, r.Refused)
			list(stdout, r.Older)
			if len(r.Older) == 0 {
				list(stdout, r.Kill)
			}
			code = exitError
		case *dryRun:
			fmt.Fprintf(stdout, "session %s (since %s): would kill %d process(es)\n", s, t.Since.Format(time.RFC3339), len(r.Kill))
			list(stdout, r.Kill)
		default:
			fmt.Fprintf(stdout, "session %s: %d process(es) killed\n", s, r.Killed)
		}
	}
	return code
}

func list(w io.Writer, ps []sandbox.Proc) {
	for _, p := range ps {
		fmt.Fprintf(w, "  pid %d  %s  started %s\n", p.PID, p.Name, p.Start.Format(time.RFC3339))
	}
}

// resumeCmd is `tack resume`: it removes the HALT file.
func resumeCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("resume", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "config file (default ~/.config/tack/config.toml)")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return exitUsage
	}
	dir, err := haltStateDir(*cfgPath, stderr)
	if err != nil {
		fmt.Fprintln(stderr, "tack resume:", err)
		return exitError
	}
	was, err := halt.Resume(dir)
	if err != nil {
		fmt.Fprintln(stderr, "tack resume:", err)
		return exitError
	}
	if was {
		fmt.Fprintln(stdout, "tack resumed")
	} else {
		fmt.Fprintln(stdout, "tack was not halted")
	}
	return exitOK
}

// haltStateDir is the state directory the config names, or the default when the config cannot be loaded.
func haltStateDir(cfgPath string, stderr io.Writer) (string, error) {
	if cfgPath == "" {
		p, err := config.Path()
		if err != nil {
			return "", err
		}
		cfgPath = p
	}
	if cfg, err := config.Load(cfgPath); err == nil && cfg.StateDir != "" {
		return cfg.StateDir, nil
	} else if err != nil {
		fmt.Fprintf(stderr, "tack: warning: %v; using the default state directory\n", err)
	}
	return config.StateDir()
}
