//go:build unix

package sandbox

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
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

// userProcs counts the processes the user runs now, or 0 when ps can't say
// (then no process limit is set).
func userProcs() int {
	out, err := exec.Command("ps", "-U", strconv.Itoa(os.Getuid()), "-o", "pid=").Output()
	if err != nil {
		return 0
	}
	return strings.Count(string(out), "\n")
}
