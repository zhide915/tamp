// Package syncer is tamp's boundary with Mutagen: the pinned download and
// the sync session of an environment's apps/ folder on Windows and macOS.
// Tests replace it with Fake.
package syncer

import (
	"context"
	"fmt"
	"strings"
)

// Client is all that tamp asks of Mutagen. A Mutagen binary that does not
// start fails a method with a *RunError.
type Client interface {
	// Installed reports whether the pinned Mutagen is on this machine and runs.
	Installed(ctx context.Context) (bool, error)
	// Install downloads the pinned Mutagen, verifies its checksum, and
	// replaces a copy that does not run.
	Install(ctx context.Context) error

	// Session returns the named session, or nil when there is none. It
	// never downloads Mutagen: without it there is no session.
	Session(ctx context.Context, name string) (*Session, error)
	// Replicate makes the container's folder match the host's, deleting what the
	// host lacks except ignored files, so a new session brings back nothing
	// the host deleted.
	Replicate(ctx context.Context, s Spec) error
	// Create creates a session and connects both sides.
	Create(ctx context.Context, s Spec) error
	// Resume connects a paused or disconnected session.
	Resume(ctx context.Context, name string) error
	// Flush waits for a full sync of both sides. It fails on a session that
	// cannot sync, such as a halted one.
	Flush(ctx context.Context, name string) error
	// Pause stops a session from syncing until Resume.
	Pause(ctx context.Context, name string) error
	// Terminate deletes a session. Neither side's files change.
	Terminate(ctx context.Context, name string) error
}

// Spec is the session tamp wants: two-way, with the host winning conflicts.
type Spec struct {
	Name string
	// Host is the folder on the host, which wins every conflict.
	Host string
	// Container and Path are the folder in a running container.
	Container, Path string
	// Ignore holds the paths neither side sends, in gitignore syntax.
	Ignore []string
}

// Session is one session as Mutagen reports it.
type Session struct {
	Name            string
	Host            string
	Container, Path string
	Paused          bool
	// Status is Mutagen's, such as "watching" or "halted-on-root-deletion".
	Status    string
	Conflicts []Conflict
}

// Conflict is a change the sync cannot settle: the host wins every other
// conflict, but neither side deletes files the other ignores.
type Conflict struct {
	// Path is relative to the synced folder, in slash form; "" is the folder.
	Path string
	// HostDeleted tells that the host deleted Path.
	HostDeleted bool
}

// Halted reports whether Mutagen stopped the session for its safety, such as
// when one side's folder was deleted. Only a new session syncs again.
func (s Session) Halted() bool { return strings.HasPrefix(s.Status, "halted") }

// ForPlatform is the sync client for an OS: Mutagen on Windows and macOS, and
// nil on Linux, where apps/ is bind-mounted.
func ForPlatform(goos string) Client {
	switch goos {
	case "windows", "darwin":
		return &Mutagen{}
	}
	return nil
}

// RunError is a Mutagen binary that does not start, such as a corrupt or
// quarantined one.
type RunError struct {
	Bin string
	Err error
}

func (e *RunError) Error() string { return fmt.Sprintf("Mutagen cannot run: %v", e.Err) }
func (e *RunError) Unwrap() error { return e.Err }

// ReplicaName is the session that Replicate runs for the session name, which a
// killed run can leave behind. Its single `-` before "replica" keeps it apart
// from every name tamp gives a two-way session.
func ReplicaName(name string) string { return name + "-replica" }

// mutagenFix is the fix for a Mutagen command that ran and failed: Mutagen
// reaches the container through the docker command.
const mutagenFix = "fix what Mutagen says, and check that `docker ps` works in this shell"
