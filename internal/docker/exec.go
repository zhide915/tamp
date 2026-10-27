package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"slices"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"
	"github.com/zhide915/tamp/internal/exitcode"
)

func (d *Daemon) Exec(ctx context.Context, e Exec) error {
	return d.exec(ctx, e, nil)
}

func (d *Daemon) exec(ctx context.Context, e Exec, stdin io.Reader) error {
	_, api, err := d.connect()
	if err != nil {
		return err
	}
	created, err := api.ExecCreate(ctx, e.Container, client.ExecCreateOptions{
		Cmd:          e.Cmd,
		AttachStdin:  stdin != nil,
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return execError(e, err)
	}
	attached, err := api.ExecAttach(ctx, created.ID, client.ExecAttachOptions{})
	if err != nil {
		return execError(e, err)
	}
	defer attached.Close()
	// Reading the output blocks past ctx, so its end closes the stream. The
	// command runs on in the container: Docker has no call that stops an exec.
	defer context.AfterFunc(ctx, attached.Close)()

	if stdin != nil {
		go func() {
			_, _ = io.Copy(attached.Conn, stdin)
			// The half-close is the command's end of input.
			_ = attached.CloseWrite()
		}()
	}

	stdout, stderr := e.Stdout, e.Stderr
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	// The output ending is how the command's end is seen.
	if _, err := stdcopy.StdCopy(stdout, stderr, attached.Reader); err != nil {
		return execError(e, err)
	}

	res, err := api.ExecInspect(ctx, created.ID, client.ExecInspectOptions{})
	if err != nil {
		return execError(e, err)
	}
	if res.ExitCode != 0 {
		return &ExitError{Status: res.ExitCode, Err: exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("%s exited %d in %s", commandLine(e.Cmd), res.ExitCode, e.Container),
			"fix what the output above names")}
	}
	return nil
}

// ExitError is Exec's answer for a command that ran and exited non-zero, as
// against one that Docker could not run.
type ExitError struct {
	Status int
	Err    *exitcode.Error
}

func (e *ExitError) Error() string { return e.Err.Error() }

func (e *ExitError) Unwrap() error { return e.Err }

func (d *Daemon) ReadFile(ctx context.Context, container, path string) ([]byte, error) {
	_, api, err := d.connect()
	if err != nil {
		return nil, err
	}
	res, err := api.CopyFromContainer(ctx, container, client.CopyFromContainerOptions{SourcePath: path})
	if cerrdefs.IsNotFound(err) {
		return nil, fmt.Errorf("%s in %s: %w", path, container, ErrNotExist)
	}
	if err != nil {
		return nil, exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot read %s in %s: %v", path, container, rootCause(err)),
			"check that the container is running with `docker ps`")
	}
	defer func() { _ = res.Content.Close() }()

	// A copy always comes as a tar stream; one file is a one-entry archive.
	tr := tar.NewReader(res.Content)
	if _, err := tr.Next(); err != nil {
		return nil, fmt.Errorf("read %s in %s: %w", path, container, err)
	}
	return io.ReadAll(tr)
}

func (d *Daemon) WriteFile(ctx context.Context, container, path string, data []byte) error {
	// Written by the image's user, so the bench can rewrite it, and renamed
	// into place, so the frappe container never reads half a Procfile.
	return d.exec(ctx, Exec{
		Container: container,
		Cmd:       []string{"sh", "-c", `cat > "$1.tamp-new" && mv "$1.tamp-new" "$1"`, "sh", path},
	}, bytes.NewReader(data))
}

func execError(e Exec, err error) error {
	return exitcode.New(exitcode.CodeFailed,
		fmt.Sprintf("cannot run %s in %s: %v", commandLine(e.Cmd), e.Container, rootCause(err)),
		"check that the container is running with `docker ps`")
}

// commandLine renders argv for an error message, keeping the words that fit
// in 60 characters: the command's own output is already above.
func commandLine(cmd []string) string {
	line := cmd[0]
	for _, word := range cmd[1:] {
		if len(line)+1+len(word) > 60 {
			return line + " ..."
		}
		line += " " + word
	}
	return line
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
