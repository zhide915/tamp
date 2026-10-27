package main

import (
	"github.com/spf13/cobra"
	"github.com/zhide915/tamp/internal/env"
)

func newStopCommand(manager func() *env.Manager, g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the environment's containers",
		Example: "  tamp stop\n" +
			"  tamp stop -e erp-dev",
		Args: noArgs,
		RunE: func(*cobra.Command, []string) error {
			return manager().Stop(g.env)
		},
	}
}

func newRemoveCommand(manager func() *env.Manager, g *globals) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "remove --yes",
		Short: "Delete the environment's sync session, containers, network and volumes, and keep its directory",
		Example: "  tamp remove --yes\n" +
			"  tamp remove -e erp-dev --yes",
		Args: noArgs,
		RunE: func(*cobra.Command, []string) error {
			return manager().Remove(g.env, yes)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm deleting the containers, network and volumes, with every site's database")
	return cmd
}

func newListCommand(manager func() *env.Manager) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List every environment that has containers, with its sites",
		Example: "  tamp list\n" +
			"  tamp list --no-color",
		Args: noArgs,
		RunE: func(*cobra.Command, []string) error {
			return manager().List()
		},
	}
}

func newStatusCommand(manager func() *env.Manager, g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the environment's containers, sync state and site URLs",
		Example: "  tamp status\n" +
			"  tamp status -e erp-dev",
		Args: noArgs,
		RunE: func(*cobra.Command, []string) error {
			return manager().Status(g.env)
		},
	}
}
