package main

import (
	"github.com/spf13/cobra"
	"github.com/zhide915/tamp/internal/env"
)

func newContextCommand(manager func() *env.Manager, g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "context",
		Short: "Print what an AI coding agent needs to work in the environment, as Markdown",
		Long: "context prints the versions, where to edit code, how to run bench commands and tests,\n" +
			"the sites and their login, and where to look when something fails. It reads the recipe\n" +
			"and the containers on each run, works on a stopped environment, and changes nothing.\n\n" +
			"To use it, add this line to the agent's instruction file, such as AGENTS.md or\n" +
			"CLAUDE.md, in the environment's directory:\n\n" +
			"  Run `tamp context` at the start of each session.\n\n" +
			"Without -e, tamp finds the environment only in its own directory. For an agent that\n" +
			"starts anywhere else, such as an app's folder in apps/, name the environment:\n\n" +
			"  Run `tamp context -e <name>` at the start of each session.",
		Example: "  tamp context\n" +
			"  tamp context -e erp-dev",
		Args: noArgs,
		RunE: func(*cobra.Command, []string) error {
			return manager().Context(g.env)
		},
	}
}
