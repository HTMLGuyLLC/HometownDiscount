//go:build !darwin

package sandbox

import "time"

// killDetached has nothing to do here: on Linux every process a command
// starts lives in the sandbox's own process namespace, which ends with it.
func killDetached(dir, tmp string, since time.Time) {}
