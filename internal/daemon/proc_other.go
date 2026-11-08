//go:build !darwin && !linux

package daemon

import "errors"

// procStart fails where tack cannot read a process's start time: no holder record is then trusted.
func procStart(int) (string, error) {
	return "", errors.New("reading a process's start time is not supported on this platform")
}
