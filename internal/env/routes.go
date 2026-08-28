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

// routes builds the router's view from the registry, not each tamp.toml: the
// cached site lists let a stopped environment keep its routes, and stopping
// one environment never takes another's away.
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

func (m *Manager) router() *router.Router { return router.New(m.Home, m.Engine) }

// applyRoutes reassembles the Caddyfile and starts the router if needed —
// what create and start call.
func (m *Manager) applyRoutes(ctx context.Context, out io.Writer) (router.Status, error) {
	return m.underLock(func(r *router.Router, envs []router.Env) (router.Status, error) {
		return r.Apply(ctx, envs, out)
	})
}

// refreshRoutes reassembles the Caddyfile and reloads an already-running
// router — what rm calls.
func (m *Manager) refreshRoutes(ctx context.Context) (router.Status, error) {
	return m.underLock(func(r *router.Router, envs []router.Env) (router.Status, error) {
		return r.Refresh(ctx, envs)
	})
}

// underLock holds the machine lock across the whole read-registry →
// write-all-routes cycle: two concurrent writers would each emit only the
// environments they saw.
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

func (m *Manager) announceRoutes(e *Environment, st router.Status) {
	if st.Port != router.DefaultPort {
		m.Out.Note(fmt.Sprintf("port %d was taken, so the router is on %d — every URL below carries it",
			router.DefaultPort, st.Port))
	}
	m.Out.Note("mail: " + MailURL(e.Name(), st))
}
