package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"

	"github.com/townsendmerino/tack/internal/config"
)

// NewToken writes a fresh random client token to client.token in the lock's directory, 0600, and returns it. The
// daemon makes one at every start, under its lock, so a token read before a restart is refused after it.
func (l *Lock) NewToken() (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return "", errors.New("daemon: the lock has been released")
	}
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(b[:])
	path := filepath.Join(l.dir, TokenName)
	tmp := path + ".tmp"
	_ = os.Remove(tmp) // WriteFile keeps an existing file's mode; a fresh file gets 0600
	if err := os.WriteFile(tmp, []byte(tok+"\n"), 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", err
	}
	return tok, nil
}

// ReadToken reads the client token in stateDir for a client to present at initialize. It refuses a token file other
// users could read.
func ReadToken(stateDir string) (string, error) {
	return config.ReadToken(filepath.Join(stateDir, TokenName))
}
