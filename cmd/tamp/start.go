package main

import (
	"github.com/spf13/cobra"
	"github.com/zhide915/tamp/internal/env"
)

func newStartCommand(manager func() *env.Manager, g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Build whatever the recipe lists that is missing, and start the environment",
		Args:  noArgs,
		RunE: func(*cobra.Command, []string) error {
			return manager().Start(g.env)
		},
	}
}
