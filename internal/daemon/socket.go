package daemon

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
)

// MaxSocketPath is the longest Unix socket path tack binds: macOS's sun_path holds 104 bytes with the terminating NUL,
// and Linux's 108, so 103 bytes fit both. A longer path makes bind fail with a bare EINVAL (DESIGN §9.1).
const MaxSocketPath = 103

// CheckSocketPath refuses a socket path too long to bind, saying how long it is.
func CheckSocketPath(path string) error {
	if n := len(path); n > MaxSocketPath {
		return fmt.Errorf("daemon: the socket path %s is %d bytes; a Unix socket path can be at most %d. Set a shorter state_dir in config.toml", path, n, MaxSocketPath)
	}
	return nil
}

// SocketPath is the daemon's socket in stateDir.
func SocketPath(stateDir string) string { return filepath.Join(stateDir, SocketName) }

// Listen binds the daemon's socket in the lock's directory. Whatever is at the path is removed first: a socket left
// by a daemon that crashed answers nobody, and since this process holds the lock, no other daemon is serving on it.
// The socket is 0600 in a 0700 directory, and the listener closes any connection whose peer is not this user.
// Release closes it.
func (l *Lock) Listen() (*Listener, error) {
	path := SocketPath(l.dir)
	if err := CheckSocketPath(path); err != nil {
		return nil, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil, errors.New("daemon: the lock has been released")
	}
	if l.ln != nil {
		return nil, errors.New("daemon: the socket is already bound")
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("daemon: removing the stale socket: %w", err)
	}
	ul, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("daemon: binding %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		ul.Close()
		return nil, err
	}
	l.ln = &Listener{ul: ul, uid: os.Getuid(), peer: peerUID, closing: l.markStopping}
	return l.ln, nil
}

// Listener accepts connections on the daemon's socket from this user only (DESIGN §9.1: the file mode, and the
// peer's uid besides).
type Listener struct {
	ul   *net.UnixListener
	uid  int
	peer func(*net.UnixConn) (int, error)
	// closing is called before the socket is closed and unlinked: the lock's record then says the daemon is stopping.
	closing func()
	// Refused, when set, is told of each connection closed because its peer is another user or could not be read. Set
	// it before the first Accept.
	Refused func(error)
}

// Accept returns the next connection whose peer is this user, closing the others.
func (l *Listener) Accept() (net.Conn, error) {
	for {
		c, err := l.ul.AcceptUnix()
		if err != nil {
			return nil, err
		}
		uid, err := l.peer(c)
		if err == nil && uid == l.uid {
			return c, nil
		}
		if err == nil {
			err = fmt.Errorf("daemon: refused a connection from uid %d; this daemon serves uid %d", uid, l.uid)
		} else {
			err = fmt.Errorf("daemon: refused a connection whose peer could not be read: %w", err)
		}
		c.Close()
		if l.Refused != nil {
			l.Refused(err)
		}
	}
}

// Close marks the daemon stopping, then stops listening and unlinks the socket.
func (l *Listener) Close() error {
	if l.closing != nil {
		l.closing()
	}
	return l.ul.Close()
}

// Addr is the socket's address.
func (l *Listener) Addr() net.Addr { return l.ul.Addr() }
