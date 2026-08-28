// Package syncer moves an environment's source between host and container.
// Linux bind-mounts the host's apps/ directory into the container; Windows
// and macOS get a Mutagen session instead, because bind mounts there are slow
// and deliver no inotify events. Having no sync session is a mode, not an
// error.
package syncer

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/zhide915/tamp/internal/exitcode"
)

// Mode is a [sync].mode / --sync value.
type Mode string

const (
	// ModeAuto picks per operating system; the default.
	ModeAuto Mode = "auto"
	// ModeMutagen forces a sync session.
	ModeMutagen Mode = "mutagen"
	// ModeBind forces a bind mount. On Windows and macOS it is slow and gets
	// no file events, so no hot reload.
	ModeBind Mode = "bind"
	// ModeOff keeps the source container-only.
	ModeOff Mode = "off"
)

// Modes holds every mode, in the order error messages list them.
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

// ModeNames lists the strings --sync and [sync].mode accept.
func ModeNames() []string {
	names := make([]string, len(Modes))
	for i, mode := range Modes {
		names[i] = string(mode)
	}
	return names
}

// Effective is a Mode with auto already resolved: mutagen, bind, or off.
type Effective Mode

// All three are working environments; none may be treated as a failure of
// another.
const (
	UseMutagen = Effective(ModeMutagen)
	UseBind    = Effective(ModeBind)
	UseOff     = Effective(ModeOff)
)

func (e Effective) String() string { return string(e) }

// Resolve decides what auto means on goos: bind on Linux, where it is native
// speed with working inotify; Mutagen elsewhere, where bind mounts are slow
// and file events never arrive.
func Resolve(mode Mode, goos string) Effective {
	if mode != ModeAuto {
		return Effective(mode)
	}
	if goos == "linux" {
		return UseBind
	}
	return UseMutagen
}

// AppsDirName is the one directory tamp puts on the host for editing.
const AppsDirName = "apps"

// AppsDir is an environment's host-side source directory.
func AppsDir(envDir string) string { return filepath.Join(envDir, AppsDirName) }

// Ignores are the paths a sync session skips: build outputs and caches each
// side regenerates, and the virtualenv, whose contents are platform-specific.
// .git deliberately syncs — that is what lets git run on the host, and it is
// safe because only one side ever writes it.
var Ignores = []string{
	"env/",
	"node_modules/",
	"__pycache__/",
	"*.pyc",
	".mypy_cache/",
	"dist/",
	"*.egg-info/",
}

// Session describes one environment's sync.
type Session struct {
	// Name is the environment's own resource name, so sessions trace back to
	// their environment.
	Name string
	// Alpha is the host directory. It wins conflicts, being the edited side.
	Alpha string
	// Beta is the container endpoint in Mutagen's docker transport form.
	Beta string
	// DockerHost pins Mutagen's docker transport to the engine tamp resolved,
	// not whatever Mutagen would detect.
	DockerHost string
}

// BetaURL is the Mutagen docker endpoint for a bench's in-container apps
// directory.
func BetaURL(user, container, appsDir string) string {
	return fmt.Sprintf("docker://%s@%s%s", user, container, appsDir)
}

// Binary is a Mutagen executable and its provenance.
type Binary struct {
	Path string
	// Version has no leading v.
	Version string
	// Managed marks a binary tamp downloaded, as opposed to one found on PATH.
	Managed bool
}

// Mutagen abstracts the Mutagen binary so session lifecycle logic is testable
// on machines without one.
type Mutagen interface {
	// Find reports an existing Mutagen without downloading anything, with
	// CodeNotFound when there is none.
	Find(ctx context.Context) (Binary, error)
	// Ensure returns a usable Mutagen, downloading the pinned release if
	// needed.
	Ensure(ctx context.Context) (Binary, error)

	// Create starts a two-way session and waits for its first full pass, so
	// a successful return means the source is on the host.
	Create(ctx context.Context, s Session, out io.Writer) error
	Pause(ctx context.Context, name string) error
	Resume(ctx context.Context, name string) error
	Terminate(ctx context.Context, name string) error
	Sessions(ctx context.Context) ([]string, error)
}

// cloudFolders are path segments (matched whole, case-folded) that mean a
// third-party synchronizer also owns the tree.
var cloudFolders = []string{"onedrive", "dropbox", "google drive", "googledrive"}

// PathWarnings flags locations that will cause trouble later: a cloud-sync
// folder means two synchronizers undoing each other, and a space in the path
// trips quoting somewhere in the shells a bench command crosses. Warnings,
// not refusals — where the environment lives is the user's call.
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
