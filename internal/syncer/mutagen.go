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

// DataDirName is where tamp keeps Mutagen's own state, under the tamp home.
//
// tamp gives Mutagen a data directory of its own rather than sharing the
// user's. A client and a daemon of different releases refuse each other, and
// the user's Mutagen is theirs to upgrade whenever they like — pointing tamp
// at their daemon would turn their upgrade into tamp's outage.
const DataDirName = "mutagen"

// CLI drives the real Mutagen binary.
type CLI struct {
	// Home is the tamp home, under which the managed binary and Mutagen's own
	// state live.
	Home string
	// ReleaseBaseURL overrides where the pinned release is downloaded from. It
	// is a field so a test can serve the archive locally.
	ReleaseBaseURL string
	// LookPath finds an executable on the machine's PATH. It is a field for
	// the same reason: what the developer running the tests happens to have
	// installed must not decide what they say.
	LookPath func(string) (string, error)
}

// New returns the Mutagen driver for the machine whose tamp home is home.
func New(home string) *CLI { return &CLI{Home: home} }

// syncMode is what makes this two-way sync safe to point at a container:
// both sides propagate, and a file changed on both since the last pass is
// resolved in favour of the host — the side a person or an agent is editing.
const syncMode = "two-way-resolved"

func (c *CLI) Create(ctx context.Context, s Session, out io.Writer) error {
	args := []string{
		"sync", "create",
		"--name=" + s.Name,
		"--sync-mode=" + syncMode,
		// The user's own ~/.mutagen.yml has no business changing what tamp's
		// sessions do; every setting that matters is on this command line.
		"--no-global-configuration",
	}
	for _, ignore := range Ignores {
		args = append(args, "--ignore="+ignore)
	}
	args = append(args, s.Alpha, s.Beta)

	if err := c.run(ctx, s.DockerHost, out, args...); err != nil {
		return err
	}
	// Waiting for the first full pass is what makes "created" mean "the source
	// is on the host": the session exists the moment it is made, and the tree
	// behind it arrives some seconds later.
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

// sessionTemplate prints one session name per line. Asking Mutagen for exactly
// the field tamp wants beats parsing the table it prints for people, which is
// laid out for reading rather than for machines.
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

// run invokes Mutagen, downloading it first if the machine has none.
//
// dockerHost points Mutagen's docker transport at the same engine tamp
// resolved, rather than leaving it to find one for itself. It is empty for the
// commands that only talk to Mutagen's own daemon.
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
	if out != nil {
		cmd.Stdout, cmd.Stderr = out, out
	}

	// A command tamp is not narrating still has to be able to say why it
	// failed, so its output is kept rather than dropped.
	var captured bytes.Buffer
	if out == nil {
		cmd.Stdout, cmd.Stderr = &captured, &captured
	}
	if err := cmd.Run(); err != nil {
		reason := strings.TrimSpace(captured.String())
		if reason == "" {
			reason = err.Error()
		}
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("mutagen %s failed: %s", strings.Join(args, " "), firstLine(reason)),
			"tamp manages Mutagen itself — 'tamp doctor' reports what it found")
	}
	return nil
}

// firstLine keeps tamp's one-line error contract when Mutagen answers with a
// paragraph. The rest is on the stream when tamp was narrating the command.
func firstLine(s string) string {
	if newline := strings.Index(s, "\n"); newline >= 0 {
		return strings.TrimSpace(s[:newline])
	}
	return s
}

// A driver that has drifted from the interface would silently stop being the
// thing tamp's lifecycle is written against.
var _ Mutagen = (*CLI)(nil)
