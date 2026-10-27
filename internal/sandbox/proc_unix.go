//go:build unix

package sandbox

import (
	"os/exec"
	"syscall"
)

// isolate starts the command in a new session: no controlling terminal
// (nothing can type into the user's shell) and its own process group, so a
// timeout kills everything the command started, background jobs included.
func isolate(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}

func killGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
