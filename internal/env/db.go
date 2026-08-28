package env

import (
	"context"
	"fmt"
)

// DBHost is loopback on purpose: a development database, with no business
// being reachable from outside the machine.
const DBHost = "127.0.0.1"

// DBUser is the environment's own root; its password lives in the secrets
// directory.
const DBUser = "root"

// DB prints how to reach an environment's MariaDB — the one deliberate
// exception to hostname-only access, because MySQL clients speak TCP and have
// no Host header to route on.
func (m *Manager) DB(ctx context.Context, name string) error {
	e, err := m.resolve(name)
	if err != nil {
		return err
	}
	password, err := ReadDBRootPassword(e.Dir)
	if err != nil {
		return err
	}

	m.Out.Print("host      " + DBHost)
	m.Out.Print(fmt.Sprintf("port      %d", e.Config.Ports.DB))
	m.Out.Print("user      " + DBUser)
	m.Out.Print("password  " + password)
	m.Out.Print("")

	hosts, live, err := m.sites(ctx, e)
	if err != nil {
		return err
	}
	m.printDatabases(ctx, e, hosts, live)

	m.Out.Hint(fmt.Sprintf("mysql -h %s -P %d -u %s -p", DBHost, e.Config.Ports.DB, DBUser))
	return nil
}

// printDatabases lists each site's database. The name is Frappe's invention,
// read from the site's own config where Frappe itself looks it up; a stopped
// environment still gets its sites listed.
func (m *Manager) printDatabases(ctx context.Context, e *Environment, hosts []string, live bool) {
	if len(hosts) == 0 {
		m.Out.Print("no databases yet — this bench has no sites")
		m.Out.Hint(fmt.Sprintf("create one: tamp site new %s <host>", e.Name()))
		return
	}

	bench := e.bench(m.Engine, m.Out.Stream())
	rows := make([][]string, 0, len(hosts))
	for _, host := range hosts {
		database := unknownField
		if live {
			name, err := bench.SiteDatabase(ctx, host)
			if err != nil {
				m.Out.Warn(fmt.Sprintf("could not ask %s which database it is: %v", host, err))
			} else {
				database = name
			}
		}
		rows = append(rows, []string{host, database})
	}
	m.Out.Table([]string{"SITE", "DATABASE"}, rows)
}
