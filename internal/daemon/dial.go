package daemon

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/townsendmerino/tack/internal/core"
	"github.com/townsendmerino/tack/internal/proto"
)

// ErrNotRunning is Dial finding no daemon, with nothing to start one.
var ErrNotRunning = errors.New("no tack daemon is running")

// Conn is a client's initialized connection to the daemon.
type Conn struct {
	*core.Client
	Init proto.InitializeResult
	// Warning is set when the daemon speaks an older protocol than this client, which then works at the daemon's.
	Warning string
	nc      net.Conn
}

// Close closes the connection.
func (c *Conn) Close() error { return c.nc.Close() }

// Dial connects to the daemon in stateDir and initializes the connection, presenting the client token when this
// client can read it (privileged) and none when it cannot. When no daemon answers and spawn is not nil, Dial calls
// spawn once (the first tack that finds no daemon starts one, DESIGN §6.1) and waits up to wait for the socket. With
// spawn nil it returns ErrNotRunning. A socket this client cannot reach for another reason is that error, not
// ErrNotRunning.
//
// A daemon restarting between the dial and the token's read leaves this client connected to the one that is going,
// with its successor's token or none: the token is refused there, or missing. Dial then connects again, once, waiting
// up to wait for the successor's socket.
func Dial(stateDir string, client proto.ClientInfo, spawn func() error, wait time.Duration) (*Conn, error) {
	path := SocketPath(stateDir)
	nc, err := connect(path, spawn, wait)
	for attempt := 1; ; attempt++ {
		if err != nil {
			return nil, err
		}
		cl := core.NewClient(nc)
		var init proto.InitializeResult
		tok, terr := ReadToken(stateDir) // a client that cannot read it connects unprivileged
		err = cl.Call(proto.MethodInitialize, proto.InitializeParams{Client: client, Protocol: proto.Version, Token: tok}, &init)
		var pe *proto.Error
		restarted := errors.Is(terr, fs.ErrNotExist) || errors.As(err, &pe) && pe.Code == proto.CodeUnauthorized
		if restarted && attempt == 1 {
			nc.Close()
			nc, err = connect(path, nil, wait)
			continue
		}
		if err != nil {
			nc.Close()
			return nil, err
		}
		return &Conn{Client: cl, Init: init, Warning: versionWarning(proto.Version, init), nc: nc}, nil
	}
}

// connect dials the socket at path. When no daemon answers and spawn is not nil, it calls spawn once and waits up to
// wait for the socket; with spawn nil it waits up to wait for one that is starting, and then returns ErrNotRunning.
func connect(path string, spawn func() error, wait time.Duration) (net.Conn, error) {
	nc, err := net.Dial("unix", path)
	if err == nil {
		return nc, nil
	}
	if !notRunning(err) {
		return nil, fmt.Errorf("daemon: connecting to %s: %w", path, err)
	}
	if spawn != nil {
		if err := spawn(); err != nil {
			return nil, fmt.Errorf("daemon: starting it: %w", err)
		}
	}
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		if nc, err = net.Dial("unix", path); err == nil {
			return nc, nil
		}
		if !notRunning(err) {
			return nil, fmt.Errorf("daemon: connecting to %s: %w", path, err)
		}
	}
	if spawn != nil {
		return nil, fmt.Errorf("daemon: started it, but %s did not answer within %s: %w", path, wait, err)
	}
	return nil, fmt.Errorf("%w (%s: %v)", ErrNotRunning, path, err)
}

// notRunning says whether a dial's error means no daemon is serving: no socket, or one nobody listens on (a daemon
// that crashed left it).
func notRunning(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED)
}

// versionWarning says what a client speaking own should tell its user about the daemon init describes: nothing when
// they agree; that it works at the daemon's older version otherwise (DESIGN §6.1).
func versionWarning(own int, init proto.InitializeResult) string {
	if init.Protocol >= own {
		return ""
	}
	return fmt.Sprintf("tack daemon %s speaks protocol %d and this tack speaks %d; working at %d. Run tack daemon stop to restart it at this version",
		init.Daemon.Version, init.Protocol, own, init.Protocol)
}

// SpawnDetached starts exe with args as a daemon: in a session of its own (setsid), so it outlives the client, its
// terminal and its process group; with stdin from /dev/null, and stdout and stderr appended to logPath (0600, its
// directory 0700). It returns the pid without waiting for the daemon to serve, and reaps it if it exits while this
// process runs.
func SpawnDetached(exe string, args []string, logPath string) (int, error) {
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return 0, err
	}
	lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return 0, err
	}
	defer lf.Close()
	cmd := exec.Command(exe, args...)
	cmd.Stdout, cmd.Stderr = lf, lf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("daemon: starting %s: %w", exe, err)
	}
	go cmd.Wait()
	return cmd.Process.Pid, nil
}
