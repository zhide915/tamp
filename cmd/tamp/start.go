package main

import (
	"github.com/spf13/cobra"
	"github.com/zhide915/tamp/internal/env"
)

func newStartCommand(manager func() *env.Manager, g *globals) *cobra.Command {
	return rewritesRecipe(" when a recipe app lacks a name or a branch, to record them", &cobra.Command{
		Use:   "start",
		Short: "Build what the recipe lists and the environment lacks, and start it",
		Example: "  tamp start\n" +
			"  tamp start -e erp-dev",
		Args: noArgs,
		RunE: func(*cobra.Command, []string) error {
			return manager().Start(g.env)
		},
	})
}
