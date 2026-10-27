package sandbox

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Sandbox canaries: things a replay must never be able to do. The workspace
// lives in the system temp folder, as it does in a real replay.
func TestCanaries(t *testing.T) {
	if err := Available(); err != nil {
		t.Skip(err)
	}
	base := t.TempDir()
	ws, tmp := filepath.Join(base, "ws"), filepath.Join(base, "tmp")
	os.MkdirAll(ws, 0o755)
	os.WriteFile(filepath.Join(ws, "inside.txt"), []byte("hello"), 0o644)
	other := t.TempDir() // another folder in the temp area, e.g. another session's files
	os.WriteFile(filepath.Join(other, "secret.txt"), []byte("token"), 0o644)
	home, _ := os.UserHomeDir()

	run := func(cmd string) Result {
		t.Helper()
		r, err := Run(context.Background(), ws, tmp, cmd, 20*time.Second)
		if err != nil {
			t.Fatalf("%s: %v", cmd, err)
		}
		return r
	}
	if r := run("cat inside.txt"); r.ExitCode != 0 || r.Stdout != "hello" {
		t.Errorf("reading the workspace failed: %+v", r)
	}
	if r := run(`echo x > "$TMPDIR/scratch.txt" && cat "$TMPDIR/scratch.txt"`); r.ExitCode != 0 || strings.TrimSpace(r.Stdout) != "x" {
		t.Errorf("writing the scratch folder failed: %+v", r)
	}
	if r := run("echo x > made.txt"); r.ExitCode == 0 {
		t.Error("the workspace must be read-only, so a model can't fake evidence")
	}
	if r := run("cat " + filepath.Join(other, "secret.txt")); r.ExitCode == 0 || strings.Contains(r.Stdout, "token") {
		t.Errorf("read another temp folder: %+v", r)
	}
	// macOS denies the home folder; Linux mounts an empty folder over it.
	// Either way, nothing in the real one may show.
	if r := run("ls -A " + home); r.ExitCode == 0 {
		real := map[string]bool{}
		if entries, err := os.ReadDir(home); err == nil {
			for _, e := range entries {
				real[e.Name()] = true
			}
		}
		for _, name := range strings.Fields(r.Stdout) {
			if real[name] {
				t.Errorf("listed the home folder: %q is visible", name)
				break
			}
		}
	}
	for _, p := range []string{"/private/var/tmp", "/private/var/log", "/Library/Preferences", "/opt/homebrew/var", "/Library/Application Support"} {
		if _, err := os.Stat(p); err == nil {
			if r := run("ls " + p); r.ExitCode == 0 {
				t.Errorf("listed %s: %+v", p, r)
			}
		}
	}
	if r := run("curl -s -m 5 https://example.com >/dev/null"); r.ExitCode == 0 {
		t.Errorf("network was reachable: %+v", r)
	}
	if runtime.GOOS == "linux" {
		// Only system folders are mounted: /var and /run, where daemon
		// sockets live, aren't there at all.
		if r := run("ls /var /run"); r.ExitCode == 0 {
			t.Errorf("/var or /run is visible: %+v", r)
		}
	}
	// The tools replayed jobs use still work.
	if r := run(`grep -c hello inside.txt && find . -name '*.txt' | wc -l && sed -n 1p inside.txt && awk 'END { print NR }' inside.txt && ls -la >/dev/null`); r.ExitCode != 0 || strings.Fields(r.Stdout)[0] != "1" {
		t.Errorf("everyday tools failed: %+v", r)
	}
	if r := run("echo hi > /dev/tty"); r.ExitCode == 0 {
		t.Error("the sandbox must not reach a terminal")
	}
	if r := run("kill -0 " + strconv.Itoa(os.Getpid())); r.ExitCode == 0 {
		t.Error("the sandbox must not signal other processes")
	}
	if r := run("ps -p " + strconv.Itoa(os.Getpid()) + " -o command="); strings.Contains(r.Stdout, "sandbox.test") {
		t.Errorf("the sandbox can see other processes' command lines: %q", r.Stdout)
	}
	if r := run("env"); strings.Contains(r.Stdout, "ANTHROPIC") || strings.Contains(r.Stdout, "AWS_") {
		t.Errorf("environment leaked secrets: %s", r.Stdout)
	}
	// The read-only tools replays rely on must still work.
	for _, cmd := range []string{"grep -rn hello .", "find . -name '*.txt'", "sed -n 1p inside.txt", "awk '{print $1}' inside.txt",
		"wc -l inside.txt", "ls -la", "head -1 inside.txt | sort | uniq", "cat inside.txt | tr a-z A-Z", "python3 -c 'print(1)' || true"} {
		if r := run(cmd); r.ExitCode != 0 {
			t.Errorf("%q failed in the sandbox: %+v", cmd, r)
		}
	}
	if r := run("yes | head -c 1000000"); len(r.Stdout) > MaxOutput+100 || !strings.Contains(r.Stdout, "truncated") {
		t.Errorf("output wasn't capped: %d bytes", len(r.Stdout))
	}
}

func TestTimeoutKillsBackgroundJobs(t *testing.T) {
	if err := Available(); err != nil {
		t.Skip(err)
	}
	base := t.TempDir()
	start := time.Now()
	r, err := Run(context.Background(), base, filepath.Join(base, "tmp"), "sleep 30 & sleep 30; echo done", 500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if !r.TimedOut || time.Since(start) > 5*time.Second {
		t.Errorf("timeout not enforced: %+v after %v", r, time.Since(start))
	}
}

func TestPreflight(t *testing.T) {
	if err := Available(); err != nil {
		t.Skip(err)
	}
	if err := Preflight(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// A command that keeps writing is stopped once the scratch folder passes its
// limit, and the folder is emptied, so it can't fill the disk.
func TestScratchLimit(t *testing.T) {
	if err := Available(); err != nil {
		t.Skip(err)
	}
	defer func(n int64) { maxScratchBytes = n }(maxScratchBytes)
	maxScratchBytes = 1 << 20
	base := t.TempDir()
	ws, tmp := filepath.Join(base, "ws"), filepath.Join(base, "tmp")
	os.MkdirAll(ws, 0o755)
	start := time.Now()
	r, err := Run(context.Background(), ws, tmp, `i=0; while :; do i=$((i+1)); head -c 100000 /dev/zero > "$TMPDIR/f$i"; done`, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.Stderr, scratchFull) || r.TimedOut || time.Since(start) > 10*time.Second {
		t.Errorf("want a quick stop at the limit, got %+v after %v", r, time.Since(start))
	}
	if n := dirSize(tmp, 1<<40); n != 0 {
		t.Errorf("scratch folder should be emptied, has %d bytes", n)
	}
	if r, _ := Run(context.Background(), ws, tmp, `echo ok > "$TMPDIR/a" && cat "$TMPDIR/a"`, 10*time.Second); strings.TrimSpace(r.Stdout) != "ok" {
		t.Errorf("the next command should work normally: %+v", r)
	}
}

func TestParseEtime(t *testing.T) {
	for s, want := range map[string]time.Duration{
		"00:05":       5 * time.Second,
		"01:02":       62 * time.Second,
		"01:02:03":    3723 * time.Second,
		"2-00:00:01":  48*time.Hour + time.Second,
		"12:34:56:78": -1,
		"x:01":        -1,
	} {
		got, ok := parseEtime(s)
		if (want < 0) == ok || (ok && got != want) {
			t.Errorf("parseEtime(%q) = %v, %v; want %v", s, got, ok, want)
		}
	}
}

// A command can't start more than maxProcs processes on top of the user's
// own, so a fork bomb can't use up the user's whole allowance.
func TestProcessLimit(t *testing.T) {
	if err := Available(); err != nil {
		t.Skip(err)
	}
	base := t.TempDir()
	r, err := Run(context.Background(), base, filepath.Join(base, "tmp"), "ulimit -u 2>/dev/null || ulimit -p", 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(r.Stdout))
	if err != nil || n > userProcs()+maxProcs+100 {
		t.Errorf("want a process limit near %d, got %q", userProcs()+maxProcs, r.Stdout)
	}
}

// A process that leaves the command's process group, which a timeout kills,
// is blocked on macOS or stopped with the command everywhere.
func TestNoProcessOutlivesTheCommand(t *testing.T) {
	if err := Available(); err != nil {
		t.Skip(err)
	}
	base := t.TempDir()
	run := func(cmd string) Result {
		t.Helper()
		r, err := Run(context.Background(), base, filepath.Join(base, "tmp"), cmd, 20*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	if r := run(`python3 -c 'import os; os.posix_spawn'`); r.ExitCode != 0 {
		t.Skip("no python3 with posix_spawn in the sandbox")
	}
	if runtime.GOOS == "darwin" {
		for _, call := range []string{"os.setsid()", "os.setpgid(0, 0)"} {
			if r := run("python3 -c 'import os; " + call + "'"); r.ExitCode == 0 {
				t.Errorf("%s should be blocked", call)
			}
		}
	}
	// posix_spawn's setsid flag gets past the profile on macOS; the process
	// must still be gone once the command returns.
	run(`python3 -c "import os; os.posix_spawn('/bin/sleep', ['sleep', '97.31'], os.environ, setsid=True)"`)
	time.Sleep(300 * time.Millisecond)
	if out, _ := exec.Command("pgrep", "-f", "sleep 97.31").Output(); len(strings.TrimSpace(string(out))) > 0 {
		exec.Command("pkill", "-f", "sleep 97.31").Run()
		t.Errorf("a detached process outlived the command: pid %s", out)
	}
}
