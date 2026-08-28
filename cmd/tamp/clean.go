package main

import (
	"github.com/spf13/cobra"
	"github.com/zhide915/tamp/internal/env"
)

func newCleanCommand(d deps) *cobra.Command {
	var req env.CleanRequest

	cmd := &cobra.Command{
		Use:   "clean [env]",
		Short: "Wipe an environment's deps, assets or data layer",
		Long: "Wipe an environment's deps, assets or data layer.\n\n" +
			"Without a layer flag, tamp prints the layer table — what each layer\n" +
			"holds, what wipes it, what brings it back — and destroys nothing.\n\n" +
			"--deps and --assets are safe: 'tamp rebuild' restores both. --data\n" +
			"destroys every site's database and files, so it needs --yes. Your\n" +
			"source code is never touched.\n\n" + envArgHelp,
		Args: optionalEnvArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := d.manager()
			if err != nil {
				return err
			}
			req.Env = envArg(args)
			return m.Clean(cmd.Context(), req)
		},
	}

	cmd.Flags().BoolVar(&req.Deps, "deps", false, "wipe the virtualenv, node_modules and __pycache__")
	cmd.Flags().BoolVar(&req.Assets, "assets", false, "wipe the built JS and CSS")
	cmd.Flags().BoolVar(&req.Data, "data", false, "destroy every site's database and files")
	cmd.Flags().BoolVar(&req.All, "all", false, "wipe every layer but source")
	cmd.Flags().BoolVar(&req.Yes, "yes", false, "confirm destroying the data layer")
	return cmd
}

func newRebuildCommand(d deps) *cobra.Command {
	return &cobra.Command{
		Use:   "rebuild [env]",
		Short: "Reinstall the dependencies and rebuild the assets",
		Long: "Reinstall the dependencies and rebuild the assets.\n\n" +
			"This restores the two layers 'tamp clean --deps' and 'tamp clean\n" +
			"--assets' wipe. Dependencies come from the machine-wide pip and yarn\n" +
			"caches, so a rebuild rarely goes to the network.\n\n" +
			"The environment must be running.\n\n" + envArgHelp,
		Args: optionalEnvArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := d.manager()
			if err != nil {
				return err
			}
			return m.Rebuild(cmd.Context(), envArg(args))
		},
	}
}
