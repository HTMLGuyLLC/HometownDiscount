//go:build !unix

package sandbox

import "os/exec"

// No sandbox exists on these systems (Available reports it), so commands
// never get this far.
func isolate(*exec.Cmd)   {}
func killGroup(*exec.Cmd) {}
