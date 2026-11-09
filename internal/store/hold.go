package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/townsendmerino/tack/internal/proto"
)

// ErrSessionHeld is Hold finding the session held by another Hold, in this process or another.
var ErrSessionHeld = errors.New("store: the session is open in another tack process")

// Hold is a session core's claim on a session it has in memory: an exclusive flock on sessions/<id>/held, kept until
// Release. A Log gives up the writer's lock between turns so that tack rewind can write it; the hold is kept, so no
// other core reopens or recovers the session and appends a turn the holder's memory does not have. Only cores take
// holds: tack rewind does not.
type Hold struct {
	f *os.File
}

// Hold takes the session's hold; ErrSessionHeld while another has it, and an fs.ErrNotExist error for a session with
// no directory.
func (s *Store) Hold(id proto.SessionID) (*Hold, error) {
	p, err := s.logPath(id)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(filepath.Dir(p), "held"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("%w: %s", ErrSessionHeld, id)
		}
		return nil, fmt.Errorf("store: holding %s: %w", id, err)
	}
	return &Hold{f: f}, nil
}

// Release gives the hold up. Releasing a nil or released Hold does nothing.
func (h *Hold) Release() error {
	if h == nil || h.f == nil {
		return nil
	}
	err := h.f.Close()
	h.f = nil
	return err
}
