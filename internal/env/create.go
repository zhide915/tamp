package env

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/zhide915/tamp/internal/engine"
	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/ui"
)

// CreateLogFile records what happened during a create. It is left behind by a
// create that failed, next to the tamp.toml, so the user can see how far
// tamp got and what the engine said.
const CreateLogFile = "create.log"

// createSteps is how many numbered steps a create prints. It grows as tamp
// learns to do more at create time — a bench, a toolchain, a router.
const createSteps = 4

// CreateRequest is what `tamp create` was asked for.
type CreateRequest struct {
	// Name is the environment name, still unvalidated.
	Name string
	// Parent is the directory <name>/ is created inside. Empty means the
	// directory tamp was run in (there is no mandatory root).
	Parent string
	// Frappe is the --frappe value, still unvalidated.
	Frappe string
}

// Create provisions a new environment and brings its containers up.
//
// Everything it makes outside the environment directory — the registry entry,
// the containers, the volumes — is undone if any step fails; the directory
// itself is left with tamp.toml and create.log, because the one thing tamp
// must never destroy is a directory the user might have put something in.
func (m *Manager) Create(ctx context.Context, req CreateRequest) error {
	name, err := ParseName(req.Name)
	if err != nil {
		return err
	}
	version, toolchain, err := ParseFrappeVersion(req.Frappe)
	if err != nil {
		return err
	}
	dir, err := m.createDir(req, name)
	if err != nil {
		return err
	}

	log := &createLog{out: m.Out, total: createSteps}
	defer log.save(dir)

	log.step("checking Docker")
	if _, err := m.Engine.Ping(ctx); err != nil {
		return err
	}

	log.step(fmt.Sprintf("resolving the Frappe %s toolchain", version))
	log.note(fmt.Sprintf("python %s · node %s · mariadb %s",
		toolchain.Python, toolchain.Node, toolchain.MariaDB))

	log.step("writing the environment")
	e, err := m.provision(dir, name, version, toolchain)
	if err != nil {
		return err
	}

	log.step("starting containers")
	if err := m.Engine.ComposeUp(ctx, e.project(), log.stream()); err != nil {
		m.rollback(ctx, e, log)
		return err
	}

	m.Out.OK(fmt.Sprintf("%s ready — no sites yet", name))
	m.Out.Hint("next: tamp site new <host>")
	return nil
}

// createDir settles where the environment goes and refuses to build on top of
// anything that is already there.
func (m *Manager) createDir(req CreateRequest, name Name) (string, error) {
	parent := req.Parent
	if parent == "" {
		parent = m.Cwd
	}
	dir, err := filepath.Abs(filepath.Join(parent, string(name)))
	if err != nil {
		return "", exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot resolve %q to an absolute path: %v", parent, err),
			"pass --dir a directory that exists")
	}

	if _, err := os.Stat(dir); err == nil {
		return "", exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("%s already exists", dir),
			"pick another name, or create the environment somewhere else with --dir")
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot look at %s: %v", dir, err),
			"check the permissions on the parent directory")
	}
	return dir, nil
}

// provision claims the name, writes the environment's files, and returns it
// ready to start. Everything here is undoable by rollback.
func (m *Manager) provision(dir string, name Name, version FrappeVersion, tc Toolchain) (*Environment, error) {
	res, err := NewResources(name, dir)
	if err != nil {
		return nil, err
	}

	// Claiming the name and allocating the port happen in one pass under the
	// machine lock, and the port is recorded in the entry itself: both
	// facts are then on disk together when the lock is released, so a second
	// create cannot see the name free or reuse the port.
	var port int
	err = UpdateRegistry(m.Home, func(reg Registry) error {
		if existing, taken := reg[string(name)]; taken {
			return exitcode.New(exitcode.CodeFailed,
				fmt.Sprintf("an environment named %q is already registered, at %s", name, existing.Path),
				"pick another name, or remove the old one with 'tamp rm "+string(name)+"'")
		}
		port, err = AllocateDBPort(reg)
		if err != nil {
			return err
		}
		reg[string(name)] = Entry{Path: dir, Hash: res.Hash, DBPort: port, Sites: []string{}}
		return nil
	})
	if err != nil {
		return nil, err
	}

	e := &Environment{
		Dir:       dir,
		Config:    NewConfig(name, version, tc, port),
		Resources: res,
	}
	if err := m.writeEnvironment(e); err != nil {
		m.unregister(name)
		return nil, err
	}
	return e, nil
}

// writeEnvironment lays down the directory and everything in it.
func (m *Manager) writeEnvironment(e *Environment) error {
	if err := os.MkdirAll(StateDir(e.Dir), 0o755); err != nil {
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot create %s: %v", StateDir(e.Dir), err),
			"check the permissions on the parent directory")
	}
	if err := e.Config.Save(ConfigPath(e.Dir)); err != nil {
		return err
	}
	if err := WriteGitignore(e.Dir); err != nil {
		return err
	}
	if err := EnsureDBRootPassword(e.Dir); err != nil {
		return err
	}
	return e.Generate()
}

// rollback undoes what a failed create put outside the environment directory.
//
// Its own failures are reported and then dropped: the user is about to be
// told why the create failed, and burying that under "and the cleanup failed
// too" would replace the actionable error with a less useful one.
func (m *Manager) rollback(ctx context.Context, e *Environment, log *createLog) {
	m.Out.Warn(fmt.Sprintf("create failed — rolling back %s", e.Name()))

	if err := m.Engine.ComposeDown(ctx, e.project(), engine.RemoveVolumes, log.stream()); err != nil {
		m.Out.Warn(fmt.Sprintf("could not remove the containers: %v", err))
		m.Out.Warn(fmt.Sprintf("remove them by hand with: docker compose -p %s down --volumes", e.Resources.Project()))
	}
	m.unregister(e.Name())

	m.Out.Note(fmt.Sprintf("%s was left in place, with %s and %s",
		e.Dir, ConfigFile, filepath.Join(StateDirName, CreateLogFile)))
	m.Out.Note("delete the directory to try again")
}

func (m *Manager) unregister(name Name) {
	err := UpdateRegistry(m.Home, func(reg Registry) error {
		delete(reg, string(name))
		return nil
	})
	if err != nil {
		m.Out.Warn(fmt.Sprintf("could not remove %q from the registry: %v", name, err))
	}
}

// createLog narrates a create to the terminal and, at the same time, into the
// buffer that becomes create.log.
//
// It is a buffer rather than an open file because the environment directory
// does not exist yet when the first step is printed, and a create that fails
// before the directory exists has nowhere to write anyway.
type createLog struct {
	buf   bytes.Buffer
	out   *ui.Printer
	n     int
	total int
}

func (l *createLog) step(msg string) {
	l.n++
	l.out.Step(l.n, l.total, msg)
	fmt.Fprintf(&l.buf, "[%d/%d] %s\n", l.n, l.total, msg)
}

func (l *createLog) note(msg string) {
	l.out.Note(msg)
	fmt.Fprintln(&l.buf, msg)
}

// stream is where the engine's own output goes: to the terminal as it happens,
// and into the log so a failed create can be read afterwards.
func (l *createLog) stream() io.Writer {
	return io.MultiWriter(l.out.Stream(), &l.buf)
}

// save writes the log into an environment directory that already exists.
//
// It never creates that directory: a create rejected before it got that far —
// a name already registered, an unreachable engine — must leave nothing behind
// at all, and a log of a create that made nothing is not worth a directory.
//
// A failure here is silent on purpose: it happens while tamp is already
// reporting something the user cares about more.
func (l *createLog) save(dir string) {
	if _, err := os.Stat(dir); err != nil {
		return
	}
	if err := os.MkdirAll(StateDir(dir), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(StateDir(dir), CreateLogFile), l.buf.Bytes(), 0o644)
}
