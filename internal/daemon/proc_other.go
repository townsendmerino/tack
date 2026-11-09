//go:build !darwin && !linux

package daemon

import "errors"

// procInfo fails where tack cannot read a process's start time: no holder record is then trusted.
func procInfo(int) (string, bool, error) {
	return "", false, errors.New("reading a process's start time is not supported on this platform")
}
