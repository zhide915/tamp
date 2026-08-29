package syncer

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/zhide915/tamp/internal/exitcode"
)

// DataDirName is Mutagen's state directory under the tamp home. tamp runs its
// own daemon rather than sharing the user's: Mutagen releases refuse each
// other, and the user upgrading theirs must not break tamp.
const DataDirName = "mutagen"

// CLI drives the real Mutagen binary.
type CLI struct {
	Home string
	// ReleaseBaseURL overrides the download source so tests can serve the
	// archive locally.
	ReleaseBaseURL string
	// LookPath stands in for exec.LookPath so tests are independent of what
	// the machine has installed.
	LookPath func(string) (string, error)
}

// New returns a Mutagen driver rooted at the given tamp home.
func New(home string) *CLI { return &CLI{Home: home} }

// syncMode propagates both ways and resolves a two-sided change in favour of
// the host — the side being edited.
const syncMode = "two-way-resolved"

func (c *CLI) Create(ctx context.Context, s Session, out io.Writer) error {
	args := []string{
		"sync", "create",
		"--name=" + s.Name,
		"--sync-mode=" + syncMode,
		// Keep the user's ~/.mutagen.yml out of tamp's sessions.
		"--no-global-configuration",
	}
	for _, ignore := range Ignores {
		args = append(args, "--ignore="+ignore)
	}
	args = append(args, s.Alpha, s.Beta)

	if err := c.run(ctx, s.DockerHost, out, args...); err != nil {
		return err
	}
	// Flush waits for the first full pass, so returning means the source is
	// actually on the host.
	return c.run(ctx, s.DockerHost, out, "sync", "flush", s.Name)
}

func (c *CLI) Pause(ctx context.Context, name string) error {
	return c.run(ctx, "", nil, "sync", "pause", name)
}

func (c *CLI) Resume(ctx context.Context, name string) error {
	return c.run(ctx, "", nil, "sync", "resume", name)
}

func (c *CLI) Terminate(ctx context.Context, name string) error {
	return c.run(ctx, "", nil, "sync", "terminate", name)
}

func (c *CLI) Flush(ctx context.Context, name string) error {
	return c.run(ctx, "", nil, "sync", "flush", name)
}

func (c *CLI) Report(ctx context.Context, name string) (string, error) {
	var out bytes.Buffer
	if err := c.run(ctx, "", &out, "sync", "list", name); err != nil {
		return "", err
	}
	return out.String(), nil
}

func (c *CLI) StopDaemon(ctx context.Context) error {
	return c.run(ctx, "", nil, "daemon", "stop")
}

// sessionTemplate asks Mutagen for names directly rather than parsing its
// human-oriented table.
const sessionTemplate = `{{range .}}{{.Name}}
{{end}}`

func (c *CLI) Sessions(ctx context.Context) ([]string, error) {
	var out bytes.Buffer
	if err := c.run(ctx, "", &out, "sync", "list", "--template="+sessionTemplate); err != nil {
		return nil, err
	}

	var names []string
	for line := range strings.SplitSeq(out.String(), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			names = append(names, line)
		}
	}
	return names, nil
}

// run invokes Mutagen, installing it first if needed. dockerHost pins the
// docker transport to the engine tamp resolved; it is empty for commands that
// only talk to Mutagen's own daemon.
func (c *CLI) run(ctx context.Context, dockerHost string, out io.Writer, args ...string) error {
	binary, err := c.Ensure(ctx)
	if err != nil {
		return err
	}

	dataDir := filepath.Join(c.Home, DataDirName)
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot create %s: %v", dataDir, err),
			"check the permissions on your ~/.tamp directory")
	}

	cmd := exec.CommandContext(ctx, binary.Path, args...)
	cmd.Env = append(os.Environ(), "MUTAGEN_DATA_DIRECTORY="+dataDir)
	if dockerHost != "" {
		cmd.Env = append(cmd.Env, "DOCKER_HOST="+dockerHost)
	}

	// Always captured, streamed as well when the caller wants it: a failure
	// has to be able to say why even when its words already went somewhere.
	var captured bytes.Buffer
	sink := io.Writer(&captured)
	if out != nil {
		sink = io.MultiWriter(out, &captured)
	}
	cmd.Stdout, cmd.Stderr = sink, sink
	if err := cmd.Run(); err != nil {
		reason := strings.TrimSpace(captured.String())
		if reason == "" {
			reason = err.Error()
		}
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("mutagen %s failed: %s", strings.Join(args, " "), lastLine(reason)),
			"tamp manages Mutagen itself — 'tamp doctor' reports what it found")
	}
	return nil
}

// lastLine trims a multi-line transcript to tamp's one-line contract, from
// the end: a streamed run opens with progress lines, and the failure is what
// Mutagen says last.
func lastLine(s string) string {
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return s
}

var _ Mutagen = (*CLI)(nil)
