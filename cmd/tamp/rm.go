package main

import (
	"github.com/spf13/cobra"
	"github.com/zhide915/tamp/internal/engine"
	"github.com/zhide915/tamp/internal/env"
	"github.com/zhide915/tamp/internal/ui"
)

func newRemoveCommand(p *ui.Printer, eng engine.Engine) *cobra.Command {
	var volumes, yes bool

	cmd := &cobra.Command{
		Use:   "rm [env]",
		Short: "Remove an environment's containers, network and registry entry",
		Long: "Remove an environment's containers, network and registry entry.\n\n" +
			"tamp never deletes the environment directory or anything in it.\n" +
			"Volumes survive unless you pass --volumes.\n\n" + envArgHelp,
		Args: optionalEnvArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := env.NewManager(eng, p)
			if err != nil {
				return err
			}
			return m.Remove(cmd.Context(), env.RemoveRequest{
				Name:    envArg(args),
				Volumes: volumes,
				Yes:     yes,
			})
		},
	}

	cmd.Flags().BoolVar(&volumes, "volumes", false, "destroy the environment's volumes too")
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm the removal")
	return cmd
}
