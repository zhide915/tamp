package main

import (
	"github.com/spf13/cobra"
	"github.com/zhide915/tamp/internal/engine"
	"github.com/zhide915/tamp/internal/env"
	"github.com/zhide915/tamp/internal/ui"
)

func newListCommand(p *ui.Printer, eng engine.Engine) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List the environments on this machine",
		Long: "List the environments on this machine.\n\n" +
			"Entries whose directory has gone are dropped from the registry, with a notice.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			m, err := env.NewManager(eng, p)
			if err != nil {
				return err
			}
			return m.List(cmd.Context())
		},
	}
}
