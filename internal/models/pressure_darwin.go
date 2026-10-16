//go:build darwin

package models

import "syscall"

// MemoryPressure reads macOS's memory-pressure level, kern.memorystatus_vm_pressure_level (DESIGN §8.1): the OS's
// own signal, not swap counters or RSS, which other apps move and which inverts under pressure on Darwin.
func MemoryPressure() (Pressure, error) {
	v, err := syscall.SysctlUint32("kern.memorystatus_vm_pressure_level")
	if err != nil {
		return PressureUnknown, err
	}
	switch v {
	case 1:
		return PressureNormal, nil
	case 2:
		return PressureWarning, nil
	case 4:
		return PressureCritical, nil
	}
	return PressureUnknown, nil
}
