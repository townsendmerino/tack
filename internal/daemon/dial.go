package daemon

import (
	"errors"
	"fmt"
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
// spawn nil it returns ErrNotRunning.
func Dial(stateDir string, client proto.ClientInfo, spawn func() error, wait time.Duration) (*Conn, error) {
	path := SocketPath(stateDir)
	nc, err := net.Dial("unix", path)
	if err != nil {
		if spawn == nil {
			return nil, fmt.Errorf("%w (%s: %v)", ErrNotRunning, path, err)
		}
		if err := spawn(); err != nil {
			return nil, fmt.Errorf("daemon: starting it: %w", err)
		}
		deadline := time.Now().Add(wait)
		for {
			if nc, err = net.Dial("unix", path); err == nil {
				break
			}
			if time.Now().After(deadline) {
				return nil, fmt.Errorf("daemon: started it, but %s did not answer within %s: %w", path, wait, err)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	cl := core.NewClient(nc)
	var init proto.InitializeResult
	err = initialize(cl, stateDir, client, &init)
	var pe *proto.Error
	if errors.As(err, &pe) && pe.Code == proto.CodeUnauthorized {
		err = initialize(cl, stateDir, client, &init) // the daemon restarted between reading the token and presenting it
	}
	if err != nil {
		nc.Close()
		return nil, err
	}
	return &Conn{Client: cl, Init: init, Warning: versionWarning(proto.Version, init), nc: nc}, nil
}

func initialize(cl *core.Client, stateDir string, client proto.ClientInfo, init *proto.InitializeResult) error {
	tok, _ := ReadToken(stateDir) // a client that cannot read it connects unprivileged
	return cl.Call(proto.MethodInitialize, proto.InitializeParams{Client: client, Protocol: proto.Version, Token: tok}, init)
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
