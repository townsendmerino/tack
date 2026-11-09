//go:build !darwin && !linux

package daemon

import (
	"errors"
	"net"
)

// peerUID fails closed where tack cannot read a socket peer's uid: every connection is refused.
func peerUID(*net.UnixConn) (int, error) {
	return 0, errors.New("reading a socket peer's uid is not supported on this platform")
}
