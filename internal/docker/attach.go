package docker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/zhide915/tamp/internal/exitcode"
)

// Shell runs docker exec, whose CLI already handles the terminal's raw mode,
// resizing and the Windows console.
func (d *Daemon) Shell(ctx context.Context, s Shell) (int, error) {
	args := []string{"exec", "--interactive"}
	if s.TTY {
		args = append(args, "--tty")
	}
	args = append(args, "--user", s.User, "--workdir", s.Dir, s.Container)
	cmd, pidFile := s.Cmd, ""
	if !s.TTY {
		// Without a TTY, Ctrl-C ends docker but not the command, so the
		// command runs in a session of its own, whose ID tamp records to stop it.
		pidFile = fmt.Sprintf("/tmp/tamp-shell-%d-%d", os.Getpid(), time.Now().UnixNano())
		cmd = append([]string{"sh", "-c", shellWrapper, pidFile}, s.Cmd...)
	}
	code, interrupted, err := d.attach(ctx, 0, s.Stdin, s.Stdout, s.Stderr, append(args, cmd...)...)
	if err != nil || !interrupted {
		return code, err
	}
	if pidFile != "" {
		stop := []string{"exec", "--user", s.User, s.Container, "sh", "-c", stopScript, pidFile}
		if _, _, err := d.attach(context.Background(), 0, nil, nil, nil, stop...); err != nil {
			return 0, err
		}
	}
	return 128 + int(syscall.SIGINT), nil
}

// shellWrapper runs "$@" as the leader of a new session, writes the session
// ID to the file $0, and removes the file when the command ends. setsid runs
// in a child of this sh, which is no group leader, so it never forks.
const shellWrapper = `setsid -w sh -c 'echo $$ > "$0"; exec "$@"' "$0" "$@"; c=$?; rm -f "$0"; exit $c`

// stopScript sends SIGINT to the session that the file $0 names, waiting up
// to a second for a command that has not written the file yet.
const stopScript = `for i in 1 2 3 4 5 6 7 8 9 10; do
  if [ -s "$0" ]; then pkill -INT -s "$(cat "$0")"; exit 0; fi
  sleep 0.1
done 2>/dev/null`

func (d *Daemon) Logs(ctx context.Context, l Logs) error {
	args := []string{"logs"}
	if l.Follow {
		args = append(args, "--follow")
	}
	if l.Tail != "" {
		args = append(args, "--tail", l.Tail)
	}
	code, interrupted, err := d.attach(ctx, 200*time.Millisecond, nil, l.Stdout, l.Stderr, append(args, l.Container)...)
	// Ctrl-C is how the user ends --follow; docker exits 130 when it caught it.
	if err == nil && code != 0 && code != 128+int(syscall.SIGINT) && !interrupted {
		err = exitcode.New(exitcode.CodeFailed, fmt.Sprintf("docker logs %s exited %d", l.Container, code),
			"check the container with `docker ps --all`")
	}
	return err
}

// attach runs the docker CLI on the caller's streams. A failure waits up to
// grace for the Ctrl-C it may have died of, which can reach tamp after docker exits.
func (d *Daemon) attach(ctx context.Context, grace time.Duration, stdin io.Reader, stdout, stderr io.Writer, args ...string) (code int, interrupted bool, err error) {
	addr, _, err := d.connect()
	if err != nil {
		return 0, false, err
	}
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Env = append(os.Environ(), "DOCKER_HOST="+addr.Host)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	// Ctrl-C reaches docker through the terminal too: tamp waits for its exit
	// code instead of dying first.
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	defer signal.Stop(interrupts)

	err = cmd.Run()
	// On Windows, docker can exit 0 on Ctrl-C.
	select {
	case <-interrupts:
		interrupted = true
	default:
	}
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		var killed bool
		code, killed = exitStatus(exit)
		interrupted = interrupted || killed
		if !interrupted && grace > 0 {
			select {
			case <-interrupts:
				interrupted = true
			case <-time.After(grace):
			}
		}
		return code, interrupted, nil
	case errors.Is(err, exec.ErrNotFound):
		return 0, false, exitcode.New(exitcode.CodeDockerUnavailable, "the docker command is not on PATH",
			"install Docker Desktop, or add the docker command to PATH")
	}
	return 0, interrupted, err
}

// statusControlCExit is the exit status of a Windows process ended by Ctrl-C.
const statusControlCExit = 0xC000013A

// exitStatus is a shell's exit code for how docker ended: 128 plus the
// signal when one killed it, as a shell reports, and whether Ctrl-C killed it.
// An exit of 130 is the command's own, not a Ctrl-C that tamp can stop.
func exitStatus(exit *exec.ExitError) (code int, interrupted bool) {
	if ws, ok := exit.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal()), ws.Signal() == syscall.SIGINT
	}
	code = exit.ExitCode()
	if uint32(code) == statusControlCExit {
		return 128 + int(syscall.SIGINT), true
	}
	return code, false
}
