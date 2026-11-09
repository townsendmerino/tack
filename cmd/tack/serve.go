package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/townsendmerino/tack/internal/config"
	"github.com/townsendmerino/tack/internal/daemon"
	"github.com/townsendmerino/tack/internal/eval"
	"github.com/townsendmerino/tack/internal/models"
	"github.com/townsendmerino/tack/internal/serve"
)

// exitBusy is tack serve -detach's exit when a timed run holds goinfer's timing lock: 75 (EX_TEMPFAIL), as goinfer's
// timing_lock.py check uses.
const exitBusy = 75

// serveCmd is `tack serve` (DESIGN §8.2): the on-demand model server on the box with the GPU, as [serve] configures it.
// Under systemd's socket activation it takes the socket systemd passes; started by hand or over ssh it listens on
// [serve] listen. With -detach it checks goinfer's timing lock (exit 75 when a timed run holds it), starts itself in a
// session of its own, and returns: the daemon on the Mac runs that over ssh when socket activation is not set up.
func serveCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "config file (default ~/.config/tack/config.toml)")
	detach := fs.Bool("detach", false, "start in the background, after checking goinfer's timing lock, and return")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return exitUsage
	}
	cfg, path, stateDir, err := loadConfig(*cfgPath)
	if err != nil {
		fmt.Fprintln(stderr, "tack serve:", err)
		return exitError
	}
	sc := cfg.Serve
	if sc == nil {
		fmt.Fprintf(stderr, "tack serve: %s has no [serve] section\n", path)
		return exitUsage
	}
	checkLock := func() error { return eval.CheckTimingLock(sc.TimingLock) }
	if *detach {
		if err := checkLock(); err != nil {
			fmt.Fprintln(stderr, "tack serve: busy:", err)
			return exitBusy
		}
		exe, err := os.Executable()
		if err != nil {
			fmt.Fprintln(stderr, "tack serve:", err)
			return exitError
		}
		pid, err := daemon.SpawnDetached(exe, []string{"serve", "-config", path}, filepath.Join(stateDir, "logs", "serve.log"))
		if err != nil {
			fmt.Fprintln(stderr, "tack serve:", err)
			return exitError
		}
		fmt.Fprintf(stdout, "tack serve: started, pid %d, listening on %s\n", pid, sc.Listen)
		return exitOK
	}
	key, err := config.ReadToken(sc.KeyFile)
	if err != nil {
		fmt.Fprintln(stderr, "tack serve:", err)
		return exitError
	}
	s := cfg.Servers[sc.Server]
	idle := sc.IdleUnload.Duration
	f, err := serve.New(serve.Options{Key: key, CheckLock: checkLock, Log: stderr, Child: models.ChildConfig{Binary: s.Binary,
		ModelPath: s.ModelPath, Model: s.Model, Ctx: s.Ctx, Args: s.Args, Dir: filepath.Join(stateDir, "serve", sc.Server),
		IdleUnload: idle, IdleExit: idle, Admission: true, AllowDevel: s.AllowDevel}})
	if err != nil {
		fmt.Fprintln(stderr, "tack serve:", err)
		return exitError
	}
	ln, err := serveListener(os.Getenv, os.Getpid(), 3, sc.Listen)
	if err != nil {
		fmt.Fprintln(stderr, "tack serve:", err)
		return exitError
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fmt.Fprintf(stderr, "tack serve %s: %s on %s; unloads after %s idle\n", version, s.Model, ln.Addr(), idle)
	if err := serve.Serve(ctx, ln, f, 30*time.Second); err != nil {
		fmt.Fprintln(stderr, "tack serve:", err)
		return exitError
	}
	return exitOK
}

// serveListener is the socket systemd passed at fd when it activated this process (sd_listen_fds(3): LISTEN_PID is
// this process and LISTEN_FDS is 1), or else a listener on listen. Reading those two is systemd's protocol, not a tack
// setting, as GOINFER_TIMING_LOCK is goinfer's (internal/eval/timinglock.go).
func serveListener(getenv func(string) string, pid int, fd uintptr, listen string) (net.Listener, error) {
	if getenv("LISTEN_PID") == strconv.Itoa(pid) {
		if n := getenv("LISTEN_FDS"); n != "1" {
			return nil, fmt.Errorf("systemd passed %s sockets; tack serve takes one", n)
		}
		f := os.NewFile(fd, "tack-serve.socket")
		ln, err := net.FileListener(f)
		f.Close() // FileListener dups it
		return ln, err
	}
	if listen == "" {
		return nil, errors.New("no socket from systemd, and [serve] listen is empty")
	}
	return net.Listen("tcp", listen)
}
