package sandbox

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// killDetached stops processes a command started that left its process
// group through posix_spawn's setsid or setpgroup flags, which the sandbox
// profile can't block (it blocks the setsid and setpgid calls). They are
// the processes started since the command began whose working folder is
// still the workspace or the scratch folder: nothing outside the sandbox
// knows those private paths. One that also changed folder is still inside
// the sandbox, with no network and nothing to write once the job's folders
// are deleted. Linux needs none of this: the sandbox's own process
// namespace ends with it.
func killDetached(dir, tmp string, since time.Time) {
	out, err := exec.Command("ps", "-axo", "pid=,etime=").Output()
	if err != nil {
		return
	}
	window := time.Since(since) + 2*time.Second
	self := strconv.Itoa(os.Getpid())
	var pids []string
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 || f[0] == self {
			continue
		}
		if age, ok := parseEtime(f[1]); ok && age <= window {
			pids = append(pids, f[0])
		}
	}
	if len(pids) == 0 {
		return
	}
	// lsof exits 1 when some of the processes are gone already; the rest
	// are still listed.
	out, _ = exec.Command("lsof", "-a", "-d", "cwd", "-p", strings.Join(pids, ","), "-Fpn").Output()
	pid := 0
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "p"):
			pid, _ = strconv.Atoi(line[1:])
		case strings.HasPrefix(line, "n") && pid > 0 && (within(line[1:], dir) || within(line[1:], tmp)):
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
}
