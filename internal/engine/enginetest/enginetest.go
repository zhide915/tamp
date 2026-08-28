// Package enginetest is the recording fake for the engine boundary — the
// one fake in the codebase, since the boundary is tamp's single fake point.
// It records every request, not just the answers: checking tamp's output
// alone would miss tamp asking the engine for the wrong thing.
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

// DefaultServices are the compose services of a tamp environment. A test in
// internal/env checks the generated compose file against this list, so the
// two cannot drift apart silently.
var DefaultServices = []string{"frappe", "mariadb", "redis-cache", "redis-queue", "mailpit"}

// BenchInitConfig is the shared site config a real bench init leaves
// behind. The fake writes it on bench init so the merge tamp performs next
// has real bench keys to preserve.
const BenchInitConfig = `{
 "background_workers": 1,
 "db_host": "localhost",
 "frappe_user": "frappe",
 "live_reload": true,
 "redis_cache": "redis://localhost:13000",
 "shallow_clone": true
}`

// Op is one recorded compose operation: which project, from which file,
// with what removal — what catches tamp acting on the wrong environment.
type Op struct {
	Method  string
	Project engine.ComposeProject
	// Removal is meaningful only on ComposeDown.
	Removal engine.Removal
}

// Exec is one recorded in-container command.
type Exec struct {
	Container string
	Cmd       []string
	Env       []string
	WorkDir   string
	User      string
	// Stdin reports whether input was attached.
	Stdin bool
	TTY   bool
	Size  engine.ConsoleSize
}

// Line joins the command into one string, so tests match by content rather
// than argv index.
func (e Exec) Line() string { return strings.Join(e.Cmd, " ") }

// Fake is a scripted Engine: set the answers, run the code under test,
// assert on Calls, Ops, Execs and Files.
type Fake struct {
	// Info is Ping's answer while PingErr is nil.
	Info    engine.Info
	PingErr error

	// Compose is ComposeVersion's answer while ComposeErr is nil.
	Compose    string
	ComposeErr error

	UpErr error
	// UpErrOnce fails only the next compose up, then clears itself — e.g. a
	// refused port bind whose fallback attempt succeeds.
	UpErrOnce  error
	StopErr    error
	RestartErr error
	DownErr    error

	// NetworkErr fails every network operation.
	NetworkErr error

	// ExecErr fails every in-container command; otherwise all succeed, like
	// a fully provisioned bench container.
	ExecErr error
	// ExecFails fails one command, keyed by a fragment of its command line —
	// how a test plants a failure mid-create.
	ExecFails map[string]error

	// Services are the containers a brought-up project has.
	Services []string

	// Calls names each engine method invoked, in order.
	Calls []string
	// Ops records each compose operation in full.
	Ops []Op
	// Execs records each in-container command, in order.
	Execs []Exec
	// Files is the fake's container filesystem, keyed by absolute path. A
	// real store, not a recording: tamp reads back what it wrote, and a
	// write-only fake would let that round trip pass untested.
	Files map[string]string
	// Volumes names each volume asked to exist, in order.
	Volumes []string
	// Removed names each volume asked to be removed, in order.
	Removed []string
	// Log is every container's log body; tests care which container was
	// read and how, not what was stored.
	Log string
	// LogReqs records each log request, in order.
	LogReqs []engine.LogRequest

	// networks maps network name to attached containers. Compose creates
	// and removes an environment's network and tamp attaches the router to
	// it, so both must be modelled together.
	networks map[string]map[string]bool

	// AppAliases maps a repo's URL-derived name to the app it declares
	// (frappe/health clones as healthcare); get-app on a mapped name puts
	// the declared app on the bench, as bench itself would.
	AppAliases map[string]string

	// PrivateRepos maps an app source URL to the password that unlocks it:
	// git commands touching the source fail the way auth does unless the
	// exec's environment carries that password.
	PrivateRepos map[string]string
	// MissingRepos are source URLs no host serves: git commands touching
	// them fail the way a typo or a deleted repository does.
	MissingRepos map[string]bool

	// sim models the Frappe bench behind the engine, kept apart from the
	// recording so the two change for different reasons.
	sim *benchSim

	// running tracks per project whether containers exist and whether they
	// run, so up, stop, down and Containers stay consistent.
	running map[string]bool

	// projectVolumes tracks which projects have volumes: up creates them,
	// down removes them only when asked.
	projectVolumes map[string]bool
}

// Running is an engine that is up — probed Docker plus compose v2 — the
// default backdrop for tests not about a broken engine.
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
		NetworkErr: unreachable,
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
	if f.UpErrOnce != nil {
		err := f.UpErrOnce
		f.UpErrOnce = nil
		return err
	}
	if f.UpErr != nil {
		return f.UpErr
	}
	f.setRunning(p.Name, true)
	// Compose creates the project's network on the way up; tamp names the
	// network after the project, so the fake can infer it.
	f.ensureNetwork(p.Name)
	if f.projectVolumes == nil {
		f.projectVolumes = map[string]bool{}
	}
	f.projectVolumes[p.Name] = true
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
	delete(f.networks, p.Name)
	if removal == engine.RemoveVolumes {
		delete(f.projectVolumes, p.Name)
		// Everything this bench holds lives in the volumes just removed; a
		// fake that remembered it would let "the data is gone" pass untrue.
		// Only the bench tree: the shared volumes beside it — toolchain,
		// package caches, template store — are nobody's to destroy.
		f.bench().reset(p.Name)
		for path := range f.Files {
			if strings.HasPrefix(path, frappe.WorkspaceDir+"/") {
				delete(f.Files, path)
			}
		}
	}
	return nil
}

func (f *Fake) Containers(_ context.Context, project string) ([]engine.Container, error) {
	f.Calls = append(f.Calls, "Containers")
	if f.PingErr != nil {
		// An unreachable engine cannot be asked what runs on it either.
		return nil, f.PingErr
	}

	running, exists := f.running[project]
	if !exists {
		// Never brought up, or taken down: no containers is an answer, not
		// a failure.
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

func (f *Fake) InspectNetwork(_ context.Context, name string) (*engine.Network, error) {
	f.Calls = append(f.Calls, "InspectNetwork")
	if f.NetworkErr != nil {
		return nil, f.NetworkErr
	}
	attached, exists := f.networks[name]
	if !exists {
		return nil, nil
	}
	return &engine.Network{Name: name, Containers: slices.Sorted(maps.Keys(attached))}, nil
}

func (f *Fake) ConnectNetwork(_ context.Context, network, container string) error {
	f.Calls = append(f.Calls, "ConnectNetwork")
	if f.NetworkErr != nil {
		return f.NetworkErr
	}
	attached, exists := f.networks[network]
	if !exists {
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot attach %s to the network %s: no such network", container, network),
			"this is a tamp bug: the network should exist before anything joins it")
	}
	attached[container] = true
	return nil
}

func (f *Fake) DisconnectNetwork(_ context.Context, network, container string) error {
	f.Calls = append(f.Calls, "DisconnectNetwork")
	if f.NetworkErr != nil {
		return f.NetworkErr
	}
	delete(f.networks[network], container)
	return nil
}

// Attached names the containers on a network, sorted; a network the fake
// never made has none.
func (f *Fake) Attached(network string) []string {
	return slices.Sorted(maps.Keys(f.networks[network]))
}

func (f *Fake) EnsureVolume(_ context.Context, name string) error {
	f.Calls = append(f.Calls, "EnsureVolume")
	f.Volumes = append(f.Volumes, name)
	return nil
}

func (f *Fake) RemoveVolume(_ context.Context, name string) error {
	f.Calls = append(f.Calls, "RemoveVolume")
	f.Removed = append(f.Removed, name)
	return nil
}

func (f *Fake) HasVolumes(_ context.Context, project string) (bool, error) {
	f.Calls = append(f.Calls, "HasVolumes")
	if f.PingErr != nil {
		return false, f.PingErr
	}
	return f.projectVolumes[project], nil
}

func (f *Fake) Exec(_ context.Context, req engine.ExecRequest) error {
	f.Calls = append(f.Calls, "Exec")
	exec := Exec{
		Container: req.Container,
		Cmd:       slices.Clone(req.Cmd),
		Env:       slices.Clone(req.Env),
		WorkDir:   req.WorkDir,
		User:      req.User,
		Stdin:     req.Stdin != nil,
		TTY:       req.TTY,
		Size:      req.Size,
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
	return f.bench().answer(exec, req.Stdout, req.Stderr)
}

// bench lazily builds the bench model, so the zero Fake works.
func (f *Fake) bench() *benchSim {
	if f.sim == nil {
		f.sim = &benchSim{
			aliases: func() map[string]string { return f.AppAliases },
			private: func() map[string]string { return f.PrivateRepos },
			missing: func() map[string]bool { return f.MissingRepos },
			put:     f.put,
			drop:    func(path string) { delete(f.Files, path) },
			has: func(path string) bool {
				_, ok := f.Files[path]
				return ok
			},
		}
	}
	return f.sim
}

// Apps names the apps on every bench, sorted.
func (f *Fake) Apps() []string { return f.bench().allApps() }

// AddApp puts an app on one container's bench without a fetch, as backdrop
// for tests about apps that are already there.
func (f *Fake) AddApp(container, name string) { f.bench().at(container).addApp(name) }

// SiteApps names what a site had installed, in install order.
func (f *Fake) SiteApps(host string) []string { return f.bench().siteAppsOf(host) }

// Sites names every bench's sites, sorted.
func (f *Fake) Sites() []string { return f.bench().allSites() }

func (f *Fake) Logs(_ context.Context, req engine.LogRequest) error {
	f.Calls = append(f.Calls, "Logs")
	f.LogReqs = append(f.LogReqs, req)
	if f.ExecErr != nil {
		return f.ExecErr
	}
	if req.Stdout != nil {
		_, _ = io.WriteString(req.Stdout, f.Log)
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

// Wrote returns what was written at path; false when nothing was.
func (f *Fake) Wrote(path string) (string, bool) {
	body, ok := f.Files[path]
	return body, ok
}

// Written lists every written path, sorted.
func (f *Fake) Written() []string { return slices.Sorted(maps.Keys(f.Files)) }

// Ran reports whether any executed command line contained fragment — "did
// tamp run bench init" without pinning every flag.
func (f *Fake) Ran(fragment string) bool {
	for _, e := range f.Execs {
		if strings.Contains(e.Line(), fragment) {
			return true
		}
	}
	return false
}

// record logs the operation and enforces what real compose would for free:
// the generated file must exist before it is acted on.
func (f *Fake) record(method string, p engine.ComposeProject, removal engine.Removal, out io.Writer) error {
	f.Calls = append(f.Calls, method)
	f.Ops = append(f.Ops, Op{Method: method, Project: p, Removal: removal})

	if out != nil {
		// Real compose narrates; a silent fake would let a broken capture of
		// that stream pass.
		fmt.Fprintf(out, "compose %s %s\n", method, p.Name)
	}

	if _, err := os.Stat(p.File); err != nil {
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("compose file %s is missing", p.File),
			"this is a tamp bug: the file should have been generated first")
	}
	return nil
}

// Up puts a project's containers and network in place without a compose up
// — backdrop for tests about something found already running.
func (f *Fake) Up(project string) {
	f.setRunning(project, true)
	f.ensureNetwork(project)
}

func (f *Fake) ensureNetwork(name string) {
	if f.networks == nil {
		f.networks = map[string]map[string]bool{}
	}
	if _, exists := f.networks[name]; !exists {
		f.networks[name] = map[string]bool{}
	}
}

// Down marks a project stopped without a compose stop, containers and
// network left in place.
func (f *Fake) Down(project string) { f.setRunning(project, false) }

func (f *Fake) setRunning(project string, running bool) {
	if f.running == nil {
		f.running = map[string]bool{}
	}
	f.running[project] = running
}

var _ engine.Engine = (*Fake)(nil)
