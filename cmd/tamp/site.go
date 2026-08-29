package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/zhide915/tamp/internal/env"
	"github.com/zhide915/tamp/internal/exitcode"
)

func newSiteCommand(d deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "site",
		Short: "Create, list and remove the sites on an environment's bench",
		Long: "Create, list and remove the sites on an environment's bench.\n\n" +
			"A site is named exactly as the hostname it is browsed at, because that\n" +
			"is how Frappe finds it: the router passes the Host header through and\n" +
			"the bench resolves the site from it. One site is one database.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return unknownCommand("site " + args[0])
			}
			return cmd.Help()
		},
	}

	cmd.AddCommand(newSiteNewCommand(d))
	cmd.AddCommand(newSiteListCommand(d))
	cmd.AddCommand(newSiteRemoveCommand(d))
	return cmd
}

func newSiteNewCommand(d deps) *cobra.Command {
	var adminPassword, apps string

	cmd := &cobra.Command{
		Use:   "new [env] <host>",
		Short: "Create a site and route it",
		Long: "Create a site and route it.\n\n" +
			"The hostname is the site's name and its route at once, so it has to be\n" +
			"free across every environment on this machine. A *.localhost name is\n" +
			"browsable immediately; any other name is created and routed with its\n" +
			"hosts entry pending, and 'tamp hosts sync' writes it.\n\n" +
			"The environment has to be running: tamp never starts one for you.\n\n" +
			"Apps named with --apps have to be on the bench already: tamp fetches\n" +
			"none of them, because it has no way to know which branch you want.\n\n" + envArgHelp,
		Args: envAndOneArg("tamp site new needs a hostname for the site"),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := d.manager()
			if err != nil {
				return err
			}
			name, host := splitEnvArg(args)
			return m.SiteNew(cmd.Context(), env.SiteNewRequest{
				Env:           name,
				Host:          host,
				Apps:          apps,
				AdminPassword: adminPassword,
			})
		},
	}

	cmd.Flags().StringVar(&apps, "apps", "",
		"apps to install on the site, comma-separated; each must already be on the bench")
	cmd.Flags().StringVar(&adminPassword, "admin-password", "",
		"the site Administrator's password (tamp generates and prints one otherwise)")
	return cmd
}

func newSiteListCommand(d deps) *cobra.Command {
	return &cobra.Command{
		Use:   "list [env]",
		Short: "List an environment's sites",
		Long: "List an environment's sites, with the URL each is browsed at, the\n" +
			"state of its hosts-file entry, and the apps it has installed.\n\n" +
			"A stopped environment is listed from what tamp last saw, which is why\n" +
			"its apps column reads ? until the environment is running again.\n\n" + envArgHelp,
		Args: optionalEnvArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := d.manager()
			if err != nil {
				return err
			}
			return m.SiteList(cmd.Context(), envArg(args))
		},
	}
}

func newSiteRemoveCommand(d deps) *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:   "rm [env] <host>",
		Short: "Drop a site, its database and its files",
		Long: "Drop a site, its database and its files.\n\n" +
			"Only that site: every other site on the bench keeps its own database.\n" +
			"Without --yes tamp prints what it would destroy and stops.\n\n" + envArgHelp,
		Args: envAndOneArg("tamp site rm needs the hostname of the site to remove"),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := d.manager()
			if err != nil {
				return err
			}
			name, host := splitEnvArg(args)
			return m.SiteRemove(cmd.Context(), env.SiteRemoveRequest{
				Env:  name,
				Host: host,
				Yes:  yes,
			})
		},
	}

	cmd.Flags().BoolVar(&yes, "yes", false, "confirm the removal")
	return cmd
}

// envAndOneArg accepts a hostname optionally preceded by the environment —
// hostname last, so the optional environment sits where every other command
// puts it.
func envAndOneArg(missing string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		switch {
		case len(args) == 0:
			return exitcode.Usage(missing, usageHint(cmd))
		case len(args) > 2:
			return exitcode.Usage(fmt.Sprintf("unexpected argument %q", args[2]), usageHint(cmd))
		}
		return nil
	}
}

func splitEnvArg(args []string) (name, host string) {
	if len(args) == 1 {
		return "", args[0]
	}
	return args[0], args[1]
}
