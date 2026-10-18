package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/townsendmerino/tack/internal/eval"
	"github.com/townsendmerino/tack/tools/gate"
)

// exitHeld is goinfer's timing_lock.py exit status for "a timed run holds the box".
const exitHeld = 75

func evalCmd(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "mine" {
		return mineCmd(args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "pilot" {
		return pilotCmd(args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "pilot-serve" {
		return pilotServeCmd(args[1:], stdout, stderr)
	}
	if len(args) == 0 || args[0] != "models" {
		fmt.Fprintln(stderr, "usage: tack eval models -manifest FILE -out DIR [-arm NAME] [-fit-only]")
		return exitUsage
	}
	fs := flag.NewFlagSet("eval models", flag.ContinueOnError)
	fs.SetOutput(stderr)
	manifest := fs.String("manifest", "", "the box's E0 manifest (eval/e0/<box>.toml)")
	out := fs.String("out", "", "results directory (durable: not /tmp)")
	arm := fs.String("arm", "", "run only this arm")
	fitOnly := fs.Bool("fit-only", false, "load each arm and record goinfer's plan; nothing is timed (the day fit check)")
	rev := fs.String("tack-rev", "", "the tack commit this binary must have been built from (checked against its build info)")
	if err := fs.Parse(args[1:]); err != nil {
		return exitUsage
	}
	if *manifest == "" || *out == "" {
		fmt.Fprintln(stderr, "tack eval models: -manifest and -out are required")
		return exitUsage
	}
	tackRev, err := eval.Revision(*rev)
	if err != nil {
		fmt.Fprintln(stderr, "tack eval models:", err)
		fmt.Fprintln(stdout, gate.Verdict(gate.Inconclusive, "eval models: not started: "+err.Error()))
		return exitError
	}
	m, err := eval.LoadManifest(*manifest)
	if err != nil {
		fmt.Fprintln(stderr, "tack eval models:", err)
		return exitError
	}
	lock, err := eval.TimingLockPath()
	if err != nil {
		fmt.Fprintln(stderr, "tack eval models:", err)
		return exitError
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	results, err := eval.Run(ctx, eval.Options{Manifest: m, Out: *out, Arm: *arm, FitOnly: *fitOnly, TackRev: tackRev,
		LockPath: lock, Log: stderr})
	if err != nil {
		fmt.Fprintln(stderr, "tack eval models:", err)
		fmt.Fprintln(stdout, gate.Verdict(gate.Inconclusive, "eval models: "+err.Error()))
		if errors.Is(err, eval.ErrHeld) {
			return exitHeld
		}
		return exitError
	}
	return eval.Summarize(stdout, "eval models", results)
}
