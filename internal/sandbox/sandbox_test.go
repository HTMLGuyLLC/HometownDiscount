package sandbox

import (
	"context"
	"os"
	"path/filepath"
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
	for _, p := range []string{"/private/var/tmp", "/private/var/log", "/Library/Preferences"} {
		if _, err := os.Stat(p); err == nil {
			if r := run("ls " + p); r.ExitCode == 0 {
				t.Errorf("listed %s: %+v", p, r)
			}
		}
	}
	if r := run("curl -s -m 5 https://example.com >/dev/null"); r.ExitCode == 0 {
		t.Errorf("network was reachable: %+v", r)
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
