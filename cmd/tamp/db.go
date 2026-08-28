package main

import (
	"github.com/spf13/cobra"
	"github.com/zhide915/tamp/internal/engine"
	"github.com/zhide915/tamp/internal/env"
	"github.com/zhide915/tamp/internal/syncer"
	"github.com/zhide915/tamp/internal/ui"
)

func newDBCommand(p *ui.Printer, eng engine.Engine, sync syncer.Mutagen) *cobra.Command {
	return &cobra.Command{
		Use:   "db [env]",
		Short: "Print how to reach an environment's database, and what is in it",
		Long: "Print how to reach an environment's database, and what is in it.\n\n" +
			"This is the one thing tamp does not put behind a hostname: a database\n" +
			"client speaks the MySQL protocol and has nowhere to put a Host header,\n" +
			"so each environment publishes one port on loopback for it.\n\n" +
			"One database per site, named by Frappe when the site was created.\n\n" + envArgHelp,
		Args: optionalEnvArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := env.NewManager(eng, sync, p)
			if err != nil {
				return err
			}
			return m.DB(cmd.Context(), envArg(args))
		},
	}
}
