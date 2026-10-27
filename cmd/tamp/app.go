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
		Short: "Add, list and remove the environment's apps",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return exitcode.Usage(fmt.Sprintf("unknown command %q for `tamp app`", args[0]), usageFix(cmd))
			}
			// The help is a diagnostic here, not a result.
			cmd.SetOut(cmd.ErrOrStderr())
			if err := cmd.Help(); err != nil {
				return err
			}
			return exitcode.Usage("missing an app command", "run `tamp app add`, `tamp app list` or `tamp app remove`")
		},
	}
	cmd.AddCommand(newAppAddCommand(manager, g))
	cmd.AddCommand(newAppListCommand(manager, g))
	cmd.AddCommand(newAppRemoveCommand(manager, g))
	return cmd
}

func newAppAddCommand(manager func() *env.Manager, g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "add <url|name>[#branch]",
		Short: "Clone an app, or adopt the one in apps/, record it in the recipe, and install it on every site",
		Args:  oneArg("the app's URL or name"),
		RunE: func(_ *cobra.Command, args []string) error {
			return manager().AppAdd(g.env, args[0])
		},
	}
}

func newAppListCommand(manager func() *env.Manager, g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List Frappe and the environment's apps with their branches",
		Args:  noArgs,
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
		Args:  oneArg("the app's name"),
		RunE: func(_ *cobra.Command, args []string) error {
			return manager().AppRemove(g.env, args[0], yes)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm deleting the app's data from every site")
	return cmd
}
