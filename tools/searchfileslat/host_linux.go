//go:build linux

package main

import (
	"fmt"
	"os"
	"strings"
)

// hostState is the 1-minute load average and MemAvailable in GB, from /proc.
func hostState() (load, freeGB float64, err error) {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, 0, err
	}
	if _, err := fmt.Sscan(string(b), &load); err != nil {
		return 0, 0, fmt.Errorf("/proc/loadavg: %v", err)
	}
	m, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0, err
	}
	for line := range strings.SplitSeq(string(m), "\n") {
		if rest, ok := strings.CutPrefix(line, "MemAvailable:"); ok {
			var kb float64
			if _, err := fmt.Sscan(rest, &kb); err != nil {
				return 0, 0, fmt.Errorf("/proc/meminfo: %v", err)
			}
			return load, kb / (1 << 20), nil
		}
	}
	return 0, 0, fmt.Errorf("/proc/meminfo has no MemAvailable")
}
