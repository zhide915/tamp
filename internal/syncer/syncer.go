// Package syncer is how an environment's source reaches its container.
//
// There are two answers and both are first class. On Linux the host's apps/
// directory is bind-mounted straight in, which is native speed and native
// inotify; on Windows and macOS a bind mount is neither, so the code lives in
// a container volume and Mutagen mirrors it to a real folder on the host for
// the agent to edit. "No sync session" is a mode, not a failure — every code
// path here has to treat it as one.
package syncer

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/zhide915/tamp/internal/exitcode"
)

// Mode is the value of [sync].mode in tamp.toml.
type Mode string

const (
	// ModeAuto lets tamp pick per operating system. It is the default and
	// what nearly every environment stays on.
	ModeAuto Mode = "auto"
	// ModeMutagen forces a sync session, even where a bind mount would do.
	ModeMutagen Mode = "mutagen"
	// ModeBind forces a bind mount. On Windows and macOS this is the degraded
	// fallback: it works, it is slow, and inotify does not fire — which means
	// no hot reload.
	ModeBind Mode = "bind"
	// ModeOff leaves the source in the container and puts nothing on the host.
	ModeOff Mode = "off"
)

// Modes lists every value of --sync, in the order the error message names them.
var Modes = []Mode{ModeAuto, ModeMutagen, ModeBind, ModeOff}

// ParseMode validates a --sync value.
func ParseMode(s string) (Mode, error) {
	for _, mode := range Modes {
		if Mode(s) == mode {
			return mode, nil
		}
	}
	return "", exitcode.New(exitcode.CodeFailed,
		fmt.Sprintf("%q is not a way of syncing source", s),
		"modes: "+strings.Join(ModeNames(), ", "))
}

// ModeNames lists the values --sync and [sync].mode accept.
func ModeNames() []string {
	names := make([]string, len(Modes))
	for i, mode := range Modes {
		names[i] = string(mode)
	}
	return names
}

// Effective is what a mode means on this machine: one of mutagen, bind or off,
// with auto already decided.
type Effective Mode

const (
	UseMutagen = Effective(ModeMutagen)
	UseBind    = Effective(ModeBind)
	UseOff     = Effective(ModeOff)
)

func (e Effective) String() string { return string(e) }

// Resolve settles what auto means on an operating system.
//
// Linux gets a bind mount because there a bind mount is the whole answer:
// the container reads the host's filesystem directly, at full speed, with
// inotify working. Windows and macOS get Mutagen because there it is not —
// bind-mounted host paths are slow enough to have made bench commands take
// hours, and file events do not arrive at all, so the dev server never
// reloads.
func Resolve(mode Mode, goos string) Effective {
	if mode != ModeAuto {
		return Effective(mode)
	}
	if goos == "linux" {
		return UseBind
	}
	return UseMutagen
}

// AppsDirName is the source layer's directory inside an environment. It is the
// one thing tamp puts on the host for a person or an agent to edit.
const AppsDirName = "apps"

// AppsDir is an environment's host-side source directory.
func AppsDir(envDir string) string { return filepath.Join(envDir, AppsDirName) }

// Ignores are the paths a sync session leaves alone.
//
// They are build outputs and caches: things each side regenerates for itself,
// and things whose contents differ legitimately between a Linux container and
// the host. Syncing them would be pure cost and, for the virtualenv, actively
// wrong — the container's Python is not the host's.
//
// .git is deliberately absent. It syncs, which is what lets git work on the
// host, and it is safe only because exactly one side ever writes to it.
var Ignores = []string{
	"env/",
	"node_modules/",
	"__pycache__/",
	"*.pyc",
	".mypy_cache/",
	"dist/",
	"*.egg-info/",
}

// Session is one environment's sync, described in full.
type Session struct {
	// Name identifies the session to Mutagen. It is the environment's own
	// resource name, so a session is traceable to the environment that owns it.
	Name string
	// Alpha is the host directory — the side that wins conflicts, because it
	// is the side a person or an agent is editing.
	Alpha string
	// Beta is the container endpoint, in Mutagen's docker transport form.
	Beta string
	// DockerHost is the engine endpoint Mutagen's docker transport should use.
	// It is passed rather than left to Mutagen's own detection so that the
	// session acts on the same Docker tamp does.
	DockerHost string
}

// BetaURL is the Mutagen endpoint for a bench's apps directory inside its
// container.
func BetaURL(user, container, appsDir string) string {
	return fmt.Sprintf("docker://%s@%s%s", user, container, appsDir)
}

// Binary is the Mutagen tamp drives, and where it came from.
type Binary struct {
	Path string
	// Version is the release, without the leading v.
	Version string
	// Managed says tamp downloaded this one itself, rather than finding it on
	// the machine's PATH.
	Managed bool
}

// Mutagen is the sync agent tamp drives.
//
// It is an interface for the same reason the container engine is: everything
// above it — when a session is made, paused, resumed or destroyed — is tamp's
// own logic and has to be testable without a Mutagen binary on the machine.
type Mutagen interface {
	// Find reports the Mutagen already on this machine, and returns an error
	// carrying CodeNotFound when there is none. It downloads nothing: it is
	// what a diagnosis asks.
	Find(ctx context.Context) (Binary, error)
	// Ensure reports the Mutagen tamp will drive, downloading the pinned
	// release if the machine has none.
	Ensure(ctx context.Context) (Binary, error)

	// Create starts a two-way session and waits for its first full pass, so
	// that a command which returns has actually put the source on the host.
	Create(ctx context.Context, s Session, out io.Writer) error
	// Pause stops a session without forgetting it.
	Pause(ctx context.Context, name string) error
	// Resume restarts a paused session.
	Resume(ctx context.Context, name string) error
	// Terminate forgets a session for good.
	Terminate(ctx context.Context, name string) error
	// Sessions names the sessions Mutagen currently holds for tamp.
	Sessions(ctx context.Context) ([]string, error)
}

// cloudFolders are the directory names that mean a third party is also syncing
// this path. Matching is on a whole path segment, case-folded.
var cloudFolders = []string{"onedrive", "dropbox", "google drive", "googledrive"}

// PathWarnings reports what about an environment's location will bite later.
//
// Both are warnings rather than refusals: they describe a setup that works
// until it does not, and where the environment goes is the user's call. A
// cloud-sync folder means two synchronizers writing the same files, each
// undoing the other; a space in the path is a quoting bug waiting somewhere in
// the chain of shells a bench command passes through.
func PathWarnings(dir string) []string {
	var warnings []string
	for _, segment := range strings.Split(filepath.ToSlash(dir), "/") {
		folded := strings.ToLower(segment)
		for _, cloud := range cloudFolders {
			if folded == cloud {
				warnings = append(warnings,
					fmt.Sprintf("%s is inside %s, which syncs these files too — two synchronizers on one directory undo each other", dir, segment))
				break
			}
		}
	}
	if strings.Contains(dir, " ") {
		warnings = append(warnings,
			fmt.Sprintf("%s has a space in it, which some of the tools a bench command passes through quote badly", dir))
	}
	return warnings
}
