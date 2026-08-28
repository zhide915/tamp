package env

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"text/tabwriter"
)

// listing is one row of `tamp list`.
type listing struct {
	Name  string
	State State
	Cfg   *Config
	Path  string
}

// List reports every environment on the machine.
//
// It is the one command that reads the whole registry rather than a single
// environment, which makes it the natural place to notice entries whose
// directory is gone — and to drop them, so the index does not accumulate
// environments that no longer exist.
func (m *Manager) List(ctx context.Context) error {
	reg, err := LoadRegistry(m.Home)
	if err != nil {
		return err
	}

	// An unreachable engine costs the state column and nothing else. Refusing
	// to list would be worse than listing without it: the registry, the paths
	// and the versions are all still true with Docker stopped.
	engineUp := true
	if _, err := m.Engine.Ping(ctx); err != nil {
		engineUp = false
		m.Out.Warn("Docker is unreachable, so tamp cannot tell which environments are running")
	}

	var rows []listing
	var gone []string
	for _, name := range reg.Names() {
		entry := reg[name]

		if _, err := os.Stat(ConfigPath(entry.Path)); errors.Is(err, fs.ErrNotExist) {
			gone = append(gone, name)
			continue
		}

		row := listing{Name: name, State: StateUnknown, Path: entry.Path}
		cfg, warnings, err := LoadConfig(ConfigPath(entry.Path))
		if err != nil {
			// A config tamp cannot read is still an environment that exists;
			// saying so beats hiding the row and leaving the user wondering
			// where it went.
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

	if len(rows) == 0 {
		m.Out.Print("no environments yet")
		m.Out.Hint("create one: tamp create <name>")
		return nil
	}
	m.printListing(rows)
	return nil
}

// prune drops registry entries whose directory no longer holds a tamp.toml.
//
// The registry is an index, not the truth: directories get moved and
// deleted without tamp's knowledge, and an entry that outlives its directory
// only ever produces confusing errors somewhere else.
func (m *Manager) prune(gone []string) {
	if len(gone) == 0 {
		return
	}

	// The stats that produced gone ran outside the lock, so each entry is
	// checked again under it: a concurrent create may have re-registered one of
	// these names at a live path, and that entry must survive.
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
	// tabwriter needs the whole table before it can align it, so the table is
	// rendered into a buffer and then handed to the Printer line by line.
	// Writing straight into the Printer's stream would work and would also be
	// the one place in tamp that bypasses it.
	var table bytes.Buffer
	w := tabwriter.NewWriter(&table, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tSTATE\tFRAPPE\tPYTHON\tNODE\tMARIADB\tPATH")
	for _, row := range rows {
		frappe, python, node, mariadb := unknownField, unknownField, unknownField, unknownField
		if row.Cfg != nil {
			frappe = string(row.Cfg.Frappe.Version)
			python, node, mariadb = row.Cfg.Toolchain.Python, row.Cfg.Toolchain.Node, row.Cfg.Toolchain.MariaDB
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			row.Name, row.State, frappe, python, node, mariadb, row.Path)
	}
	if err := w.Flush(); err != nil {
		m.Out.Warn(fmt.Sprintf("could not lay out the table: %v", err))
		return
	}

	for line := range strings.SplitSeq(strings.TrimRight(table.String(), "\n"), "\n") {
		m.Out.Print(line)
	}
}

// unknownField stands in for a column tamp could not fill — an environment
// whose config would not load still gets a row, because it still exists.
const unknownField = "?"
