//go:build darwin

package main

import (
	"encoding/binary"
	"fmt"

	"golang.org/x/sys/unix"
)

// hostState is the 1-minute load average and the free memory in GB, from sysctl in this process (no program is run).
// Free memory is vm.page_free_count's pages: recorded, never a guard, since Darwin keeps reclaimable memory out of it.
func hostState() (load, freeGB float64, err error) {
	raw, err := unix.SysctlRaw("vm.loadavg") // struct loadavg { fixpt_t ldavg[3]; long fscale; }
	if err != nil {
		return 0, 0, err
	}
	if len(raw) < 24 {
		return 0, 0, fmt.Errorf("vm.loadavg is %d bytes, want 24", len(raw))
	}
	ld := binary.LittleEndian.Uint32(raw[0:4])
	scale := binary.LittleEndian.Uint64(raw[16:24])
	if scale == 0 {
		return 0, 0, fmt.Errorf("vm.loadavg has a zero fscale")
	}
	pages, err := unix.SysctlUint32("vm.page_free_count")
	if err != nil {
		return 0, 0, err
	}
	return float64(ld) / float64(scale), float64(pages) * float64(unix.Getpagesize()) / (1 << 30), nil
}
