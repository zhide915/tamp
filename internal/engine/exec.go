package engine

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
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

	size := client.ConsoleSize{Width: req.Size.Width, Height: req.Size.Height}
	created, err := api.ExecCreate(ctx, req.Container, client.ExecCreateOptions{
		Cmd:          req.Cmd,
		Env:          req.Env,
		WorkingDir:   req.WorkDir,
		User:         req.User,
		TTY:          req.TTY,
		ConsoleSize:  size,
		AttachStdin:  req.Stdin != nil,
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return execError(req, err)
	}

	attached, err := api.ExecAttach(ctx, created.ID, client.ExecAttachOptions{TTY: req.TTY, ConsoleSize: size})
	if err != nil {
		return execError(req, err)
	}
	defer attached.Close()

	if req.Stdin != nil {
		// Not waited on: an idle terminal never reaches EOF, so the copy
		// dies with the process.
		go func() {
			_, _ = io.Copy(attached.Conn, req.Stdin)
			if !req.TTY {
				// Half-close gives a piped command its EOF. Skipped under TTY:
				// there EOF is a keystroke, and half-closing a Windows named
				// pipe kills the whole connection, output included.
				_ = attached.CloseWrite()
			}
		}()
	}

	if err := copyOutput(req, attached.Reader); err != nil {
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

// copyOutput drains the output stream; its end is also how Exec waits for
// the process — only then is the exit status meaningful.
func copyOutput(req ExecRequest, r io.Reader) error {
	stdout, stderr := req.Stdout, req.Stderr
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	if req.TTY {
		// A TTY carries one unframed stream.
		_, err := io.Copy(stdout, r)
		return err
	}
	// Without a TTY the daemon frames stdout and stderr separately.
	_, err := stdcopy.StdCopy(stdout, stderr, r)
	return err
}

// ExitError is a command that ran in a container and exited non-zero. A
// distinct type because a non-zero exit can be a legitimate "no" to a
// probe, unlike every other Exec failure, which is a fault.
type ExitError struct {
	Container string
	Cmd       []string
	Status    int
}

func (e *ExitError) Error() string { return e.reason().Error() }

// Unwrap exposes the exitcode.Error, so an uncaught ExitError still exits
// with tamp's failure code.
func (e *ExitError) Unwrap() error { return e.reason() }

func (e *ExitError) reason() *exitcode.Error {
	return exitcode.New(exitcode.CodeFailed,
		fmt.Sprintf("%s exited %d in %s", commandLine(e.Cmd), e.Status, e.Container),
		"the output above says why")
}

// Probe runs a yes/no command: exit 0 is yes, non-zero exit is no. Any
// other failure — daemon gone, container gone — is returned as an error,
// never read as "no".
func Probe(ctx context.Context, eng Engine, req ExecRequest) (bool, error) {
	err := eng.Exec(ctx, req)
	if err == nil {
		return true, nil
	}
	var refused *ExitError
	if errors.As(err, &refused) {
		return false, nil
	}
	return false, err
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

	// The daemon always answers a copy with a tar stream; a single file is a
	// one-entry archive.
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
		// Without this the daemon chowns extracted files to root, defeating
		// FileSpec's UID/GID.
		CopyUIDGID: true,
	})
	if err != nil {
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot write %s in %s: %v", f.Path, container, rootCause(err)),
			"check that "+path.Dir(f.Path)+" exists in the container")
	}
	return nil
}

// tarOneFile wraps a file in the one-entry archive the copy endpoint wants.
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
		// Fixed timestamp: these files are rewritten on every start, and two
		// identical writes should be identical.
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
	// VolumeCreate is idempotent: an existing volume is returned, not an
	// error.
	if _, err := api.VolumeCreate(ctx, client.VolumeCreateOptions{Name: name}); err != nil {
		return exitcode.New(exitcode.CodeEngineUnavailable,
			fmt.Sprintf("cannot create the %s volume: %v", name, rootCause(err)),
			"start Docker and try again")
	}
	return nil
}

func (d *Docker) RemoveVolume(ctx context.Context, name string) error {
	_, api, err := d.connect()
	if err != nil {
		return err
	}
	// Listed first because a volume that is already gone is the asked-for
	// outcome, not an error. The name filter matches substrings, so re-check.
	res, err := api.VolumeList(ctx, client.VolumeListOptions{
		Filters: make(client.Filters).Add("name", name),
	})
	if err != nil {
		return exitcode.New(exitcode.CodeEngineUnavailable,
			fmt.Sprintf("cannot list Docker volumes: %v", rootCause(err)),
			"start Docker and try again")
	}
	exists := false
	for _, v := range res.Items {
		if v.Name == name {
			exists = true
			break
		}
	}
	if !exists {
		return nil
	}
	if _, err := api.VolumeRemove(ctx, name, client.VolumeRemoveOptions{}); err != nil {
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot remove the %s volume: %v", name, rootCause(err)),
			"remove it yourself with 'docker volume rm "+name+"'")
	}
	return nil
}

func execError(req ExecRequest, err error) error {
	return exitcode.New(exitcode.CodeFailed,
		fmt.Sprintf("cannot run %s in %s: %v", commandLine(req.Cmd), req.Container, rootCause(err)),
		"check that the environment is running with 'tamp list'")
}

// commandLine renders argv for an error message, truncating a long script
// to its shell — the script is already in the output above.
func commandLine(cmd []string) string {
	line := strings.Join(cmd, " ")
	if len(line) > 60 {
		return cmd[0] + " ..."
	}
	return line
}
