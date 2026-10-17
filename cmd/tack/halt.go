package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/townsendmerino/tack/internal/config"
	"github.com/townsendmerino/tack/internal/halt"
)

// sweep is sweepAll; tests replace it, since a real sweep would kill other packages' sandboxed tests.
var sweep = sweepAll

// haltCmd is `tack halt` (DESIGN §6.6): it creates the HALT file, which every running tack watches, then kills every
// process still inside any of tack's sandboxes, wherever it detached to. It must work when nothing else does, so a
// config it cannot read falls back to the default state directory, with a warning.
func haltCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("halt", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "config file (default ~/.config/tack/config.toml)")
	reason := fs.String("reason", "", "why, recorded in each halted session's log")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return exitUsage
	}
	dir, err := haltStateDir(*cfgPath, stderr)
	if err != nil {
		fmt.Fprintln(stderr, "tack halt:", err)
		return exitError
	}
	was, err := halt.Halt(dir, "tack halt", *reason)
	if err != nil {
		fmt.Fprintln(stderr, "tack halt:", err)
		return exitError
	}
	code := exitOK
	n, err := sweep()
	if err != nil {
		fmt.Fprintln(stderr, "tack halt: killing what tack's commands left running:", err)
		code = exitError
	}
	state := "halted"
	if was {
		state = "already halted"
	}
	fmt.Fprintf(stdout, "tack is %s (%s); %d sandboxed process(es) killed. tack resume lifts it.\n", state, halt.Path(dir), n)
	return code
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
