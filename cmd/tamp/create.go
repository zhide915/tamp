package main

import (
	"strings"

	"github.com/spf13/cobra"
	"github.com/zhide915/tamp/internal/env"
	"github.com/zhide915/tamp/internal/syncer"
)

func newCreateCommand(d deps) *cobra.Command {
	var frappe, apps, syncMode, dir string
	var noCache bool

	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a new environment and start it",
		Long: "Create a new environment and start it.\n\n" +
			"tamp creates ./<name>/ where you run it, writes tamp.toml and the\n" +
			"files generated from it, and brings up the environment's containers.\n" +
			"No site is created — sites are always explicit.\n\n" +
			"Apps are fetched onto the bench, not installed to any site. Pin the\n" +
			"branch you mean — erpnext:version-15 — because an app given without\n" +
			"one is fetched at its repository's default branch, usually develop.\n\n" +
			"The first create of a Frappe version caches its initialized bench;\n" +
			"later ones unpack it in seconds. --no-cache skips the store.",
		// The name is required: create alone cannot fall back to the current
		// directory, since nothing exists there yet.
		Args: exactlyOneArg("tamp create needs a name for the environment"),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := d.manager()
			if err != nil {
				return err
			}
			return m.Create(cmd.Context(), env.CreateRequest{
				Name:    args[0],
				Parent:  dir,
				Frappe:  frappe,
				Apps:    apps,
				Sync:    syncMode,
				NoCache: noCache,
			})
		},
	}

	cmd.Flags().StringVar(&frappe, "frappe", string(env.DefaultFrappeVersion),
		"Frappe version: version-15, version-16 or develop")
	cmd.Flags().StringVar(&apps, "apps", "",
		"apps to fetch onto the bench, comma-separated: erpnext:version-15, or a git URL")
	cmd.Flags().StringVar(&syncMode, "sync", string(syncer.ModeAuto),
		"how source reaches the container: "+strings.Join(syncer.ModeNames(), ", "))
	cmd.Flags().StringVar(&dir, "dir", "",
		"create the environment under this directory instead of the current one")
	cmd.Flags().BoolVar(&noCache, "no-cache", false,
		"initialize a fresh bench instead of unpacking the cached template")
	return cmd
}
