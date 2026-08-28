// Package synctest provides the recording fake that stands in for Mutagen.
//
// Mutagen is tamp's second external process, after the container engine, and
// so its second fake point. Everything above it — when a session is made,
// paused, resumed or destroyed — is tamp's own logic, and this is what lets a
// test exercise it on a machine that has never had Mutagen on it.
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

// Fake is a scripted Mutagen. Set the answers, run the code under test, then
// assert on Calls and on the sessions it holds.
type Fake struct {
	// Installed is the binary Find reports. The zero value is a machine with
	// no Mutagen, which Ensure then downloads onto unless DownloadErr says the
	// download is blocked.
	Installed syncer.Binary

	// DownloadErr is the download that cannot happen — the offline machine
	// behind a proxy, whose environments fall back to a bind mount.
	DownloadErr error
	// SessionErr fails every session operation.
	SessionErr error

	// Calls names each operation tamp asked for, in order.
	Calls []string
	// Created records each session tamp made, in order.
	Created []syncer.Session

	// sessions is the fake's daemon: what it holds, and whether each is
	// paused. tamp pauses on stop and resumes on start, so the two have to be
	// modelled together for either to be testable.
	sessions map[string]bool
}

// Installed is a machine that already has the pinned Mutagen, which is every
// machine after the first sync tamp performs on it.
func Installed() *Fake {
	return &Fake{Installed: syncer.Binary{
		Path:    "/home/user/.tamp/bin/mutagen-" + syncer.Version + "/mutagen",
		Version: syncer.Version,
		Managed: true,
	}}
}

// Blocked is a machine that has no Mutagen and cannot get one — the offline or
// proxied case, where tamp warns loudly and falls back to a bind mount.
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
		// Real Mutagen narrates the first pass, and tamp streams that into
		// the create log; a silent fake would let a broken capture pass.
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

// Paused reports whether the fake holds a session by that name and it is
// paused. A session it does not hold is not paused — it is gone.
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

// A fake that has drifted from the interface would silently stop testing the
// thing it stands in for.
var _ syncer.Mutagen = (*Fake)(nil)
