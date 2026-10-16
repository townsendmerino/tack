//go:build !darwin

package models

// MemoryPressure is unknown off macOS: the memory guard is the Mac's (DESIGN §8.1). On nobara, goinfer's own guards
// decide (DESIGN §8.2).
func MemoryPressure() (Pressure, error) { return PressureUnknown, nil }
