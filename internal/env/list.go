package env

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/zhide915/tamp/internal/router"
)

// listing is one row of `tamp list`.
type listing struct {
	Name  string
	Mail  string
	State State
	Cfg   *Config
	Path  string
}

// List reports every environment. As the one command that reads the whole
// registry, it is also the place that prunes entries whose directory is gone.
func (m *Manager) List(ctx context.Context) error {
	reg, err := LoadRegistry(m.Home)
	if err != nil {
		return err
	}

	// Docker being down costs the state column and nothing else — the rest of
	// the row is still true.
	engineUp := true
	if _, err := m.Engine.Ping(ctx); err != nil {
		engineUp = false
		m.Out.Warn("Docker is unreachable, so tamp cannot tell which environments are running")
	}

	// The router's port is tamp's own record, so the URLs stay right with
	// Docker down; the status carries the port either way.
	status, err := m.router().Status(ctx)
	if err != nil && engineUp {
		return err
	}

	var rows []listing
	var gone []string
	for _, name := range reg.Names() {
		entry := reg[name]

		if _, err := os.Stat(ConfigPath(entry.Path)); errors.Is(err, fs.ErrNotExist) {
			gone = append(gone, name)
			continue
		}

		row := listing{Name: name, Mail: MailURL(Name(name), status), State: StateUnknown, Path: entry.Path}
		cfg, warnings, err := LoadConfig(ConfigPath(entry.Path))
		if err != nil {
			// An unreadable config is still an environment that exists — it
			// keeps its row.
			m.Out.Warn(err.Error())
		} else {
			row.Cfg = cfg
			for _, w := range warnings {
				m.Out.Warn(w)
			}
		}

		if engineUp {
			containers, err := m.Engine.Containers(ctx, Resources{Name: Name(name), Hash: entry.Hash}.Project())
			if err != nil {
				m.Out.Warn(err.Error())
			} else {
				row.State = stateOf(containers)
			}
		}
		rows = append(rows, row)
	}

	m.prune(gone)

	m.printRouter(status, engineUp)

	if len(rows) == 0 {
		m.Out.Print("no environments yet")
		m.Out.Hint("create one: tamp create <name>")
		return nil
	}
	m.printListing(rows)
	return nil
}

// printRouter leads the listing: a stopped router is why nothing on the
// machine responds.
func (m *Manager) printRouter(status router.Status, engineUp bool) {
	switch {
	case !engineUp:
		m.Out.Print("router  unknown — Docker is unreachable")
	case status.Running:
		m.Out.Print("router  running on " + status.URL("localhost"))
	default:
		m.Out.Print("router  not running — no hostname resolves until it is")
		m.Out.Hint("start it by starting an environment: tamp start <name>")
	}
	m.Out.Print("")
}

// prune drops registry entries whose directory no longer holds a tamp.toml —
// they only ever produce confusing errors elsewhere.
func (m *Manager) prune(gone []string) {
	if len(gone) == 0 {
		return
	}

	// Re-checked under the lock: a concurrent create may have re-registered
	// one of these names at a live path.
	type pruned struct{ name, path string }
	var dropped []pruned
	err := UpdateRegistry(m.Home, func(reg Registry) error {
		for _, name := range gone {
			entry, ok := reg[name]
			if !ok {
				continue
			}
			if _, err := os.Stat(ConfigPath(entry.Path)); errors.Is(err, fs.ErrNotExist) {
				delete(reg, name)
				dropped = append(dropped, pruned{name, entry.Path})
			}
		}
		return nil
	})
	if err != nil {
		m.Out.Warn(fmt.Sprintf("could not update the registry: %v", err))
		return
	}
	for _, p := range dropped {
		m.Out.Warn(fmt.Sprintf("pruned %q from the registry — %s is gone", p.name, p.path))
	}
}

func (m *Manager) printListing(rows []listing) {
	table := make([][]string, 0, len(rows))
	for _, row := range rows {
		frappe, python, node, mariadb := unknownField, unknownField, unknownField, unknownField
		if row.Cfg != nil {
			frappe = string(row.Cfg.Frappe.Version)
			python, node, mariadb = row.Cfg.Toolchain.Python, row.Cfg.Toolchain.Node, row.Cfg.Toolchain.MariaDB
		}
		table = append(table, []string{
			row.Name, string(row.State), frappe, python, node, mariadb, row.Mail, row.Path})
	}
	m.Out.Table([]string{"NAME", "STATE", "FRAPPE", "PYTHON", "NODE", "MARIADB", "MAIL", "PATH"}, table)
}

// unknownField fills a column tamp could not read.
const unknownField = "?"
