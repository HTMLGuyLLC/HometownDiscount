package memory

import "os"

func read() Status {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return Status{FreePct: -1}
	}
	return parseMeminfo(string(b))
}
