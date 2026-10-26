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
// their recipes. A recipe it cannot read or a hostname two recipes list is
// a warning: one broken environment never stops routing for the rest.
func (m *Manager) route(ctx context.Context) error {
	containers, err := m.docker.Containers(ctx, LabelEnv)
	if err != nil {
		return err
	}
	var names []string
	paths := map[string]string{}
	for _, c := range containers {
		name := c.Labels[LabelEnv]
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

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	return router.Router{Dir: router.Dir(home), Docker: m.docker, Out: m.p.Progress()}.Apply(ctx, envs)
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
