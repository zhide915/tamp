package env

import (
	"context"
	"fmt"
)

// DBHost is the address an environment's MariaDB is published on. It is
// loopback and not the machine's own address on purpose: this is a development
// database, and nothing outside the machine has any business reaching it.
const DBHost = "127.0.0.1"

// DBUser is the account tamp publishes. There is one, it is the environment's
// own root, and its password lives in the environment's secrets directory.
const DBUser = "root"

// DB prints how to reach an environment's MariaDB, and what is inside it.
//
// This is tamp's one deliberate exception to reaching everything by hostname:
// a database GUI client speaks the MySQL protocol over TCP and has nowhere to
// put a Host header, so the environment publishes one port and tamp says
// which. Everything else still goes through the router.
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

// printDatabases lists the one database each site has.
//
// The name is Frappe's to invent — it derives it when the site is created and
// never says it again — so tamp reads it from the site's own config, which is
// where Frappe itself looks it up. A stopped environment has nothing to ask,
// and the sites are still worth listing.
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
