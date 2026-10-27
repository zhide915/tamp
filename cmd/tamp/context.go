package main

import (
	"github.com/spf13/cobra"
	"github.com/zhide915/tamp/internal/env"
)

func newContextCommand(manager func() *env.Manager, g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "context",
		Short: "Print, as markdown, what an AI coding agent needs to work in the environment",
		Long: "context prints the versions, where to edit code, how to run bench commands, the sites\n" +
			"with their login, and where to look when something fails. It reads the recipe and the\n" +
			"containers each time, works on a stopped environment, and changes nothing.\n\n" +
			"Add one line to the agent's instruction file, such as AGENTS.md or CLAUDE.md:\n\n" +
			"  Run `tamp context` at the start of each session.\n\n" +
			"Outside the environment's directory, name it: `tamp context -e <name>`.",
		Example: "  tamp context\n" +
			"  tamp context -e erp-dev",
		Args: noArgs,
		RunE: func(*cobra.Command, []string) error {
			return manager().Context(g.env)
		},
	}
}
