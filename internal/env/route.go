package env

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/router"
)

// route rebuilds the router's config from the running environments and
// their recipes, and starts the router if it is not running.
func (m *Manager) route(ctx context.Context) error {
	r, envs, err := m.routes(ctx, "")
	if err != nil {
		return err
	}
	return r.Apply(ctx, envs)
}

// reroute rebuilds the router's config after the environment gone stopped or
// went, and reloads a running router without starting a stopped one.
func (m *Manager) reroute(ctx context.Context, gone string) error {
	r, envs, err := m.routes(ctx, gone)
	if err != nil {
		return err
	}
	return r.Reload(ctx, envs)
}

// routes reads what the router serves, leaving out the environment except.
// A recipe it cannot read or a hostname two recipes list is a warning: one
// broken environment never stops routing for the rest.
func (m *Manager) routes(ctx context.Context, except string) (router.Router, []router.Env, error) {
	containers, err := m.docker.Containers(ctx, LabelEnv)
	if err != nil {
		return router.Router{}, nil, err
	}
	var names []string
	paths := map[string]string{}
	for _, c := range containers {
		name := c.Labels[LabelEnv]
		// The engine can list a container as running just after its stop
		// returns, so the one just stopped is left out by name.
		if name == except {
			continue
		}
		if _, seen := paths[name]; !seen {
			names = append(names, name)
			paths[name] = c.Labels[LabelPath]
		}
	}

	var envs []router.Env
	for _, name := range names {
		r, err := readRecipe(paths[name])
		if err != nil {
			m.p.Warn(fmt.Sprintf("the router skips %s: %s", name, reason(err)))
			continue
		}
		e := router.Env{Name: name, Network: network(name), Upstream: frappeContainer(name)}
		for _, s := range r.Sites {
			e.Hosts = append(e.Hosts, s.Host)
		}
		envs = append(envs, e)
	}
	envs, dups := router.Resolve(envs)
	for _, d := range dups {
		m.p.Warn(fmt.Sprintf("%s and %s both list %s; the router sends it to %s", d.Winner, d.Loser, d.Host, d.Winner))
	}

	r, err := m.router()
	return r, envs, err
}

func (m *Manager) router() (router.Router, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return router.Router{}, err
	}
	return router.Router{Dir: router.Dir(home), Docker: m.docker, Out: m.p.Progress()}, nil
}

// reason is an error without its fix, which names a command for the
// environment's own directory.
func reason(err error) string {
	var e *exitcode.Error
	if errors.As(err, &e) {
		return e.Msg
	}
	return err.Error()
}
