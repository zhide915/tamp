package main

import (
	"github.com/spf13/cobra"
	"github.com/zhide915/tamp/internal/env"
)

func newDoctorCommand(manager func() *env.Manager, g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check Docker, git, Mutagen, port 80, the router, and the environment, and change nothing",
		Example: "  tamp doctor\n" +
			"  tamp doctor -e erp-dev",
		Args: noArgs,
		RunE: func(*cobra.Command, []string) error {
			return manager().Doctor(g.env)
		},
	}
}
