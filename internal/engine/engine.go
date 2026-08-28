// Package engine is tamp's boundary with Docker: endpoint detection, the
// Docker API, and compose invocation. It is the single point tamp fakes in
// tests; everything above it runs real code against a recording fake here.
package engine

import (
	"context"
	"io"
	"io/fs"
)

// Info is the engine tamp reached.
type Info struct {
	Address Address
	Version string // Docker Engine version, e.g. "29.7.2"
}

// ComposeProject identifies one environment's compose project.
type ComposeProject struct {
	// Name is the -p project name, tamp-<env>-<hash>.
	Name string
	// File is the absolute path of the generated compose.yaml.
	File string
	// Dir is the environment directory; compose resolves the file's relative
	// paths against it.
	Dir string
}

// Removal selects how much a compose down destroys. Distinct from a bool
// because volumes hold the site's database.
type Removal int

const (
	// KeepVolumes removes containers and the network only.
	KeepVolumes Removal = iota
	// RemoveVolumes also destroys the environment's volumes.
	RemoveVolumes
)

// Container is one of an environment's containers.
type Container struct {
	// Service is the compose service name.
	Service string
	Running bool
}

// Network is a Docker network and its attached containers. Each environment
// owns one; the router joins it to reach containers with no published ports.
type Network struct {
	Name       string
	Containers []string
}

// ExecRequest is one command run inside a running container.
type ExecRequest struct {
	Container string
	// Cmd is argv; it is not run through a shell.
	Cmd []string
	// Env adds KEY=VALUE entries to the image's environment.
	Env []string
	// WorkDir overrides the image's working directory.
	WorkDir string
	// User overrides the image's user.
	User string
	// Stdout and Stderr receive the demultiplexed streams; nil discards.
	// With TTY there is a single stream and it arrives on Stdout.
	Stdout, Stderr io.Writer
	// Stdin, when set, is attached to the command's standard input.
	Stdin io.Reader
	// TTY allocates a pseudo-terminal, at the cost of stdout/stderr
	// separation.
	TTY bool
	// Size is the terminal size; meaningful only with TTY.
	Size ConsoleSize
}

// ConsoleSize is a terminal size in character cells; zero uses the daemon's
// default.
type ConsoleSize struct{ Width, Height uint }

// LogRequest is one container-log read.
type LogRequest struct {
	Container string
	// Follow keeps streaming until the caller's context is cancelled.
	Follow bool
	// Tail is the number of trailing lines to start from; TailAll for all.
	Tail int
	// Stdout and Stderr receive the demultiplexed streams.
	Stdout, Stderr io.Writer
}

// Script builds argv running a shell script with positional arguments.
// bash, not sh: the scripts source nvm, which needs bash. The "tamp" word
// becomes $0 so args start at $1; passing args beside the script avoids
// shell escaping.
func Script(script string, args ...string) []string {
	return append([]string{"bash", "-c", script, "tamp"}, args...)
}

// FileSpec is a file to place into a container.
type FileSpec struct {
	// Path is absolute inside the container; its directory must exist.
	Path string
	Data []byte
	Mode fs.FileMode
	// UID and GID own the file: the bench runs as a non-root user, and a
	// root-owned file would be one it can never rewrite.
	UID, GID int
}

// Engine is the whole of tamp's dependency on Docker. An unreachable engine
// surfaces as an *exitcode.Error carrying CodeEngineUnavailable and a fix,
// so commands can return it unchanged as exit 4.
type Engine interface {
	// Ping resolves the endpoint and confirms the daemon answers.
	Ping(ctx context.Context) (Info, error)
	// ComposeVersion reports the compose v2 plugin version. It works without
	// a reachable daemon, so doctor can report it while Docker is stopped.
	ComposeVersion(ctx context.Context) (string, error)

	// ComposeUp creates and starts the project's containers and waits for
	// their healthchecks. All compose operations stream engine output to out.
	ComposeUp(ctx context.Context, p ComposeProject, out io.Writer) error
	// ComposeStop stops containers, leaving them and all volumes in place.
	ComposeStop(ctx context.Context, p ComposeProject, out io.Writer) error
	// ComposeRestart restarts one service, re-running the command its
	// container was created with.
	ComposeRestart(ctx context.Context, p ComposeProject, service string, out io.Writer) error
	// ComposeDown removes containers and the network; volumes only with
	// RemoveVolumes.
	ComposeDown(ctx context.Context, p ComposeProject, removal Removal, out io.Writer) error

	// Containers reports the project's containers, running or not. No
	// containers is an empty slice, not an error.
	Containers(ctx context.Context, project string) ([]Container, error)

	// InspectNetwork reports a network and its containers. A missing network
	// is (nil, nil): tamp keeps routes for stopped environments.
	InspectNetwork(ctx context.Context, name string) (*Network, error)
	// ConnectNetwork attaches a container to a network.
	ConnectNetwork(ctx context.Context, network, container string) error
	// DisconnectNetwork detaches a container; required before the network can
	// be removed.
	DisconnectNetwork(ctx context.Context, network, container string) error

	// EnsureVolume creates a volume if absent: compose refuses to start with
	// missing external volumes and will not create them.
	EnsureVolume(ctx context.Context, name string) error
	// HasVolumes reports whether any of the project's volumes exist.
	HasVolumes(ctx context.Context, project string) (bool, error)

	// Exec runs a command in a container and waits. A non-zero exit is an
	// error; output goes to the request's streams.
	Exec(ctx context.Context, req ExecRequest) error
	// Logs copies a container's log to the request's streams.
	Logs(ctx context.Context, req LogRequest) error
	// ReadFile returns a file's contents from inside a container.
	ReadFile(ctx context.Context, container, path string) ([]byte, error)
	// WriteFile places a file inside a container, replacing any existing one.
	WriteFile(ctx context.Context, container string, f FileSpec) error
}
