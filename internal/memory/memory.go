// Package memory tells long runs when the machine is short of memory, so
// they pause instead of pushing it into heavy swapping or a freeze. A local
// model server that grows with use is the usual cause.
package memory

import "fmt"

// Status is the machine's memory right now.
type Status struct {
	FreePct  int  // memory free or quickly freed, 0-100; -1 when unknown
	Pressure bool // the system itself reports memory pressure
}

// MinFreePct is the free share below which a run pauses.
const MinFreePct = 10

// Low reports whether a run should pause.
func (s Status) Low() bool {
	return s.Pressure || (s.FreePct >= 0 && s.FreePct < MinFreePct)
}

func (s Status) String() string {
	switch {
	case s.FreePct < 0 && s.Pressure:
		return "the system reports memory pressure"
	case s.FreePct < 0:
		return "memory use unknown"
	case s.Pressure:
		return fmt.Sprintf("the system reports memory pressure, %d%% free", s.FreePct)
	}
	return fmt.Sprintf("%d%% free", s.FreePct)
}

// Read returns the machine's memory status. Systems it can't read report
// unknown, which never pauses a run.
func Read() Status { return read() }
