// Package enginetest provides the recording fake that stands in for the
// container engine.
//
// The engine boundary is tamp's only fake point, so this is the only fake in
// the codebase. It records every request rather than just answering them,
// because a test that checks the output tamp printed still would not catch
// tamp asking the engine for the wrong thing.
package enginetest

import (
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/zhide915/tamp/internal/engine"
	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/frappe"
)

// DefaultServices are the compose services a tamp environment contains. The
// fake answers Containers from this list; a test in internal/env checks the
// generated compose file against it, so the two cannot drift apart silently.
var DefaultServices = []string{"frappe", "mariadb", "redis-cache", "redis-queue", "mailpit"}

// BenchInitConfig is the shared site config a real bench init leaves behind.
//
// The fake writes it whenever tamp runs a bench init, because tamp's next
// move is to read that file and merge its own keys into it. Against an empty
// container filesystem that merge would have nothing to preserve, and the one
// thing it must get right — not dropping what bench put there — would go
// untested.
const BenchInitConfig = `{
 "background_workers": 1,
 "db_host": "localhost",
 "frappe_user": "frappe",
 "live_reload": true,
 "redis_cache": "redis://localhost:13000",
 "shallow_clone": true
}`

// Op is one compose operation the fake was asked to perform, in full.
//
// Calls answers "which engine methods, in what order"; Op answers "on which
// project, from which file, taking what with it" — the questions that catch
// tamp acting on the wrong environment.
type Op struct {
	Method  string
	Project engine.ComposeProject
	// Removal is meaningful only on ComposeDown.
	Removal engine.Removal
}

// Exec is one command the fake was asked to run inside a container.
type Exec struct {
	Container string
	Cmd       []string
	Env       []string
	WorkDir   string
	User      string
}

// Line renders the exec the way a test asks about it: the whole command on one
// line, so a scripted shell is matched by what it says rather than by argv
// index.
func (e Exec) Line() string { return strings.Join(e.Cmd, " ") }

// Fake is a scripted Engine. Set the answers, run the code under test, then
// assert on Calls, Ops, Execs and Files.
type Fake struct {
	// Info is what Ping reports while PingErr is nil.
	Info    engine.Info
	PingErr error

	// Compose is what ComposeVersion reports while ComposeErr is nil.
	Compose    string
	ComposeErr error

	// UpErr, StopErr, RestartErr and DownErr script a compose operation that
	// fails — the mid-create failure whose rollback tamp has to get right.
	UpErr      error
	StopErr    error
	RestartErr error
	DownErr    error

	// ExecErr fails every in-container command. The fake otherwise answers
	// them all with success, which is what a bench container whose toolchain
	// is already provisioned looks like.
	ExecErr error
	// ExecFails scripts one particular command failing, keyed by a fragment of
	// its command line. It is how a test puts the failure in the middle of a
	// create — containers up, no bench yet — which is the rollback tamp has
	// the most to get wrong about.
	ExecFails map[string]error

	// Services are the containers a project has once it has been brought up.
	Services []string

	// Calls names each engine method tamp invoked, in order.
	Calls []string
	// Ops records each compose operation in full.
	Ops []Op
	// Execs records each command tamp ran inside a container, in order.
	Execs []Exec
	// Files is the fake's container filesystem, keyed by absolute path. It is
	// a real store rather than a recording: tamp reads back what it wrote —
	// bench's own config, merged with tamp's keys — and a write-only fake
	// would let that round trip pass untested.
	Files map[string]string
	// Volumes names each volume tamp asked to exist, in order.
	Volumes []string

	// running tracks, per project, whether its containers exist and whether
	// they are running — so that up, stop, down and Containers tell one
	// consistent story to the code under test.
	running map[string]bool
}

// Running is an engine that is up: a plausible Docker found by probing, and a
// compose v2 alongside it. It is the default backdrop for tests about
// something other than the engine being broken.
func Running() *Fake {
	return &Fake{
		Info: engine.Info{
			Address: engine.Address{
				Host:   "unix:///var/run/docker.sock",
				Source: engine.SourceProbe,
			},
			Version: "29.7.2",
		},
		Compose:  "2.39.1",
		Services: slices.Clone(DefaultServices),
		Files:    map[string]string{},
	}
}

// Unavailable is an engine tamp cannot reach at all — the exit-4 case.
func Unavailable() *Fake {
	unreachable := exitcode.New(exitcode.CodeEngineUnavailable,
		"no Docker engine found", "start Docker Desktop")
	return &Fake{
		PingErr: unreachable,
		ComposeErr: exitcode.New(exitcode.CodeEngineUnavailable,
			"'docker compose' is not available", "install Docker Desktop"),
		UpErr:      unreachable,
		StopErr:    unreachable,
		RestartErr: unreachable,
		DownErr:    unreachable,
		ExecErr:    unreachable,
		Services:   slices.Clone(DefaultServices),
		Files:      map[string]string{},
	}
}

func (f *Fake) Ping(context.Context) (engine.Info, error) {
	f.Calls = append(f.Calls, "Ping")
	if f.PingErr != nil {
		return engine.Info{}, f.PingErr
	}
	return f.Info, nil
}

func (f *Fake) ComposeVersion(context.Context) (string, error) {
	f.Calls = append(f.Calls, "ComposeVersion")
	if f.ComposeErr != nil {
		return "", f.ComposeErr
	}
	return f.Compose, nil
}

func (f *Fake) ComposeUp(_ context.Context, p engine.ComposeProject, out io.Writer) error {
	if err := f.record("ComposeUp", p, engine.KeepVolumes, out); err != nil {
		return err
	}
	if f.UpErr != nil {
		return f.UpErr
	}
	f.setRunning(p.Name, true)
	return nil
}

func (f *Fake) ComposeStop(_ context.Context, p engine.ComposeProject, out io.Writer) error {
	if err := f.record("ComposeStop", p, engine.KeepVolumes, out); err != nil {
		return err
	}
	if f.StopErr != nil {
		return f.StopErr
	}
	if _, exists := f.running[p.Name]; exists {
		f.setRunning(p.Name, false)
	}
	return nil
}

func (f *Fake) ComposeDown(_ context.Context, p engine.ComposeProject, removal engine.Removal, out io.Writer) error {
	if err := f.record("ComposeDown", p, removal, out); err != nil {
		return err
	}
	if f.DownErr != nil {
		return f.DownErr
	}
	delete(f.running, p.Name)
	return nil
}

func (f *Fake) Containers(_ context.Context, project string) ([]engine.Container, error) {
	f.Calls = append(f.Calls, "Containers")

	running, exists := f.running[project]
	if !exists {
		// A project that was never brought up, or that has been taken down,
		// has no containers. That is an answer, not a failure.
		return nil, nil
	}

	out := make([]engine.Container, 0, len(f.Services))
	for _, service := range f.Services {
		out = append(out, engine.Container{Service: service, Running: running})
	}
	return out, nil
}

func (f *Fake) ComposeRestart(_ context.Context, p engine.ComposeProject, service string, out io.Writer) error {
	if err := f.record("ComposeRestart", p, engine.KeepVolumes, out); err != nil {
		return err
	}
	if f.RestartErr != nil {
		return f.RestartErr
	}
	if _, exists := f.running[p.Name]; exists {
		f.setRunning(p.Name, true)
	}
	return nil
}

func (f *Fake) EnsureVolume(_ context.Context, name string) error {
	f.Calls = append(f.Calls, "EnsureVolume")
	f.Volumes = append(f.Volumes, name)
	return nil
}

func (f *Fake) Exec(_ context.Context, req engine.ExecRequest) error {
	f.Calls = append(f.Calls, "Exec")
	exec := Exec{
		Container: req.Container,
		Cmd:       slices.Clone(req.Cmd),
		Env:       slices.Clone(req.Env),
		WorkDir:   req.WorkDir,
		User:      req.User,
	}
	f.Execs = append(f.Execs, exec)
	if f.ExecErr != nil {
		return f.ExecErr
	}
	for fragment, err := range f.ExecFails {
		if strings.Contains(exec.Line(), fragment) {
			return err
		}
	}
	if strings.Contains(exec.Line(), "bench init") {
		f.put(frappe.CommonSiteConfigPath, BenchInitConfig)
	}
	return nil
}

func (f *Fake) ReadFile(_ context.Context, container, path string) ([]byte, error) {
	f.Calls = append(f.Calls, "ReadFile")
	body, ok := f.Files[path]
	if !ok {
		return nil, exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot read %s from %s: no such file", path, container),
			"check that the container is running")
	}
	return []byte(body), nil
}

func (f *Fake) WriteFile(_ context.Context, _ string, spec engine.FileSpec) error {
	f.Calls = append(f.Calls, "WriteFile")
	f.put(spec.Path, string(spec.Data))
	return nil
}

func (f *Fake) put(path, body string) {
	if f.Files == nil {
		f.Files = map[string]string{}
	}
	f.Files[path] = body
}

// Wrote returns what tamp put at path inside the container, and fails the
// test's assertion by returning false when nothing was written there.
func (f *Fake) Wrote(path string) (string, bool) {
	body, ok := f.Files[path]
	return body, ok
}

// Written lists every path tamp wrote into a container, sorted.
func (f *Fake) Written() []string { return slices.Sorted(maps.Keys(f.Files)) }

// Ran reports whether any command tamp ran inside a container contained
// fragment — the way a test asks "did tamp run bench init", without pinning
// every flag tamp passed alongside it.
func (f *Fake) Ran(fragment string) bool {
	for _, e := range f.Execs {
		if strings.Contains(e.Line(), fragment) {
			return true
		}
	}
	return false
}

// record logs the operation and enforces the one thing real compose would
// enforce for free: the generated file has to exist before tamp acts on it.
// Without this a test could not tell "tamp generated compose.yaml, then
// started it" from "tamp started something it never wrote".
func (f *Fake) record(method string, p engine.ComposeProject, removal engine.Removal, out io.Writer) error {
	f.Calls = append(f.Calls, method)
	f.Ops = append(f.Ops, Op{Method: method, Project: p, Removal: removal})

	if out != nil {
		// Real compose narrates what it is doing; tamp captures that stream
		// into create.log, and a silent fake would let a broken capture pass.
		fmt.Fprintf(out, "compose %s %s\n", method, p.Name)
	}

	if _, err := os.Stat(p.File); err != nil {
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("compose file %s is missing", p.File),
			"this is a tamp bug: the file should have been generated first")
	}
	return nil
}

func (f *Fake) setRunning(project string, running bool) {
	if f.running == nil {
		f.running = map[string]bool{}
	}
	f.running[project] = running
}

// A fake that has drifted from the interface would silently stop testing the
// thing it stands in for.
var _ engine.Engine = (*Fake)(nil)
