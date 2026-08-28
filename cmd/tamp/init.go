package main

import (
	"strings"

	"github.com/spf13/cobra"
	"github.com/zhide915/tamp/internal/env"
	"github.com/zhide915/tamp/internal/syncer"
)

func newInitCommand(d deps) *cobra.Command {
	var frappe, apps, syncMode, name string

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Turn the current directory into an environment",
		Long: "Turn the current directory into an environment.\n\n" +
			"create's sibling, for a directory you already have. The environment\n" +
			"takes the folder's name unless --name says otherwise, and the folder\n" +
			"has to be empty.\n\n" +
			"It also brings an environment back. 'tamp rm' keeps the volumes and\n" +
			"never touches the directory, so running init in what it left behind\n" +
			"re-adopts it: same name, same path, the same volumes, and every site's\n" +
			"data with them. tamp.toml decides what that environment is, so the\n" +
			"flags below do not apply to a directory being adopted.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			m, err := d.manager()
			if err != nil {
				return err
			}
			var explicit []string
			for _, f := range []string{"frappe", "apps", "sync"} {
				if cmd.Flags().Changed(f) {
					explicit = append(explicit, "--"+f)
				}
			}
			return m.Init(cmd.Context(), env.InitRequest{
				Name:     name,
				Frappe:   frappe,
				Apps:     apps,
				Sync:     syncMode,
				Explicit: explicit,
			})
		},
	}

	cmd.Flags().StringVar(&name, "name", "",
		"name the environment something other than the folder")
	cmd.Flags().StringVar(&frappe, "frappe", string(env.DefaultFrappeVersion),
		"Frappe version: version-15, version-16 or develop")
	cmd.Flags().StringVar(&apps, "apps", "",
		"apps to fetch onto the bench, comma-separated: erpnext:version-15, or a git URL")
	cmd.Flags().StringVar(&syncMode, "sync", string(syncer.ModeAuto),
		"how source reaches the container: "+strings.Join(syncer.ModeNames(), ", "))
	return cmd
}
