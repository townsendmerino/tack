package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Blobs is the content-addressed blob store: file pre-images, full tool outputs, images (DESIGN §5.3). A blob's handle
// is "sha256:" and the hex digest of its bytes; identical content is stored once.
type Blobs struct {
	dir string
}

// ErrCorrupt is returned by Get when a blob's bytes no longer match its handle.
var ErrCorrupt = errors.New("store: blob does not match its hash")

const handlePrefix = "sha256:"

func (b *Blobs) path(hexsum string) string {
	return filepath.Join(b.dir, "sha256", hexsum[:2], hexsum[2:])
}

// Put stores data and returns its handle. The blob is written to a temporary file, fsynced and renamed into place, so
// a handle that Put returned always names complete bytes.
func (b *Blobs) Put(data []byte) (string, error) {
	sum := sha256.Sum256(data)
	hexsum := hex.EncodeToString(sum[:])
	p := b.path(hexsum)
	if _, err := os.Stat(p); err == nil {
		return handlePrefix + hexsum, nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".tmp-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name()) // no-op after the rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp.Name(), p); err != nil {
		return "", err
	}
	return handlePrefix + hexsum, nil
}

// Get returns a blob's bytes, checking them against the handle.
func (b *Blobs) Get(handle string) ([]byte, error) {
	hexsum, err := parseHandle(handle)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(b.path(hexsum))
	if err != nil {
		return nil, err
	}
	if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != hexsum {
		return nil, fmt.Errorf("%w: %s", ErrCorrupt, handle)
	}
	return data, nil
}

func parseHandle(h string) (string, error) {
	hexsum, ok := strings.CutPrefix(h, handlePrefix)
	if !ok || len(hexsum) != 64 {
		return "", fmt.Errorf("store: bad blob handle %q", h)
	}
	if _, err := hex.DecodeString(hexsum); err != nil {
		return "", fmt.Errorf("store: bad blob handle %q", h)
	}
	return hexsum, nil
}
