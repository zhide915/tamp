package docker

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// wrapped runs argv through shellWrapper as docker exec would: under sh,
// as a session leader, which runc makes it.
func wrapped(t *testing.T, pidFile, stdin string, argv ...string) *exec.Cmd {
	t.Helper()
	for _, tool := range []string{"sh", "setsid", "pkill"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not on PATH", tool)
		}
	}
	cmd := exec.Command("sh", append([]string{"-c", shellWrapper, pidFile}, argv...)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdin = strings.NewReader(stdin)
	return cmd
}

func exitCode(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		return 0
	}
	exit, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("the wrapper did not run: %v", err)
	}
	if ws, ok := exit.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		t.Fatalf("the wrapper itself died of %v", ws.Signal())
	}
	return exit.ExitCode()
}

// The CLI harness runs the fake, so only a unit test reaches the script
// that the real Shell runs in the container.
func TestTheShellWrapperKeepsTheCommandsArgumentsStdinAndExitCode(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		name   string
		stdin  string
		argv   []string
		code   int
		stdout string
	}{
		{name: "quoting", argv: []string{"printf", "%s|", "a b", "it's", "$HOME", `"q"`}, stdout: `a b|it's|$HOME|"q"|`},
		{name: "stdin", stdin: "hello\n", argv: []string{"cat"}, stdout: "hello\n"},
		{name: "exit 3", argv: []string{"sh", "-c", "exit 3"}, code: 3},
		{name: "exit 130", argv: []string{"sh", "-c", "exit 130"}, code: 130},
		{name: "SIGKILL", argv: []string{"sh", "-c", "kill -KILL $$"}, code: 137},
		{name: "SIGTERM", argv: []string{"sh", "-c", "kill -TERM $$"}, code: 143},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pidFile := filepath.Join(dir, strings.ReplaceAll(tc.name, " ", "-"))
			cmd := wrapped(t, pidFile, tc.stdin, tc.argv...)
			out, err := cmd.Output()

			if code := exitCode(t, err); code != tc.code {
				t.Errorf("exit code = %d, want %d, as a plain docker exec reports", code, tc.code)
			}
			if string(out) != tc.stdout {
				t.Errorf("stdout = %q, want %q", out, tc.stdout)
			}
			if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
				t.Errorf("the pid file is left behind: %v", err)
			}
		})
	}
}

func TestTheStopScriptEndsTheCommandAndEverythingItStarted(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	// A Ctrl-C can come before the command writes its pid file, so the stop
	// script starts first and must wait for the file.
	cmd := wrapped(t, pidFile, "", "sh", "-c", "sleep 30; echo after")
	stop := exec.Command("sh", "-c", stopScript, pidFile)
	if err := stop.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	if err := stop.Wait(); err != nil {
		t.Fatalf("the stop script failed: %v", err)
	}

	select {
	case err := <-done:
		if code := exitCode(t, err); code != 130 {
			t.Errorf("exit code = %d, want 130", code)
		}
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("the command still runs 10 s after the stop script")
	}
}

func TestTheStopScriptIsQuietWhenTheCommandHasEnded(t *testing.T) {
	if _, err := exec.LookPath("pkill"); err != nil {
		t.Skip("pkill is not on PATH")
	}
	out, err := exec.Command("sh", "-c", stopScript, filepath.Join(t.TempDir(), "gone")).CombinedOutput()
	if err != nil || len(out) > 0 {
		t.Errorf("the stop script = %v, %q, want success and no output", err, out)
	}
}
