// Package router is tamp's single global Caddy container.
//
// There is exactly one on a machine, however many environments it holds, and
// it is the reason nothing tamp runs is ever reached by IP or port: every
// site and every mail UI is a hostname, routed by Host header to a container
// that publishes nothing. It owns one assembled Caddyfile, rebuilt from every
// environment's routes whenever any of them changes, and it is attached to
// each environment's private network so that it can reach inside.
//
// Like internal/env, it touches Docker only through an engine passed in, so
// the whole of it runs in a test against a temp home and the recording fake.
package router

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/zhide915/tamp/assets"
	"github.com/zhide915/tamp/internal/engine"
	"github.com/zhide915/tamp/internal/exitcode"
)

const (
	// Project is the compose project the router container belongs to. It is a
	// project of its own rather than part of any environment's, because it
	// outlives every one of them.
	Project = "tamp-router"
	// Service is the router's one compose service.
	Service = "caddy"
	// Container is the router container, as compose names it.
	Container = Project + "-" + Service + "-1"
	// Image is pinned the way every image tamp runs is — tamp hosts none.
	Image = "caddy:2.10"

	// ListenPort is what the router listens on inside its container. It never
	// varies; only the host port it is published on does.
	ListenPort = 80
	// DefaultPort is the host port the router takes when it can. It is not an
	// arbitrary default: hostname-only access means http://shop.localhost with
	// nothing after it, and that is port 80.
	DefaultPort = 80
	// FallbackPort is where the router goes when something else already holds
	// port 80. Every URL tamp prints then carries it — a worse product than
	// port 80, and a better one than a router that refuses to start.
	FallbackPort = 8080
)

const (
	// DirName holds the router's state under the tamp home: the assembled
	// Caddyfile, the compose file tamp generates for it, and the host port it
	// settled on.
	DirName = "router"
	// CaddyfileName is the assembled Caddyfile — every environment's routes in
	// one file, which is the file the container reads.
	CaddyfileName = "Caddyfile"

	composeFileName = "compose.yaml"
	stateFileName   = "router.json"

	// containerCaddyfile is where the assembled file is mounted inside the
	// container, and so what a reload is pointed at.
	containerCaddyfile = "/etc/caddy/Caddyfile"
)

// Router is the machine's router, addressed through the engine.
type Router struct {
	// Dir is where the router's generated files live.
	Dir    string
	Engine engine.Engine
	// PortIsFree decides whether the router may publish on a host port. It is
	// a field so that a test can settle the answer rather than depend on which
	// ports the machine running it happens to have spare.
	PortIsFree func(int) bool
}

// New addresses the router of the machine whose tamp home is home.
func New(home string, eng engine.Engine) *Router {
	return &Router{Dir: filepath.Join(home, DirName), Engine: eng, PortIsFree: portIsFree}
}

// Status is what the router is doing.
type Status struct {
	Running bool
	// Port is the host port the router serves on.
	Port int
}

// URL is where a hostname is reached through the router. The port is in it
// only when it has to be, because a URL with a port in it is a URL the user
// has to remember.
func (s Status) URL(host string) string {
	if s.Port == 0 || s.Port == DefaultPort {
		return "http://" + host
	}
	return fmt.Sprintf("http://%s:%d", host, s.Port)
}

// Status reports whether the router is up, and on which host port.
func (r *Router) Status(ctx context.Context) (Status, error) {
	port, err := r.port()
	if err != nil {
		return Status{}, err
	}

	// The port is tamp's own record rather than something the engine is
	// asked for, so it survives an engine that cannot be reached: a caller
	// that goes on despite the error still prints URLs that are right.
	st := Status{Port: port}
	containers, err := r.Engine.Containers(ctx, Project)
	if err != nil {
		return st, err
	}

	for _, c := range containers {
		if c.Running {
			st.Running = true
		}
	}
	return st, nil
}

// Apply points the router at the routes envs imply, starting it if it is not
// already up, and attaches it to each environment's network.
//
// Callers hold the machine lock across it. The Caddyfile is one file the whole
// machine shares, and assembling it is a read of every environment followed by
// a write of all their routes at once — two tamp commands doing that at the
// same time would each write only the routes they knew about.
func (r *Router) Apply(ctx context.Context, envs []Env, out io.Writer) (Status, error) {
	if err := r.writeCaddyfile(envs); err != nil {
		return Status{}, err
	}

	st, err := r.Status(ctx)
	if err != nil {
		return Status{}, err
	}
	if st.Running {
		if err := r.reload(ctx, out); err != nil {
			return Status{}, err
		}
	} else if st, err = r.start(ctx, out); err != nil {
		return Status{}, err
	}

	for _, e := range envs {
		if err := r.Attach(ctx, e.Network); err != nil {
			return Status{}, err
		}
	}
	return st, nil
}

// Refresh rewrites the routes and reloads a router that is already running,
// leaving a stopped one stopped.
//
// It is what removing an environment calls: taking one away is no reason to
// start a router the machine was not using.
func (r *Router) Refresh(ctx context.Context, envs []Env) (Status, error) {
	if err := r.writeCaddyfile(envs); err != nil {
		return Status{}, err
	}
	st, err := r.Status(ctx)
	if err != nil || !st.Running {
		return st, err
	}
	return st, r.reload(ctx, nil)
}

// Attach connects the router to an environment's network, which is the only
// way it reaches containers that publish nothing to the host.
//
// An environment with no network — never started, or taken down — is nothing
// to attach to and not an error: the router carries routes for stopped
// environments so that stopping one never breaks another's.
func (r *Router) Attach(ctx context.Context, network string) error {
	net, err := r.Engine.InspectNetwork(ctx, network)
	if err != nil || net == nil {
		return err
	}
	if slices.Contains(net.Containers, Container) {
		return nil
	}
	return r.Engine.ConnectNetwork(ctx, network, Container)
}

// Detach disconnects the router from an environment's network. It has to
// happen before the environment's own teardown removes that network, which
// Docker refuses while anything is still attached to it.
func (r *Router) Detach(ctx context.Context, network string) error {
	net, err := r.Engine.InspectNetwork(ctx, network)
	if err != nil || net == nil {
		return err
	}
	if !slices.Contains(net.Containers, Container) {
		return nil
	}
	return r.Engine.DisconnectNetwork(ctx, network, Container)
}

// start publishes the router on the best host port available and brings it up.
func (r *Router) start(ctx context.Context, out io.Writer) (Status, error) {
	port, err := choosePort(r.PortIsFree)
	if err != nil {
		return Status{}, err
	}
	st, err := r.startOn(ctx, port, out)
	if err != nil && port == DefaultPort {
		// The connect probe cannot see every way port 80 is unusable: on
		// Windows an excluded port range refuses the bind with nothing
		// listening there to connect to. When the up itself fails on 80, the
		// fallback port gets the retry the probe had no way to decide.
		return r.startOn(ctx, FallbackPort, out)
	}
	return st, err
}

// startOn publishes the router on one host port and brings it up.
func (r *Router) startOn(ctx context.Context, port int, out io.Writer) (Status, error) {
	if err := assets.Write("router.yaml.tmpl", r.composePath(), composeData{
		Project:            Project,
		Service:            Service,
		Image:              Image,
		Port:               port,
		ListenPort:         ListenPort,
		Caddyfile:          CaddyfileName,
		ContainerCaddyfile: containerCaddyfile,
	}); err != nil {
		return Status{}, err
	}
	if err := r.Engine.ComposeUp(ctx, r.project(), out); err != nil {
		return Status{}, err
	}
	if err := r.savePort(port); err != nil {
		return Status{}, err
	}
	return Status{Running: true, Port: port}, nil
}

// reload hands a running router its new routes without dropping a connection.
//
// The file itself is already in place: the container reads it through a bind
// mount, which is why tamp overwrites that file rather than replacing it — a
// rename would leave the container looking at the file that used to be there.
func (r *Router) reload(ctx context.Context, out io.Writer) error {
	return r.Engine.Exec(ctx, engine.ExecRequest{
		Container: Container,
		Cmd:       []string{"caddy", "reload", "--config", containerCaddyfile},
		Stdout:    out,
		Stderr:    out,
	})
}

// composeData is what the router's compose template is rendered from.
type composeData struct {
	Project            string
	Service            string
	Image              string
	Port               int
	ListenPort         int
	Caddyfile          string
	ContainerCaddyfile string
}

func (r *Router) project() engine.ComposeProject {
	return engine.ComposeProject{Name: Project, File: r.composePath(), Dir: r.Dir}
}

func (r *Router) composePath() string   { return filepath.Join(r.Dir, composeFileName) }
func (r *Router) caddyfilePath() string { return filepath.Join(r.Dir, CaddyfileName) }
func (r *Router) statePath() string     { return filepath.Join(r.Dir, stateFileName) }

// writeCaddyfile lays down the assembled routes.
//
// os.WriteFile truncates the file that is already there rather than replacing
// it, which is exactly what a running container's bind mount needs.
func (r *Router) writeCaddyfile(envs []Env) error {
	if err := r.ensureDir(); err != nil {
		return err
	}
	path := r.caddyfilePath()
	if err := os.WriteFile(path, []byte(Caddyfile(envs)), 0o644); err != nil {
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot write %s: %v", path, err),
			"check the permissions on your ~/.tamp directory")
	}
	return nil
}

func (r *Router) ensureDir() error {
	if err := os.MkdirAll(r.Dir, 0o755); err != nil {
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot create %s: %v", r.Dir, err),
			"check the permissions on your ~/.tamp directory")
	}
	return nil
}

// state is what tamp remembers about the router between commands.
//
// The host port is worth remembering because it is settled once, when the
// router starts, and read back by every command that prints a URL — including
// on a machine whose Docker has since stopped.
type state struct {
	Port int `json:"port"`
}

// port is the host port the router is published on, or the one it would take
// if it has never started.
func (r *Router) port() (int, error) {
	blob, err := os.ReadFile(r.statePath())
	if errors.Is(err, fs.ErrNotExist) {
		return DefaultPort, nil
	}
	if err != nil {
		return 0, exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot read %s: %v", r.statePath(), err),
			"check the permissions on your ~/.tamp directory")
	}

	var s state
	if err := json.Unmarshal(blob, &s); err != nil || s.Port <= 0 {
		return 0, exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("%s does not say which port the router is on", r.statePath()),
			"delete it — tamp writes it again the next time the router starts")
	}
	return s.Port, nil
}

func (r *Router) savePort(port int) error {
	blob, err := json.MarshalIndent(state{Port: port}, "", "  ")
	if err != nil {
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot render the router state: %v", err), "")
	}
	if err := os.WriteFile(r.statePath(), append(blob, '\n'), 0o644); err != nil {
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot write %s: %v", r.statePath(), err),
			"check the permissions on your ~/.tamp directory")
	}
	return nil
}
