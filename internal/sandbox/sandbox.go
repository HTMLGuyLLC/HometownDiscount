// Package sandbox runs shell commands from a replayed job. The repo copy is
// read-only; the only writable place is a scratch folder. There's no network,
// no reading of the user's home folder or other temp folders, no terminal, no
// looking at or signalling other processes, and no system services (so no
// clipboard or app launching). macOS uses the built-in Seatbelt; Linux uses
// bubblewrap. Other systems can't run replays.
package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ErrUnavailable means this machine has no supported sandbox.
var ErrUnavailable = errors.New("no sandbox available (macOS Seatbelt or Linux bubblewrap required)")

// Result is a finished command.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
	TimedOut bool
}

// MaxOutput caps each of stdout and stderr, like a terminal tool would.
const MaxOutput = 30_000

// maxFileKB caps any single file a command writes in the scratch folder.
const maxFileKB = 100 * 1024

// Available reports whether commands can be sandboxed here.
func Available() error {
	switch runtime.GOOS {
	case "darwin":
		if _, err := exec.LookPath("sandbox-exec"); err != nil {
			return ErrUnavailable
		}
		return nil
	case "linux":
		if _, err := exec.LookPath("bwrap"); err != nil {
			return fmt.Errorf("%w: install bubblewrap (e.g. `sudo apt install bubblewrap`)", ErrUnavailable)
		}
		return nil
	}
	return ErrUnavailable
}

// Preflight runs a harmless command in a throwaway sandbox, so a sandbox that
// can't start (e.g. bubblewrap blocked by AppArmor, or htd itself running
// inside another sandbox) is reported up front instead of failing every job.
func Preflight(ctx context.Context) error {
	if err := Available(); err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "htd-preflight-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	ws, tmp := filepath.Join(dir, "ws"), filepath.Join(dir, "tmp")
	os.MkdirAll(ws, 0o755)
	os.WriteFile(filepath.Join(ws, "ok.txt"), []byte("ok"), 0o644)
	r, err := Run(ctx, ws, tmp, "cat ok.txt", 30*time.Second)
	if err != nil || r.ExitCode != 0 || r.Stdout != "ok" {
		return fmt.Errorf("the sandbox can't run commands here (%v %s); replays need it", err, strings.TrimSpace(r.Stderr))
	}
	return nil
}

// Run executes command with /bin/sh in dir (read-only) inside the sandbox.
// tmp is the only writable folder; it's created if needed.
func Run(ctx context.Context, dir, tmp, command string, timeout time.Duration) (Result, error) {
	if err := Available(); err != nil {
		return Result{}, err
	}
	var err error
	if dir, err = canonical(dir); err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		return Result{}, err
	}
	if tmp, err = canonical(tmp); err != nil {
		return Result{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Limit file size and CPU time inside the shell, then run the command.
	secs := int(timeout.Seconds()) + 1
	script := fmt.Sprintf("ulimit -f %d 2>/dev/null; ulimit -t %d 2>/dev/null; %s", maxFileKB*2, secs, command)
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.CommandContext(ctx, "sandbox-exec", "-p", seatbeltProfile(dir, tmp), "/bin/sh", "-c", script)
	case "linux":
		cmd = exec.CommandContext(ctx, "bwrap", bwrapArgs(dir, tmp, script)...)
	}
	cmd.Dir = dir
	cmd.Env = []string{
		"PATH=/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin",
		"HOME=" + tmp,
		"TMPDIR=" + tmp,
		"LANG=C.UTF-8",
	}
	isolate(cmd)
	cmd.WaitDelay = 2 * time.Second // don't wait on pipes held by leftovers
	stdout, stderr := &capped{}, &capped{}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	runErr := cmd.Run()
	killGroup(cmd) // leftovers from `cmd &`

	res := Result{Stdout: stdout.String(), Stderr: stderr.String()}
	if ctx.Err() == context.DeadlineExceeded {
		res.TimedOut = true
		res.ExitCode = -1
		return res, nil
	}
	var exit *exec.ExitError
	switch {
	case runErr == nil:
	case errors.As(runErr, &exit):
		res.ExitCode = exit.ExitCode()
	case errors.Is(runErr, exec.ErrWaitDelay):
	default:
		return res, runErr
	}
	return res, nil
}

func canonical(p string) (string, error) {
	p, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return p, nil
}

// capped keeps the first MaxOutput bytes and counts the rest.
type capped struct {
	b       []byte
	dropped int
}

func (c *capped) Write(p []byte) (int, error) {
	room := MaxOutput - len(c.b)
	if room > len(p) {
		room = len(p)
	}
	if room > 0 {
		c.b = append(c.b, p[:room]...)
	}
	c.dropped += len(p) - room
	return len(p), nil
}

func (c *capped) String() string {
	if c.dropped == 0 {
		return string(c.b)
	}
	return string(c.b) + fmt.Sprintf("\n[output truncated: %d more bytes]", c.dropped)
}

// seatbeltProfile: in Seatbelt the last matching rule wins, so the denies
// come first and the workspace and scratch allows last.
func seatbeltProfile(dir, tmp string) string {
	home, _ := os.UserHomeDir()
	if r, err := filepath.EvalSymlinks(home); err == nil {
		home = r
	}
	q := func(s string) string {
		return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
	}
	return strings.Join([]string{
		`(version 1)`,
		`(allow default)`,
		`(deny network*)`,
		`(deny mach-lookup)`,                   // no system services: clipboard, app launching, keychain
		`(deny process-info* (target others))`, // can't read other processes' arguments or environment
		`(deny signal (target others))`,        // can't kill or signal other processes
		`(deny process-exec (literal "/usr/bin/open") (literal "/usr/bin/osascript") (literal "/usr/bin/pbcopy") (literal "/usr/bin/pbpaste"))`,
		`(deny file-write*)`,
		`(allow file-write* (literal "/dev/null") (literal "/dev/dtracehelper"))`,
		`(deny file-read* (subpath "/Users") (subpath ` + q(home) + `) (subpath "/Volumes") (subpath "/private/tmp") (subpath "/private/var/folders") (subpath "/private/var/tmp") (subpath "/private/var/log") (subpath "/Library/Keychains") (subpath "/Library/Preferences") (subpath "/opt/homebrew/etc") (subpath "/usr/local/etc"))`,
		`(allow file-read-metadata)`, // stat only (ls -la needs ".."): no file contents, no folder listings
		`(allow file-read* (subpath ` + q(dir) + `) (subpath ` + q(tmp) + `))`,
		`(allow file-write* (subpath ` + q(tmp) + `))`,
	}, "\n")
}

// bwrapArgs mounts the system read-only, hides homes, temp folders and
// runtime sockets, binds the workspace read-only and the scratch folder
// read-write, and unshares the network and everything else it can.
func bwrapArgs(dir, tmp, script string) []string {
	args := []string{"--ro-bind", "/", "/"}
	hide := []string{"/home", "/root", "/tmp", "/var/tmp", "/run", "/mnt", "/media", "/srv"}
	if home, err := os.UserHomeDir(); err == nil && home != "" && home != "/" {
		hide = append(hide, home) // e.g. /var/home on Fedora Silverblue
	}
	for _, p := range hide {
		if st, err := os.Lstat(p); err == nil && st.IsDir() {
			args = append(args, "--tmpfs", p)
		}
	}
	return append(args,
		"--dev", "/dev",
		"--proc", "/proc",
		"--ro-bind", dir, dir,
		"--bind", tmp, tmp,
		"--unshare-all",
		"--new-session",
		"--die-with-parent",
		"--chdir", dir,
		"/bin/sh", "-c", script,
	)
}
