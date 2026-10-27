package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/zhide915/tamp/internal/env"
	"github.com/zhide915/tamp/internal/exitcode"
)

func newNewCommand(manager func() *env.Manager) *cobra.Command {
	var frappe string
	var apps, sites []string
	cmd := &cobra.Command{
		Use:   "new <path> --frappe <branch> [--app <url|name>[#branch]]... [--site <host>]...",
		Short: "Create an environment at a new or empty directory, and build it",
		Example: "  tamp new erp-dev --frappe version-15 --site shop.localhost\n" +
			"  tamp new . --frappe version-15 --app payments#version-15 --site pay.localhost",
		Args: func(cmd *cobra.Command, args []string) error {
			const fix = "run `tamp new <path> --frappe <branch>`"
			switch {
			case len(args) == 0:
				return exitcode.Usage("missing the environment's path", fix)
			case len(args) > 1:
				return exitcode.Usage(fmt.Sprintf("unexpected argument %q", args[1]), fix)
			}
			return nil
		},
		RunE: func(_ *cobra.Command, args []string) error {
			return manager().New(args[0], frappe, apps, sites)
		},
	}
	cmd.Flags().StringVar(&frappe, "frappe", "", "the Frappe branch: "+env.FrappeBranchList())
	cmd.Flags().StringArrayVar(&apps, "app", nil, "an app to clone and install, as a URL or a frappe app name, with an optional #branch: repeat the flag for more")
	cmd.Flags().StringArrayVar(&sites, "site", nil, "a *.localhost site to create: repeat the flag for more")
	return cmd
}
