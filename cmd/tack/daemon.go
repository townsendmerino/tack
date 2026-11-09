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

// Exit codes for tack daemon status: 0 a daemon answered; 3 none is running; 4 one holds the lock but does not serve,
// as while it starts or shuts down (it takes its socket away first).
const (
	exitNotRunning = 3
	exitNotServing = 4
)

// drainWait is how long a client waits on a daemon that shuts down with grace (daemon.DrainWait). tack daemon stop
// waits this long for the lock; a client that starts a daemon waits this long for the socket, since a daemon started
// while another drains binds it only once that one lets go.
func drainWait(grace time.Duration) time.Duration { return daemon.DrainWait(grace) }

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
		c, err := daemon.Dial(stateDir, cliClient, spawner(path, stateDir), drainWait(cfg.Daemon.ShutdownGrace.Duration))
		if err != nil {
			fmt.Fprintln(stderr, "tack daemon start:", err)
			return exitError
		}
		defer c.Close()
		return printStatus(c, stdout, stderr)
	case "status":
		c, err := daemon.Dial(stateDir, cliClient, nil, 0)
		if errors.Is(err, daemon.ErrNotRunning) {
			if h := daemon.ReadHolder(stateDir); h.PID != 0 { // no socket while it starts or drains
				fmt.Fprintln(stdout, holderLine(h))
				return exitNotServing
			}
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
	stderr = daemon.SyncWriter(stderr) // Run and the core's callbacks write it from their own goroutines
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		stop() // the next signal takes its default action: the process ends
	}()
	err := daemon.Run(ctx, daemon.RunOptions{StateDir: stateDir, Version: version, Grace: cfg.Daemon.ShutdownGrace.Duration, Log: stderr,
		NewCore: func(coreCtx context.Context, token string, shutdown func()) (*core.Core, error) {
			st, err := store.Open(stateDir)
			if err != nil {
				return nil, err
			}
			opts := coreOptions(cfg, cfg.Main, stateDir, st, nil, stderr)
			opts.ClientToken, opts.Shutdown = token, shutdown
			opts.Edit = editKit(stateDir, filepath.Dir(cfgPath), cfg.Sandbox.Read)
			return core.New(coreCtx, opts), nil
		},
		Watch: func(coreCtx context.Context, c *core.Core) {
			halt.Watch(coreCtx, stateDir, halt.Interval, c.Halt, c.Resume)
		},
	})
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

// daemonStop asks the daemon to drain and exit, and waits until it has released its lock: within the grace window,
// plus time to record the turns it cancels.
func daemonStop(stateDir string, grace time.Duration, stdout, stderr io.Writer) int {
	c, err := daemon.Dial(stateDir, cliClient, nil, 0)
	if errors.Is(err, daemon.ErrNotRunning) {
		switch h := daemon.ReadHolder(stateDir); {
		case h.PID == 0:
			fmt.Fprintln(stdout, "tack daemon: not running")
			return exitOK
		case h.Stopping: // shutting down already: its socket is gone, and the lock follows
			return awaitRelease(stateDir, h.PID, grace, stdout, stderr)
		default:
			fmt.Fprintf(stderr, "tack daemon stop: pid %d holds the lock but does not serve yet; run stop again once it does\n", h.PID)
			return exitError
		}
	}
	if err != nil {
		fmt.Fprintln(stderr, "tack daemon stop:", err)
		return exitError
	}
	var st proto.DaemonStatusResult
	err = c.Call(proto.MethodDaemonStatus, proto.DaemonStatusParams{}, &st)
	if err == nil {
		err = c.Call(proto.MethodDaemonShutdown, proto.DaemonShutdownParams{}, nil)
	}
	c.Close()
	if errors.Is(err, core.ErrClosed) { // it finished shutting down as this stop reached it: wait for its lock
		pid := st.PID
		if pid == 0 {
			pid = daemon.ReadHolder(stateDir).PID
		}
		return awaitRelease(stateDir, pid, grace, stdout, stderr)
	}
	if err != nil {
		fmt.Fprintln(stderr, "tack daemon stop:", err)
		return exitError
	}
	return awaitRelease(stateDir, st.PID, grace, stdout, stderr)
}

// awaitRelease waits for the daemon with pid, shutting down with grace, to let go of the lock, and says so.
func awaitRelease(stateDir string, pid int, grace time.Duration, stdout, stderr io.Writer) int {
	wait := drainWait(grace)
	if !released(stateDir, pid, wait) {
		fmt.Fprintf(stderr, "tack daemon stop: pid %d still holds the lock after %s\n", pid, wait)
		return exitError
	}
	fmt.Fprintln(stdout, "tack daemon: stopped")
	return exitOK
}

// holderLine describes a daemon from its holder record, for one that holds the lock without a socket: one starting,
// or one shutting down, which takes its socket away first.
func holderLine(h daemon.Holder) string {
	state := "starting, not serving yet"
	if h.Stopping {
		state = "shutting down"
	}
	return fmt.Sprintf("tack daemon %s, pid %d, since %s: %s", h.Version, h.PID, h.Start.Local().Format(time.DateTime), state)
}

// released waits up to wait for the daemon with pid to let go of stateDir's lock: the lock free, or held by another
// daemon that has recorded itself. Neither the socket nor the record says it alone: a shutdown takes the socket away
// first, and Release removes the record just before it unlocks.
func released(stateDir string, pid int, wait time.Duration) bool {
	deadline := time.Now().Add(wait)
	for {
		if daemon.LockFree(stateDir) {
			return true
		}
		if h := daemon.ReadHolder(stateDir); h.PID != 0 && h.PID != pid { // the next daemon has it already
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
}
