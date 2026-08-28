package main

import (
	"github.com/spf13/cobra"
)

func newListCommand(d deps) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List the environments on this machine",
		Long: "List the environments on this machine.\n\n" +
			"Entries whose directory has gone are dropped from the registry, with a notice.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			m, err := d.manager()
			if err != nil {
				return err
			}
			return m.List(cmd.Context())
		},
	}
}
