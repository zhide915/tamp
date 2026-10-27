package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/zhide915/tamp/internal/env"
	"github.com/zhide915/tamp/internal/exitcode"
)

func newAppCommand(manager func() *env.Manager, g *globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "app",
		Short: "Add, list, and remove the environment's apps",
		Example: "  tamp app add payments#version-15\n" +
			"  tamp app list -e erp-dev",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return exitcode.Usage(fmt.Sprintf("unknown command %q for `tamp app`", args[0]), usageFix(cmd))
			}
			// The help is a diagnostic here, not a result.
			cmd.SetOut(cmd.ErrOrStderr())
			if err := cmd.Help(); err != nil {
				return err
			}
			e := envSuffix(g)
			return exitcode.Usage("missing an app command", "run `tamp app add <url|name>[#branch]"+e+"`, `tamp app list"+e+"`, or `tamp app remove <name> --yes"+e+"`")
		},
	}
	cmd.AddCommand(newAppAddCommand(manager, g))
	cmd.AddCommand(newAppListCommand(manager, g))
	cmd.AddCommand(newAppRemoveCommand(manager, g))
	return cmd
}

func newAppAddCommand(manager func() *env.Manager, g *globals) *cobra.Command {
	return rewritesRecipe("", &cobra.Command{
		Use:   "add <url|name>[#branch]",
		Short: "Clone an app, or adopt its folder in apps/, add it to the recipe, and install it on every site",
		Example: "  tamp app add payments#version-15\n" +
			"  tamp app add -e erp-dev https://github.com/frappe/payments#version-15",
		Args: oneArg("the app's URL or name"),
		RunE: func(_ *cobra.Command, args []string) error {
			return manager().AppAdd(g.env, args[0])
		},
	})
}

func newAppListCommand(manager func() *env.Manager, g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List Frappe and the environment's apps, with their branches and URLs",
		Example: "  tamp app list\n" +
			"  tamp app list -e erp-dev",
		Args: noArgs,
		RunE: func(*cobra.Command, []string) error {
			return manager().AppList(g.env)
		},
	}
}

func newAppRemoveCommand(manager func() *env.Manager, g *globals) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "remove <name> --yes",
		Short: "Uninstall an app from every site and the bench, remove it from the recipe, and keep its clone",
		Example: "  tamp app remove payments --yes\n" +
			"  tamp app remove -e erp-dev payments --yes",
		Args: oneArg("the app's name"),
		RunE: func(_ *cobra.Command, args []string) error {
			return manager().AppRemove(g.env, args[0], yes)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm deleting the app's data from every site")
	return rewritesRecipe("", cmd)
}
