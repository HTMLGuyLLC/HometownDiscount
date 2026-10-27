package memory

import (
	"strconv"
	"strings"
)

// parseMeminfo reads Linux's /proc/meminfo: MemAvailable is what can be
// handed out without swapping. Kernels before 3.14 lack it: unknown.
func parseMeminfo(text string) Status {
	var total, avail int64 = -1, -1
	for _, line := range strings.Split(text, "\n") {
		name, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		f := strings.Fields(rest)
		if len(f) == 0 {
			continue
		}
		n, err := strconv.ParseInt(f[0], 10, 64)
		if err != nil {
			continue
		}
		switch name {
		case "MemTotal":
			total = n
		case "MemAvailable":
			avail = n
		}
	}
	if total <= 0 || avail < 0 {
		return Status{FreePct: -1}
	}
	return Status{FreePct: int(min(100, avail*100/total))}
}
