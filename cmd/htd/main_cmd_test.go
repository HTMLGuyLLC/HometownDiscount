package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// runMainAsChild runs the real main() in a child process, because it exits
// the process on failure. The test binary re-runs itself with the arguments
// in HTD_TEST_ARGS; the child's exit status and streams are what a shell sees.
func runMainAsChild(t *testing.T, args ...string) (stdout, stderr string, exit int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestMainChild$")
	cmd.Env = append(os.Environ(),
		"HTD_TEST_MAIN=1",
		"HTD_TEST_ARGS="+strings.Join(args, "\x1f"),
		"HTD_HOME="+t.TempDir(),
		"CLAUDE_CONFIG_DIR="+t.TempDir(),
	)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &ee):
		exit = ee.ExitCode()
	default:
		t.Fatal(err)
	}
	return out.String(), errOut.String(), exit
}

// TestMainChild is not a test of its own: it is the child process of
// runMainAsChild, and does nothing in a normal run.
func TestMainChild(t *testing.T) {
	if os.Getenv("HTD_TEST_MAIN") != "1" {
		t.Skip("only runs as the child of runMainAsChild")
	}
	os.Args = append([]string{"htd"}, strings.Split(os.Getenv("HTD_TEST_ARGS"), "\x1f")...)
	main()
}

// A failing command prints "htd: <reason>" on stderr and exits 1; a command
// that works, and --help, exit 0 and print nothing on stderr.
func TestMainExitStatus(t *testing.T) {
	_, stderr, exit := runMainAsChild(t, "frobnicate")
	if exit != 1 || !strings.Contains(stderr, `htd: unknown command "frobnicate"`) {
		t.Errorf("unknown command: exit %d, stderr %q", exit, stderr)
	}

	stdout, stderr, exit := runMainAsChild(t, "version")
	if exit != 0 || !strings.Contains(stdout, "htd "+version) || stderr != "" {
		t.Errorf("version: exit %d, stdout %q, stderr %q", exit, stdout, stderr)
	}

	stdout, stderr, exit = runMainAsChild(t, "forget", "--help")
	if exit != 0 || !strings.Contains(stderr+stdout, "-all") {
		t.Errorf("--help should print the command's flags and exit 0: exit %d, stdout %q, stderr %q", exit, stdout, stderr)
	}
	if strings.Contains(stderr, "htd:") {
		t.Errorf("--help is not an error: %q", stderr)
	}

	_, stderr, exit = runMainAsChild(t, "map", "--days", "0")
	if exit != 1 || !strings.Contains(stderr, "htd: --days must be at least 1") {
		t.Errorf("map --days 0: exit %d, stderr %q", exit, stderr)
	}
}
