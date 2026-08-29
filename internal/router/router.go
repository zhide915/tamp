// Package router manages the machine's single global Caddy container, which
// routes every site and mail UI by Host header into environments that publish
// no ports. It assembles one Caddyfile from all environments' routes and
// attaches itself to each environment's private network. Docker is reached
// only through the injected engine, so the package tests against a fake.
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
	// Project is a compose project of its own: the router outlives every
	// environment.
	Project = "tamp-router"
	Service = "caddy"
	// Container is the name compose gives the router container.
	Container = Project + "-" + Service + "-1"
	Image     = "caddy:2.10"

	// ListenPort is fixed inside the container; only the published host port
	// varies.
	ListenPort = 80
	// DefaultPort keeps URLs port-free: http://shop.localhost implies 80.
	DefaultPort = 80
	// FallbackPort is used when 80 is unavailable; every printed URL then
	// carries it.
	FallbackPort = 8080
)

const (
	// DirName is the router's state directory under the tamp home.
	DirName = "router"
	// CaddyfileName is the assembled file the container reads.
	CaddyfileName = "Caddyfile"
	// StateFileName holds the settled host port, which is what makes every
	// URL tamp prints carry the right one.
	StateFileName = "router.json"

	composeFileName = "compose.yaml"

	// containerCaddyfile is the bind-mount target a reload points at.
	containerCaddyfile = "/etc/caddy/Caddyfile"
)

// Router addresses the machine's router through the engine.
type Router struct {
	Dir    string
	Engine engine.Engine
	// PortIsFree is a field so tests can fix the answer instead of depending
	// on which ports the host has spare.
	PortIsFree func(int) bool
}

func New(home string, eng engine.Engine) *Router {
	return &Router{Dir: filepath.Join(home, DirName), Engine: eng, PortIsFree: portIsFree}
}

// Status reports whether the router runs, and on which host port.
type Status struct {
	Running bool
	Port    int
}

// URL spells the port only when it differs from the default.
func (s Status) URL(host string) string {
	if s.Port == 0 || s.Port == DefaultPort {
		return "http://" + host
	}
	return fmt.Sprintf("http://%s:%d", host, s.Port)
}

func (r *Router) Status(ctx context.Context) (Status, error) {
	port, err := r.port()
	if err != nil {
		return Status{}, err
	}

	// The port comes from tamp's own record, not the engine, so callers that
	// continue past an engine error still print correct URLs.
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

// Apply writes the routes envs imply, starts or reloads the router, and
// attaches it to each environment's network.
//
// Callers must hold the machine lock: the Caddyfile is shared machine state
// assembled from every environment at once, and concurrent writers would each
// drop the other's routes.
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

// Refresh rewrites the routes and reloads only an already-running router —
// removing an environment is no reason to start one.
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

// Attach connects the router to an environment's network — its only way to
// reach containers that publish nothing. A missing network (environment never
// started, or down) is not an error: routes for stopped environments are kept.
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

// Detach must run before an environment's network is removed — Docker refuses
// to remove a network with anything still attached.
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

func (r *Router) start(ctx context.Context, out io.Writer) (Status, error) {
	port, err := choosePort(r.PortIsFree)
	if err != nil {
		return Status{}, err
	}
	st, err := r.startOn(ctx, port, out)
	if err != nil && port == DefaultPort {
		// The connect probe cannot see a bind refused by a Windows excluded
		// port range, so a failed up on 80 retries on the fallback.
		return r.startOn(ctx, FallbackPort, out)
	}
	return st, err
}

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

// reload makes a running router pick up the rewritten Caddyfile without
// dropping connections.
func (r *Router) reload(ctx context.Context, out io.Writer) error {
	return r.Engine.Exec(ctx, engine.ExecRequest{
		Container: Container,
		Cmd:       []string{"caddy", "reload", "--config", containerCaddyfile},
		Stdout:    out,
		Stderr:    out,
	})
}

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
func (r *Router) statePath() string     { return filepath.Join(r.Dir, StateFileName) }

// writeCaddyfile writes the assembled routes in place. os.WriteFile truncates
// rather than replaces, which the container's bind mount requires — a rename
// would leave the container reading the old inode.
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

// state persists the settled host port, so later commands print correct URLs
// even when Docker is down.
type state struct {
	Port int `json:"port"`
}

// port returns the recorded host port, or DefaultPort before the first start.
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
