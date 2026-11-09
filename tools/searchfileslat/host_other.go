//go:build !darwin && !linux

package main

import "errors"

// hostState is not read here: every cell is void.
func hostState() (load, freeGB float64, err error) {
	return 0, 0, errors.New("the host's load is read only on darwin and linux")
}
