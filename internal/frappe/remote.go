package frappe

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/zhide915/tamp/internal/engine"
)

// RemoteError is git's "no" to a remote probe: the source did not answer from
// inside the container, and Output is what git said about why.
type RemoteError struct {
	Source string
	Output string
}

func (e *RemoteError) Error() string {
	return fmt.Sprintf("%s did not answer from inside the container: %s",
		e.Source, strings.TrimSpace(e.Output))
}

// CheckRemote probes the source from inside the container without fetching.
// Prompts are disabled, so a private source fails at once rather than waiting
// on a terminal that isn't there; env may carry a bridged credential. A
// refusal comes back as *RemoteError; any other failure is the engine's.
func (b *Bench) CheckRemote(ctx context.Context, source string, env []string) error {
	var out bytes.Buffer
	err := b.Engine.Exec(ctx, engine.ExecRequest{
		Container: b.Container,
		Cmd:       engine.Script(checkRemoteScript, source),
		Env:       append([]string{"GIT_TERMINAL_PROMPT=0"}, env...),
		WorkDir:   BenchDir,
		Stdout:    &out,
		Stderr:    &out,
	})
	if err == nil {
		return nil
	}
	var exit *engine.ExitError
	if errors.As(err, &exit) {
		return &RemoteError{Source: source, Output: out.String()}
	}
	return err
}

const checkRemoteScript = `exec git ls-remote -- "$1" HEAD`
