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
	// Stdin says whether tamp attached the caller's input to the command.
	Stdin bool
	// TTY and Size are the pseudo-terminal tamp asked for, if it asked.
	TTY  bool
	Size engine.ConsoleSize
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
	UpErr error
	// UpErrOnce fails only the next compose up, then clears itself. It is how a
	// test scripts a port that refuses the bind while the fallback attempt
	// succeeds.
	UpErrOnce  error
	StopErr    error
	RestartErr error
	DownErr    error

	// NetworkErr fails every network operation.
	NetworkErr error

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
	// Log is what every container's log says. One body for all of them is
	// enough: what a test asks is which container tamp read and how it
	// asked, not what Docker had stored.
	Log string
	// Logs records each log request tamp made, in order.
	LogReqs []engine.LogRequest

	// networks is the fake's set of Docker networks, each holding the names of
	// the containers attached to it. Compose makes and removes an
	// environment's network, and tamp attaches the router to it afterwards,
	// so the two have to be modelled together for either to be testable.
	networks map[string]map[string]bool

	// AppAliases maps a repository's URL-derived name to the app its code
	// actually declares, for the real-world case where the two differ —
	// frappe/health clones as healthcare. A get-app on a mapped name puts the
	// declared app on the bench, the way bench itself would.
	AppAliases map[string]string

	// sites is the set of sites on the fake's bench. tamp creates one and
	// then reads the bench back to find out what it has, so the two commands
	// have to be modelled together for either to be testable.
	sites map[string]bool

	// apps is the set of apps fetched onto the fake's bench, and siteApps what
	// each site has installed. Both are modelled for the same reason sites is:
	// tamp fetches an app and then reads the bench back to decide whether a
	// site may install it.
	apps     map[string]bool
	siteApps map[string][]string

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
	// Compose creates the project's network on the way up. tamp names an
	// environment's network after its project, which is what lets the fake
	// know it without being told.
	f.ensureNetwork(p.Name)
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
	return nil
}

func (f *Fake) Containers(_ context.Context, project string) ([]engine.Container, error) {
	f.Calls = append(f.Calls, "Containers")
	if f.PingErr != nil {
		// An engine tamp cannot reach cannot be asked what is running on it
		// either, and code that copes with one has to cope with the other.
		return nil, f.PingErr
	}

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

// Attached names the containers on a network, sorted. A network the fake never
// made has none, which is how a test asks whether a teardown took it away.
func (f *Fake) Attached(network string) []string {
	return slices.Sorted(maps.Keys(f.networks[network]))
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
	if strings.Contains(exec.Line(), "bench init") {
		f.put(frappe.CommonSiteConfigPath, BenchInitConfig)
	}
	return f.answerSiteCommand(exec, req.Stdout)
}

// answerSiteCommand keeps the fake's idea of what a bench holds — its sites,
// its apps, and which apps each site has — in step with the commands tamp
// runs, and answers the ones that ask.
//
// A recording alone would not do here: tamp fetches an app or creates a site
// and then reads the bench back to find out what it now holds, so a fake that
// forgot the write would let a broken round trip pass. Arguments travel beside
// the script rather than inside it, which is what makes the hostname and the
// app name readable at a fixed position.
func (f *Fake) answerSiteCommand(exec Exec, stdout io.Writer) error {
	switch {
	case strings.Contains(exec.Line(), "bench new-site"):
		f.addSite(siteArg(exec.Cmd, "new-site"))
	case strings.Contains(exec.Line(), "bench drop-site"):
		host := siteArg(exec.Cmd, "drop-site")
		delete(f.sites, host)
		delete(f.Files, frappe.SiteConfigPath(host))
	// The listing script names a site by the config file every site has.
	case strings.Contains(exec.Line(), "site_config.json"):
		for _, host := range f.Sites() {
			fmt.Fprintln(stdout, host)
		}
	case strings.Contains(exec.Line(), "bench get-app"):
		name := appNameFromSource(scriptArg(exec.Cmd, 0))
		if declared, ok := f.AppAliases[name]; ok {
			name = declared
		}
		f.AddApp(name)
	case strings.Contains(exec.Line(), "install-app"):
		host, app := scriptArg(exec.Cmd, 0), scriptArg(exec.Cmd, 1)
		if f.siteApps == nil {
			f.siteApps = map[string][]string{}
		}
		f.siteApps[host] = append(f.siteApps[host], app)
	case strings.Contains(exec.Line(), "list-apps"):
		// Every Frappe site has frappe installed; anything else got there
		// through an install-app above.
		fmt.Fprintln(stdout, "frappe")
		for _, app := range f.siteApps[siteArg(exec.Cmd, "--site")] {
			fmt.Fprintln(stdout, app)
		}
	case strings.Contains(exec.Line(), "cd "+frappe.AppsDir):
		for _, app := range f.Apps() {
			fmt.Fprintln(stdout, app)
		}
	}
	return nil
}

// Apps names the apps the fake's bench holds, sorted.
func (f *Fake) Apps() []string { return slices.Sorted(maps.Keys(f.apps)) }

// AddApp puts an app on the fake's bench without a fetch — the backdrop for a
// test whose subject is what tamp does with an app that is already there.
func (f *Fake) AddApp(name string) {
	if name == "" {
		return
	}
	if f.apps == nil {
		f.apps = map[string]bool{}
	}
	f.apps[name] = true
}

// SiteApps names what a site had installed on it, in the order tamp installed
// them.
func (f *Fake) SiteApps(host string) []string { return f.siteApps[host] }

// appNameFromSource is the app directory a clone URL produces, which is the
// last segment of its path.
func appNameFromSource(source string) string {
	source = strings.TrimSuffix(strings.TrimSuffix(source, "/"), ".git")
	if i := strings.LastIndexAny(source, "/:"); i >= 0 {
		source = source[i+1:]
	}
	return source
}

// scriptArg is the nth argument tamp passed beside a script it ran.
func scriptArg(cmd []string, n int) string {
	const firstScriptArg = 4 // bash -c <script> tamp <arg>...
	if len(cmd) > firstScriptArg+n {
		return cmd[firstScriptArg+n]
	}
	return ""
}

// siteArg is the hostname a bench site command was pointed at.
//
// It reads both spellings the fake sees. tamp's own scripts carry the
// hostname beside the script as its first argument; a user reaching the same
// bench command through 'tamp exec' types it straight after the subcommand,
// and a fake that only understood the first would make a site created that way
// invisible to the code that goes looking for it.
func siteArg(cmd []string, subcommand string) string {
	if len(cmd) > 0 && cmd[0] == "bash" {
		const firstScriptArg = 4 // bash -c <script> tamp <arg>
		if len(cmd) > firstScriptArg {
			return cmd[firstScriptArg]
		}
		return ""
	}
	for i, word := range cmd {
		if word == subcommand && i+1 < len(cmd) {
			return cmd[i+1]
		}
	}
	return ""
}

// Sites names the sites the fake's bench holds, sorted — what tamp created
// through it, less what tamp dropped.
func (f *Fake) Sites() []string { return slices.Sorted(maps.Keys(f.sites)) }

func (f *Fake) addSite(host string) {
	if host == "" {
		return
	}
	if f.sites == nil {
		f.sites = map[string]bool{}
	}
	f.sites[host] = true
	// Creating a site writes its own config, which is where the database name
	// Frappe invented is recorded — and the only place anything can read it.
	f.put(frappe.SiteConfigPath(host), fmt.Sprintf(`{"db_name": %q}`, "_"+strings.ReplaceAll(host, ".", "_")))
}

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

// Up puts a project's containers and network in place without a compose up.
// It is the backdrop for a test whose subject is what tamp does with
// something it finds already running.
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

// Down stops a project's containers without a compose stop, leaving them and
// the project's network in place — a machine on which something tamp started
// earlier is no longer running.
func (f *Fake) Down(project string) { f.setRunning(project, false) }

func (f *Fake) setRunning(project string, running bool) {
	if f.running == nil {
		f.running = map[string]bool{}
	}
	f.running[project] = running
}

// A fake that has drifted from the interface would silently stop testing the
// thing it stands in for.
var _ engine.Engine = (*Fake)(nil)
