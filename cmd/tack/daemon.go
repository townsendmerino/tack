package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/townsendmerino/tack/internal/config"
	"github.com/townsendmerino/tack/internal/core"
	"github.com/townsendmerino/tack/internal/daemon"
	"github.com/townsendmerino/tack/internal/halt"
	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/store"
)

// Exit codes for tack daemon status: 0 a daemon answered, 3 none is running.
const exitNotRunning = 3

// spawnWait is how long a client that started a daemon waits for its socket.
const spawnWait = 10 * time.Second

var cliClient = proto.ClientInfo{Name: "tack-cli", Version: version}

// daemonCmd is `tack daemon run|start|stop|status` (DESIGN §6.1, §10).
func daemonCmd(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: tack daemon run|start|stop|status [-config FILE]")
		return exitUsage
	}
	sub := args[0]
	fs := flag.NewFlagSet("daemon "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "config file (default ~/.config/tack/config.toml)")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 {
		return exitUsage
	}
	cfg, path, stateDir, err := loadConfig(*cfgPath)
	if err != nil {
		fmt.Fprintln(stderr, "tack daemon:", err)
		return exitError
	}
	switch sub {
	case "run":
		return daemonRun(cfg, path, stateDir, stderr)
	case "start":
		c, err := daemon.Dial(stateDir, cliClient, spawner(path, stateDir), spawnWait)
		if err != nil {
			fmt.Fprintln(stderr, "tack daemon start:", err)
			return exitError
		}
		defer c.Close()
		return printStatus(c, stdout, stderr)
	case "status":
		c, err := daemon.Dial(stateDir, cliClient, nil, 0)
		if errors.Is(err, daemon.ErrNotRunning) {
			fmt.Fprintln(stdout, "tack daemon: not running")
			return exitNotRunning
		}
		if err != nil {
			fmt.Fprintln(stderr, "tack daemon status:", err)
			return exitError
		}
		defer c.Close()
		return printStatus(c, stdout, stderr)
	case "stop":
		return daemonStop(stateDir, cfg.Daemon.ShutdownGrace.Duration, stdout, stderr)
	}
	fmt.Fprintf(stderr, "tack daemon: unknown command %q; want run, start, stop or status\n", sub)
	return exitUsage
}

// loadConfig reads the config at path (the default when empty) and returns it with its path and tack's state
// directory.
func loadConfig(path string) (*config.Config, string, string, error) {
	if path == "" {
		p, err := config.Path()
		if err != nil {
			return nil, "", "", err
		}
		path = p
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs // a spawned daemon is given it, and runs from another directory
	}
	cfg, err := config.Load(path)
	if err != nil {
		return nil, "", "", err
	}
	stateDir := cfg.StateDir
	if stateDir == "" {
		if stateDir, err = config.StateDir(); err != nil {
			return nil, "", "", err
		}
	}
	return cfg, path, stateDir, nil
}

// daemonRun is the daemon in the foreground: what tack daemon start spawns. The first SIGTERM or interrupt drains and
// exits; a second one kills at once, as the default action of the signal (DESIGN §6.1). Another daemon holding the
// lock is not an error: it is named, and this one exits 0, which launchd does not restart.
func daemonRun(cfg *config.Config, cfgPath, stateDir string, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		stop() // the next signal takes its default action: the process ends
	}()
	coreCtx, cancel := context.WithCancel(context.Background()) // turns run under it through the drain
	defer cancel()
	err := daemon.Run(ctx, daemon.RunOptions{StateDir: stateDir, Version: version, Grace: cfg.Daemon.ShutdownGrace.Duration, Log: stderr,
		NewCore: func(token string, shutdown func()) (*core.Core, error) {
			st, err := store.Open(stateDir)
			if err != nil {
				return nil, err
			}
			opts := coreOptions(cfg, cfg.Main, stateDir, st, nil, stderr)
			opts.ClientToken, opts.Shutdown = token, shutdown
			opts.Edit = editKit(stateDir, filepath.Dir(cfgPath), cfg.Sandbox.Read)
			c := core.New(coreCtx, opts)
			go halt.Watch(coreCtx, stateDir, halt.Interval, c.Halt, c.Resume)
			return c, nil
		}})
	var he *daemon.HeldError
	if errors.As(err, &he) {
		fmt.Fprintf(stderr, "tack daemon: %v; not starting another\n", err)
		return exitOK
	}
	if err != nil {
		fmt.Fprintln(stderr, "tack daemon:", err)
		return exitError
	}
	return exitOK
}

// spawner starts `tack daemon run` detached, logging to logs/daemon.log in the state directory.
func spawner(cfgPath, stateDir string) func() error {
	return func() error {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		_, err = daemon.SpawnDetached(exe, []string{"daemon", "run", "-config", cfgPath}, filepath.Join(stateDir, "logs", "daemon.log"))
		return err
	}
}

// printStatus prints daemon/status on one line, and the version warning if there is one.
func printStatus(c *daemon.Conn, stdout, stderr io.Writer) int {
	var st proto.DaemonStatusResult
	if err := c.Call(proto.MethodDaemonStatus, proto.DaemonStatusParams{}, &st); err != nil {
		fmt.Fprintln(stderr, "tack daemon:", err)
		return exitError
	}
	if c.Warning != "" {
		fmt.Fprintln(stderr, "tack:", c.Warning)
	}
	line := fmt.Sprintf("tack daemon %s (protocol %d), pid %d, since %s: %d sessions, %d running", st.Version, st.Protocol, st.PID,
		st.Started.Local().Format(time.DateTime), st.Sessions, st.Running)
	if st.Halted {
		line += ", halted"
	}
	if st.Draining {
		line += ", shutting down"
	}
	fmt.Fprintln(stdout, line)
	return exitOK
}

// daemonStop asks the daemon to drain and exit, and waits until its socket stops answering: within the grace window,
// plus time to record the turns it cancels.
func daemonStop(stateDir string, grace time.Duration, stdout, stderr io.Writer) int {
	c, err := daemon.Dial(stateDir, cliClient, nil, 0)
	if errors.Is(err, daemon.ErrNotRunning) {
		fmt.Fprintln(stdout, "tack daemon: not running")
		return exitOK
	}
	if err != nil {
		fmt.Fprintln(stderr, "tack daemon stop:", err)
		return exitError
	}
	err = c.Call(proto.MethodDaemonShutdown, proto.DaemonShutdownParams{}, nil)
	c.Close()
	if err != nil {
		fmt.Fprintln(stderr, "tack daemon stop:", err)
		return exitError
	}
	wait := grace + 15*time.Second
	deadline := time.Now().Add(wait)
	for {
		next, err := daemon.Dial(stateDir, cliClient, nil, 0)
		if errors.Is(err, daemon.ErrNotRunning) {
			fmt.Fprintln(stdout, "tack daemon: stopped")
			return exitOK
		}
		if err == nil {
			next.Close()
		}
		if time.Now().After(deadline) {
			fmt.Fprintf(stderr, "tack daemon stop: it is still answering after %s\n", wait)
			return exitError
		}
		time.Sleep(100 * time.Millisecond)
	}
}
