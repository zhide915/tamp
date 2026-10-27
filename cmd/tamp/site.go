package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/zhide915/tamp/internal/env"
	"github.com/zhide915/tamp/internal/exitcode"
)

func newSiteCommand(manager func() *env.Manager, g *globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "site",
		Short: "Add, list and remove the environment's sites",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return exitcode.Usage(fmt.Sprintf("unknown command %q for `tamp site`", args[0]), usageFix(cmd))
			}
			// The help is a diagnostic here, not a result.
			cmd.SetOut(cmd.ErrOrStderr())
			if err := cmd.Help(); err != nil {
				return err
			}
			return exitcode.Usage("missing a site command", "run `tamp site add`, `tamp site list` or `tamp site remove`")
		},
	}
	cmd.AddCommand(newSiteAddCommand(manager, g))
	cmd.AddCommand(newSiteListCommand(manager, g))
	cmd.AddCommand(newSiteRemoveCommand(manager, g))
	return cmd
}

func newSiteAddCommand(manager func() *env.Manager, g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "add <host>",
		Short: "Add a *.localhost site to the recipe, create it with every app, and route it",
		Args:  oneArg("the site's hostname"),
		RunE: func(_ *cobra.Command, args []string) error {
			return manager().SiteAdd(g.env, args[0])
		},
	}
}

func newSiteListCommand(manager func() *env.Manager, g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the environment's sites with their URLs",
		Args:  noArgs,
		RunE: func(*cobra.Command, []string) error {
			return manager().SiteList(g.env)
		},
	}
}

func newSiteRemoveCommand(manager func() *env.Manager, g *globals) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "remove <host> --yes",
		Short: "Drop a site and its database, remove it from the recipe, and take it off the router",
		Args:  oneArg("the site's hostname"),
		RunE: func(_ *cobra.Command, args []string) error {
			return manager().SiteRemove(g.env, args[0], yes)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm dropping the site's database")
	return cmd
}
