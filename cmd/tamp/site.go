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
		Short: "Add, list, and remove the environment's sites",
		Example: "  tamp site add blog.localhost\n" +
			"  tamp site list -e erp-dev",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return exitcode.Usage(fmt.Sprintf("unknown command %q for `tamp site`", args[0]), usageFix(cmd))
			}
			// The help is a diagnostic here, not a result.
			cmd.SetOut(cmd.ErrOrStderr())
			if err := cmd.Help(); err != nil {
				return err
			}
			e := envSuffix(g)
			return exitcode.Usage("missing a site command", "run `tamp site add <host>"+e+"`, `tamp site list"+e+"`, or `tamp site remove <host> --yes"+e+"`")
		},
	}
	cmd.AddCommand(newSiteAddCommand(manager, g))
	cmd.AddCommand(newSiteListCommand(manager, g))
	cmd.AddCommand(newSiteRemoveCommand(manager, g))
	return cmd
}

func newSiteAddCommand(manager func() *env.Manager, g *globals) *cobra.Command {
	return rewritesRecipe("", &cobra.Command{
		Use:   "add <host>",
		Short: "Add a *.localhost site to the recipe, create it with every app, and route it",
		Example: "  tamp site add blog.localhost\n" +
			"  tamp site add -e erp-dev docs.localhost",
		Args: oneArg("the site's hostname"),
		RunE: func(_ *cobra.Command, args []string) error {
			return manager().SiteAdd(g.env, args[0])
		},
	})
}

func newSiteListCommand(manager func() *env.Manager, g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the environment's sites and their URLs",
		Example: "  tamp site list\n" +
			"  tamp site list -e erp-dev",
		Args: noArgs,
		RunE: func(*cobra.Command, []string) error {
			return manager().SiteList(g.env)
		},
	}
}

func newSiteRemoveCommand(manager func() *env.Manager, g *globals) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "remove <host> --yes",
		Short: "Drop a site and its database, remove it from the recipe, and stop routing it",
		Example: "  tamp site remove blog.localhost --yes\n" +
			"  tamp site remove -e erp-dev docs.localhost --yes",
		Args: oneArg("the site's hostname"),
		RunE: func(_ *cobra.Command, args []string) error {
			return manager().SiteRemove(g.env, args[0], yes)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm dropping the site's database")
	return rewritesRecipe("", cmd)
}

// rewritesRecipe adds to a command's help that it rewrites tamp.toml, which
// drops the recipe's comments. A non-empty when says when it does.
func rewritesRecipe(when string, cmd *cobra.Command) *cobra.Command {
	cmd.Long = cmd.Short + ".\nIt rewrites tamp.toml" + when + ", which drops the recipe's comments."
	return cmd
}
