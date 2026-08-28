// Package engine is tamp's boundary with the container engine: finding
// Docker, asking the Docker API, and invoking compose.
//
// It is the only thing tamp fakes in tests. Everything above it — commands,
// diagnosis, later the whole environment lifecycle — is exercised against real
// code with a recording fake standing in here, so a test that passes says
// something about tamp rather than about the mock.
package engine

import (
	"context"
	"io"
	"io/fs"
)

// Info is what tamp learned by reaching the engine.
type Info struct {
	Address Address
	Version string // the Docker Engine version, e.g. "29.7.2"
}

// ComposeProject identifies one environment's compose project: the name every
// container, volume and network beneath it is prefixed with, and the generated
// file that describes it.
type ComposeProject struct {
	// Name is the -p project name, tamp-<env>-<hash>.
	Name string
	// File is the absolute path to the generated compose.yaml.
	File string
	// Dir is the environment directory, which compose resolves the file's
	// relative paths — the database secret, for one — against.
	Dir string
}

// Removal says how much of an environment a compose down takes with it.
//
// It is a named type rather than a bool because the call sites are `tamp
// stop` and `tamp rm --volumes`, and the difference between them is every
// site's database.
type Removal int

const (
	// KeepVolumes removes containers and the network. Volumes always survive
	// stop, and survive rm unless the user asks otherwise.
	KeepVolumes Removal = iota
	// RemoveVolumes additionally destroys the environment's storage layers.
	RemoveVolumes
)

// Container is one of an environment's containers, as the engine sees it.
type Container struct {
	// Service is the compose service name: frappe, mariadb, redis-cache...
	Service string
	Running bool
}

// ExecRequest is one command tamp runs inside a running container.
//
// It is tamp's way into a live environment: bench, uv and nvm are somebody
// else's CLI, and tamp runs them rather than reimplementing them.
type ExecRequest struct {
	// Container is the container's name, as compose named it.
	Container string
	// Cmd is argv. Nothing runs it through a shell unless Cmd names one.
	Cmd []string
	// Env adds to the image's own environment, as KEY=VALUE strings.
	Env []string
	// WorkDir overrides the image's working directory.
	WorkDir string
	// User overrides the image's user. tamp sets it only to root, and only to
	// make a freshly created volume writable by the user the bench runs as.
	User string
	// Stdout and Stderr receive the command's two streams, demultiplexed.
	// Either may be nil, which discards that stream.
	Stdout, Stderr io.Writer
}

// Script builds the argv that runs a shell script with positional arguments.
//
// bash rather than sh: tamp's scripts source nvm, which is a bash script and
// will not run under dash. The name in the middle is what makes the caller's
// arguments $1 onwards — bash gives $0 to the word after the script.
//
// Arguments travel beside the script rather than inside it, so that what tamp
// asked for is visible in the command it ran, and so that nothing tamp
// substitutes has to be escaped for a shell.
func Script(script string, args ...string) []string {
	return append([]string{"bash", "-c", script, "tamp"}, args...)
}

// FileSpec is a file tamp puts into a container.
type FileSpec struct {
	// Path is absolute, inside the container. Its directory must exist.
	Path string
	Data []byte
	Mode fs.FileMode
	// UID and GID own the file once it lands. They are spelled out rather than
	// inherited because the bench container runs as a non-root user, and a
	// file tamp drops in as root is one bench itself can never rewrite.
	UID, GID int
}

// Engine is the whole of tamp's dependency on Docker.
//
// Every method's failure is an *exitcode.Error carrying CodeEngineUnavailable
// and a fix, which is what makes exit 4 tamp's answer to an unreachable
// engine: a command that cannot proceed without Docker returns the error
// unchanged, and the user gets something to act on rather than a stack trace.
type Engine interface {
	// Ping resolves the engine's address and confirms the daemon answers.
	Ping(ctx context.Context) (Info, error)
	// ComposeVersion reports the version of the `docker compose` v2 plugin.
	// It deliberately does not need a reachable daemon: a user whose Docker is
	// merely stopped should still be told whether compose is installed.
	ComposeVersion(ctx context.Context) (string, error)

	// ComposeUp creates and starts the project's containers, pulling any
	// images the machine does not have, and waits for the healthchecks the
	// generated file declares.
	//
	// Every compose operation streams the engine's own output to out rather
	// than to a terminal, which is how create captures it into create.log and
	// how tests read what compose was told to do.
	ComposeUp(ctx context.Context, p ComposeProject, out io.Writer) error
	// ComposeStop stops the project's containers, leaving them — and every
	// volume — in place.
	ComposeStop(ctx context.Context, p ComposeProject, out io.Writer) error
	// ComposeRestart restarts one of the project's services, which re-runs the
	// command its container was created with. It is how tamp hands a
	// container a job it could not do when it first started.
	ComposeRestart(ctx context.Context, p ComposeProject, service string, out io.Writer) error
	// ComposeDown removes the project's containers and network, and its
	// volumes only when asked.
	ComposeDown(ctx context.Context, p ComposeProject, removal Removal, out io.Writer) error

	// Containers reports the project's containers, running or not. An
	// environment that was never created, or whose containers were removed,
	// has none — that is an empty slice, not an error.
	Containers(ctx context.Context, project string) ([]Container, error)

	// EnsureVolume creates a volume unless it is already there. It exists
	// because compose refuses to start a project whose external volumes are
	// missing, and will not create one itself.
	EnsureVolume(ctx context.Context, name string) error

	// Exec runs a command inside a container and waits for it. A non-zero exit
	// is an error; what the command said is on the request's streams.
	Exec(ctx context.Context, req ExecRequest) error
	// ReadFile returns the contents of a file inside a container.
	ReadFile(ctx context.Context, container, path string) ([]byte, error)
	// WriteFile puts a file inside a container, replacing any file already
	// there.
	WriteFile(ctx context.Context, container string, f FileSpec) error
}
