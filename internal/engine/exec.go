package engine

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"
	"github.com/zhide915/tamp/internal/exitcode"
)

func (d *Docker) Exec(ctx context.Context, req ExecRequest) error {
	_, api, err := d.connect()
	if err != nil {
		return err
	}

	created, err := api.ExecCreate(ctx, req.Container, client.ExecCreateOptions{
		Cmd:        req.Cmd,
		Env:        req.Env,
		WorkingDir: req.WorkDir,
		User:       req.User,
		// No TTY: tamp is not a terminal for these commands, and without one
		// the daemon multiplexes the two streams so stdout and stderr arrive
		// separately — which is what lets tamp read a command's answer
		// without its progress chatter mixed in.
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return execError(req, err)
	}

	attached, err := api.ExecAttach(ctx, created.ID, client.ExecAttachOptions{})
	if err != nil {
		return execError(req, err)
	}
	defer attached.Close()

	stdout, stderr := req.Stdout, req.Stderr
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	// Reading to EOF is also how tamp waits for the command: the stream ends
	// when the process does, and only then is the exit code below meaningful.
	if _, err := stdcopy.StdCopy(stdout, stderr, attached.Reader); err != nil {
		return execError(req, err)
	}

	res, err := api.ExecInspect(ctx, created.ID, client.ExecInspectOptions{})
	if err != nil {
		return execError(req, err)
	}
	if res.ExitCode != 0 {
		return &ExitError{Container: req.Container, Cmd: req.Cmd, Status: res.ExitCode}
	}
	return nil
}

// ExitError is a command that ran inside a container and came back non-zero.
//
// It has a type of its own because a failed command is not always a fault:
// tamp asks a container questions by running commands in it, and "no" comes
// back as an exit status. Everything else Exec can return — an unreachable
// daemon, a container that has gone away — is a fault, and telling the two
// apart is what stops tamp reading a broken engine as an empty toolchain.
type ExitError struct {
	Container string
	Cmd       []string
	Status    int
}

func (e *ExitError) Error() string { return e.reason().Error() }

// Unwrap is what makes an exit status still exit 1: the wrapped error carries
// tamp's exit code, so a command that does not catch this returns the same
// thing it would have without the type.
func (e *ExitError) Unwrap() error { return e.reason() }

func (e *ExitError) reason() *exitcode.Error {
	return exitcode.New(exitcode.CodeFailed,
		fmt.Sprintf("%s exited %d in %s", commandLine(e.Cmd), e.Status, e.Container),
		"the output above says why")
}

func (d *Docker) ReadFile(ctx context.Context, container, filePath string) ([]byte, error) {
	_, api, err := d.connect()
	if err != nil {
		return nil, err
	}

	res, err := api.CopyFromContainer(ctx, container, client.CopyFromContainerOptions{SourcePath: filePath})
	if err != nil {
		return nil, exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot read %s from %s: %v", filePath, container, rootCause(err)),
			"check that the container is running")
	}
	defer func() { _ = res.Content.Close() }()

	// The daemon answers a copy with a tar stream whatever was asked for, so a
	// single file arrives as an archive of one entry.
	tr := tar.NewReader(res.Content)
	if _, err := tr.Next(); err != nil {
		return nil, exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot read %s from %s: %v", filePath, container, err),
			"check that it is a file rather than a directory")
	}
	body, err := io.ReadAll(tr)
	if err != nil {
		return nil, exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot read %s from %s: %v", filePath, container, err), "")
	}
	return body, nil
}

func (d *Docker) WriteFile(ctx context.Context, container string, f FileSpec) error {
	_, api, err := d.connect()
	if err != nil {
		return err
	}

	archive, err := tarOneFile(f)
	if err != nil {
		return err
	}
	_, err = api.CopyToContainer(ctx, container, client.CopyToContainerOptions{
		DestinationPath: path.Dir(f.Path),
		Content:         archive,
		// Without this the daemon rewrites every extracted file to its own
		// root, which is exactly the ownership FileSpec exists to avoid.
		CopyUIDGID: true,
	})
	if err != nil {
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot write %s in %s: %v", f.Path, container, rootCause(err)),
			"check that "+path.Dir(f.Path)+" exists in the container")
	}
	return nil
}

// tarOneFile wraps a file in the one-entry archive the daemon's copy endpoint
// expects.
func tarOneFile(f FileSpec) (io.Reader, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	header := &tar.Header{
		Typeflag: tar.TypeReg,
		Name:     path.Base(f.Path),
		Mode:     int64(f.Mode.Perm()),
		Size:     int64(len(f.Data)),
		Uid:      f.UID,
		Gid:      f.GID,
		// A fixed timestamp rather than the clock: tamp rewrites these files
		// on every start, and a changing mtime would be the only difference
		// between two identical writes.
		ModTime: time.Unix(0, 0),
	}
	if err := tw.WriteHeader(header); err != nil {
		return nil, tarError(f.Path, err)
	}
	if _, err := tw.Write(f.Data); err != nil {
		return nil, tarError(f.Path, err)
	}
	if err := tw.Close(); err != nil {
		return nil, tarError(f.Path, err)
	}
	return &buf, nil
}

func tarError(filePath string, err error) error {
	return exitcode.New(exitcode.CodeFailed,
		fmt.Sprintf("cannot package %s for the container: %v", filePath, err),
		"report this — it is a bug in tamp")
}

func (d *Docker) EnsureVolume(ctx context.Context, name string) error {
	_, api, err := d.connect()
	if err != nil {
		return err
	}
	// Docker's volume create is idempotent: asking for a volume that is
	// already there returns it rather than failing.
	if _, err := api.VolumeCreate(ctx, client.VolumeCreateOptions{Name: name}); err != nil {
		return exitcode.New(exitcode.CodeEngineUnavailable,
			fmt.Sprintf("cannot create the %s volume: %v", name, rootCause(err)),
			"start Docker and try again")
	}
	return nil
}

func execError(req ExecRequest, err error) error {
	return exitcode.New(exitcode.CodeFailed,
		fmt.Sprintf("cannot run %s in %s: %v", commandLine(req.Cmd), req.Container, rootCause(err)),
		"check that the environment is running with 'tamp list'")
}

// commandLine renders argv for an error message. A long script passed to a
// shell is cut down to the shell itself: the point of the line is which
// command failed, and the script is already in the output above it.
func commandLine(cmd []string) string {
	line := strings.Join(cmd, " ")
	if len(line) > 60 {
		return cmd[0] + " ..."
	}
	return line
}
