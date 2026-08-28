package env

import (
	"context"
	"fmt"
	"io"

	"github.com/zhide915/tamp/internal/frappe"
	"github.com/zhide915/tamp/internal/router"
)

// MailURL is where an environment's mail UI is reached through the router.
func MailURL(name Name, st router.Status) string {
	return st.URL(router.MailHost(string(name)))
}

// routes describes every registered environment to the router.
//
// It reads the registry rather than each environment's tamp.toml on purpose:
// the registry caches the site list precisely so that the routes of a stopped
// environment can still be assembled, and so that stopping one environment
// never takes another's routes away.
func routes(reg Registry) []router.Env {
	envs := make([]router.Env, 0, len(reg))
	for _, name := range reg.Names() {
		entry := reg[name]
		res := Resources{Name: Name(name), Hash: entry.Hash}
		frappeContainer := res.Container(FrappeService)

		envs = append(envs, router.Env{
			Name:    name,
			Network: res.Network(),
			Web:     fmt.Sprintf("%s:%d", frappeContainer, frappe.WebPort),
			Socket:  fmt.Sprintf("%s:%d", frappeContainer, frappe.SocketIOPort),
			Mail:    fmt.Sprintf("%s:%d", res.Container(MailpitService), MailUIPort),
			Sites:   entry.Sites,
		})
	}
	return envs
}

// router addresses the machine's one router.
func (m *Manager) router() *router.Router { return router.New(m.Home, m.Engine) }

// applyRoutes reassembles the global Caddyfile and starts the router if it is
// not already up. It is what create and start call.
func (m *Manager) applyRoutes(ctx context.Context, out io.Writer) (router.Status, error) {
	return m.underLock(func(r *router.Router, envs []router.Env) (router.Status, error) {
		return r.Apply(ctx, envs, out)
	})
}

// refreshRoutes reassembles the global Caddyfile and reloads a router that is
// already running. It is what rm calls.
func (m *Manager) refreshRoutes(ctx context.Context) (router.Status, error) {
	return m.underLock(func(r *router.Router, envs []router.Env) (router.Status, error) {
		return r.Refresh(ctx, envs)
	})
}

// underLock reads the registry and hands the routes it implies to op, with the
// machine lock held for the whole cycle.
//
// The lock is the point. The assembled Caddyfile holds every environment's
// routes, and building it is a read of the registry followed by a write of all
// of them at once: two tamp commands doing that concurrently would each write
// only the environments they saw, and one set of routes would disappear.
func (m *Manager) underLock(op func(*router.Router, []router.Env) (router.Status, error)) (router.Status, error) {
	lock, err := AcquireLock(m.Home)
	if err != nil {
		return router.Status{}, err
	}
	defer func() { _ = lock.Release() }()

	reg, err := LoadRegistry(m.Home)
	if err != nil {
		return router.Status{}, err
	}
	return op(m.router(), routes(reg))
}

// announceRoutes prints where the environment is now reachable.
func (m *Manager) announceRoutes(e *Environment, st router.Status) {
	if st.Port != router.DefaultPort {
		m.Out.Note(fmt.Sprintf("port %d was taken, so the router is on %d — every URL below carries it",
			router.DefaultPort, st.Port))
	}
	m.Out.Note("mail: " + MailURL(e.Name(), st))
}
