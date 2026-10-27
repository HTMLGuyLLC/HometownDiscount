package memory

import "syscall"

// macOS keeps both numbers the memory_pressure tool shows: the free share,
// and a pressure level (1 normal, 2 warning, 4 critical).
func read() Status {
	s := Status{FreePct: -1}
	if v, err := syscall.SysctlUint32("kern.memorystatus_level"); err == nil && v <= 100 {
		s.FreePct = int(v)
	}
	if v, err := syscall.SysctlUint32("kern.memorystatus_vm_pressure_level"); err == nil {
		s.Pressure = v >= 2
	}
	return s
}
