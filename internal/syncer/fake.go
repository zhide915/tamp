package syncer

import (
	"context"
	"fmt"
	"slices"

	"github.com/zhide915/tamp/internal/exitcode"
)

// Fake is the recording Client for tests. It models each session's state,
// but syncs no files.
type Fake struct {
	// InstallErr is Install's answer, and CreateErr Create's.
	InstallErr, CreateErr error
	// Err fails every call that acts on or reads a session, as a Mutagen that
	// cannot run would.
	Err error
	// OnCall runs at each call, named as in Calls, before it acts: how a test
	// sees what tamp had done by then.
	OnCall func(call string)
	// NoDaemon makes Peek find no Mutagen daemon running.
	NoDaemon bool

	// Calls names each method tamp called, with the session's name.
	Calls []string
	// Creates records each session tamp asked for.
	Creates []Spec

	installed bool
	sessions  map[string]*Session
}

func (f *Fake) Installed(context.Context) (bool, error) {
	f.call("Installed")
	return f.installed, nil
}

func (f *Fake) Install(context.Context) error {
	f.call("Install")
	if f.InstallErr != nil {
		return f.InstallErr
	}
	f.installed = true
	return nil
}

func (f *Fake) Session(_ context.Context, name string) (*Session, error) {
	f.call("Session " + name)
	return f.read(name)
}

func (f *Fake) read(name string) (*Session, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	s, ok := f.sessions[name]
	if !ok {
		return nil, nil
	}
	got := *s
	got.Conflicts = slices.Clone(s.Conflicts)
	// Mutagen reports a paused session as disconnected, even a halted one.
	if got.Paused {
		got.Status = "disconnected"
	}
	return &got, nil
}

// Peek reads a session as Session does, unless NoDaemon is set.
func (f *Fake) Peek(_ context.Context, name string) (*Session, error) {
	f.call("Peek " + name)
	if f.NoDaemon && f.Err == nil {
		return nil, ErrNoDaemon
	}
	return f.read(name)
}

// Replicate records the copy; the fake syncs no files.
func (f *Fake) Replicate(_ context.Context, s Spec) error {
	f.call("Replicate " + s.Name)
	return f.Err
}

func (f *Fake) Create(_ context.Context, s Spec) error {
	f.call("Create " + s.Name)
	if f.Err != nil {
		return f.Err
	}
	f.Creates = append(f.Creates, s)
	if f.CreateErr != nil {
		return f.CreateErr
	}
	if _, ok := f.sessions[s.Name]; ok {
		return fmt.Errorf("a session named %s exists", s.Name)
	}
	f.SetSession(Session{Name: s.Name, Host: s.Host, Container: s.Container, Path: s.Path, Status: "watching"})
	return nil
}

func (f *Fake) Resume(_ context.Context, name string) error {
	f.call("Resume " + name)
	if f.Err != nil {
		return f.Err
	}
	s, err := f.session(name)
	if err != nil {
		return err
	}
	s.Paused = false
	if !s.Halted() {
		s.Status = "watching"
	}
	return nil
}

// Flush fails, as Mutagen's does, on a paused or halted session.
func (f *Fake) Flush(_ context.Context, name string) error {
	f.call("Flush " + name)
	if f.Err != nil {
		return f.Err
	}
	s, err := f.session(name)
	if err != nil {
		return err
	}
	if s.Paused || s.Halted() {
		return exitcode.New(exitcode.CodeFailed,
			"Mutagen cannot finish syncing: session is not currently able to synchronize", mutagenFix)
	}
	return nil
}

func (f *Fake) Pause(_ context.Context, name string) error {
	f.call("Pause " + name)
	if f.Err != nil {
		return f.Err
	}
	s, err := f.session(name)
	if err != nil {
		return err
	}
	s.Paused = true
	return nil
}

func (f *Fake) Terminate(_ context.Context, name string) error {
	f.call("Terminate " + name)
	if f.Err != nil {
		return f.Err
	}
	if _, err := f.session(name); err != nil {
		return err
	}
	delete(f.sessions, name)
	return nil
}

// SetInstalled places or takes away the installed Mutagen.
func (f *Fake) SetInstalled(installed bool) { f.installed = installed }

// SetSession places a session. Session reports a paused one as Mutagen does.
func (f *Fake) SetSession(s Session) {
	if f.sessions == nil {
		f.sessions = map[string]*Session{}
	}
	f.sessions[s.Name] = &s
}

// Names lists the sessions that exist, sorted.
func (f *Fake) Names() []string {
	var names []string
	for name := range f.sessions {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func (f *Fake) session(name string) (*Session, error) {
	s, ok := f.sessions[name]
	if !ok {
		return nil, fmt.Errorf("no session named %s", name)
	}
	return s, nil
}

func (f *Fake) call(call string) {
	f.Calls = append(f.Calls, call)
	if f.OnCall != nil {
		f.OnCall(call)
	}
}
