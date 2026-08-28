// Package synctest fakes the Mutagen interface, so session lifecycle logic —
// tamp's own — is testable on machines with no Mutagen at all.
package synctest

import (
	"context"
	"fmt"
	"io"
	"maps"
	"slices"

	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/syncer"
)

// Fake is a scripted Mutagen: set the answers, run the code under test, then
// assert on Calls and the sessions it holds.
type Fake struct {
	// Installed is what Find reports. The zero value is a machine with no
	// Mutagen; Ensure then downloads unless DownloadErr is set.
	Installed syncer.Binary

	// DownloadErr fails Ensure's download.
	DownloadErr error
	// SessionErr fails every session operation.
	SessionErr error

	Calls   []string
	Created []syncer.Session

	// sessions maps name to paused. Pause and resume are modelled together
	// because tamp pauses on stop and resumes on start.
	sessions map[string]bool
}

// Installed is a machine that already has the pinned Mutagen.
func Installed() *Fake {
	return &Fake{Installed: syncer.Binary{
		Path:    "/home/user/.tamp/bin/mutagen-" + syncer.Version + "/mutagen",
		Version: syncer.Version,
		Managed: true,
	}}
}

// Blocked is a machine with no Mutagen that cannot download one — the
// offline or proxied case.
func Blocked() *Fake {
	return &Fake{DownloadErr: exitcode.New(exitcode.CodeFailed,
		"cannot download mutagen: no route to host",
		"check this machine's internet connection")}
}

func (f *Fake) Find(context.Context) (syncer.Binary, error) {
	f.Calls = append(f.Calls, "Find")
	if f.Installed.Path == "" {
		return syncer.Binary{}, exitcode.New(exitcode.CodeNotFound,
			"this machine has no Mutagen "+syncer.Version,
			"tamp downloads it the first time it syncs an environment")
	}
	return f.Installed, nil
}

func (f *Fake) Ensure(ctx context.Context) (syncer.Binary, error) {
	f.Calls = append(f.Calls, "Ensure")
	if f.Installed.Path != "" {
		return f.Installed, nil
	}
	if f.DownloadErr != nil {
		return syncer.Binary{}, f.DownloadErr
	}
	f.Installed = Installed().Installed
	return f.Installed, nil
}

func (f *Fake) Create(_ context.Context, s syncer.Session, out io.Writer) error {
	f.Calls = append(f.Calls, "Create")
	if f.SessionErr != nil {
		return f.SessionErr
	}
	f.Created = append(f.Created, s)
	f.set(s.Name, false)
	if out != nil {
		// Real Mutagen narrates the first pass; staying silent would let a
		// broken output capture pass.
		fmt.Fprintf(out, "mutagen created %s\n", s.Name)
	}
	return nil
}

func (f *Fake) Pause(_ context.Context, name string) error {
	f.Calls = append(f.Calls, "Pause")
	if f.SessionErr != nil {
		return f.SessionErr
	}
	if _, held := f.sessions[name]; held {
		f.set(name, true)
	}
	return nil
}

func (f *Fake) Resume(_ context.Context, name string) error {
	f.Calls = append(f.Calls, "Resume")
	if f.SessionErr != nil {
		return f.SessionErr
	}
	if _, held := f.sessions[name]; held {
		f.set(name, false)
	}
	return nil
}

func (f *Fake) Terminate(_ context.Context, name string) error {
	f.Calls = append(f.Calls, "Terminate")
	if f.SessionErr != nil {
		return f.SessionErr
	}
	delete(f.sessions, name)
	return nil
}

func (f *Fake) Sessions(context.Context) ([]string, error) {
	f.Calls = append(f.Calls, "Sessions")
	if f.SessionErr != nil {
		return nil, f.SessionErr
	}
	return slices.Sorted(maps.Keys(f.sessions)), nil
}

// Paused reports a session's paused state; held is false once terminated.
func (f *Fake) Paused(name string) (paused, held bool) {
	paused, held = f.sessions[name]
	return paused, held
}

func (f *Fake) set(name string, paused bool) {
	if f.sessions == nil {
		f.sessions = map[string]bool{}
	}
	f.sessions[name] = paused
}

var _ syncer.Mutagen = (*Fake)(nil)
