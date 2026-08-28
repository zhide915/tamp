package engine

import (
	"context"
	"fmt"
	"io"
	"strconv"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"
	"github.com/zhide915/tamp/internal/exitcode"
)

// TailAll requests the whole log rather than a trailing slice.
const TailAll = 0

func (d *Docker) Logs(ctx context.Context, req LogRequest) error {
	_, api, err := d.connect()
	if err != nil {
		return err
	}

	tail := "all"
	if req.Tail > TailAll {
		tail = strconv.Itoa(req.Tail)
	}
	stream, err := api.ContainerLogs(ctx, req.Container, client.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     req.Follow,
		Tail:       tail,
	})
	if err != nil {
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot read the logs of %s: %v", req.Container, rootCause(err)),
			"check that the environment has been created with 'tamp list'")
	}
	defer func() { _ = stream.Close() }()

	// tamp's containers have no TTY, so the daemon frames the two streams.
	stdout, stderr := req.Stdout, req.Stderr
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	if _, err := stdcopy.StdCopy(stdout, stderr, stream); err != nil {
		// Cancellation ends a follow; that is success, not failure.
		if ctx.Err() != nil {
			return nil
		}
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("the log stream of %s ended badly: %v", req.Container, err), "")
	}
	return nil
}
