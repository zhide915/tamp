package main

import (
	"github.com/spf13/cobra"
	"github.com/zhide915/tamp/internal/env"
)

// snapshotLong heads every subcommand's help, because the distinction it
// draws — protection, not caching — is the whole of what a snapshot is.
const snapshotLong = "A snapshot is a backup of an environment's data layer: every site's\n" +
	"database and files, bundled into the environment's own .tamp\n" +
	"directory. tamp takes one when you ask, and never expires or\n" +
	"prunes it.\n\n"

func newSnapshotCommand(d deps) *cobra.Command {
	create := newSnapshotCreateCommand(d)

	cmd := &cobra.Command{
		Use:   "snapshot [env]",
		Short: "Back up, list and restore an environment's site data",
		Long: snapshotLong +
			"With no subcommand, 'tamp snapshot' takes one: 'create' is the same\n" +
			"command.\n\n" + envArgHelp,
		Args: optionalEnvArg,
		RunE: create.RunE,
	}
	// The same flag, bound to the same variable create's RunE reads.
	cmd.Flags().AddFlagSet(create.Flags())

	cmd.AddCommand(create)
	cmd.AddCommand(newSnapshotListCommand(d))
	cmd.AddCommand(newSnapshotRestoreCommand(d))
	return cmd
}

const snapshotNameUsage = "name the snapshot (tamp names it after the moment otherwise)"

func newSnapshotCreateCommand(d deps) *cobra.Command {
	var name string

	cmd := &cobra.Command{
		Use:   "create [env]",
		Short: "Back up every site, with its files",
		Long: snapshotLong +
			"Each site is backed up with 'bench backup --with-files', and the\n" +
			"bundle records which apps each site had installed. A restore checks\n" +
			"that list against the bench before it changes anything.\n\n" +
			"The environment must be running.\n\n" + envArgHelp,
		Args: optionalEnvArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := d.manager()
			if err != nil {
				return err
			}
			return m.SnapshotCreate(cmd.Context(), env.SnapshotCreateRequest{Env: envArg(args), Name: name})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", snapshotNameUsage)
	return cmd
}

func newSnapshotListCommand(d deps) *cobra.Command {
	return &cobra.Command{
		Use:   "list [env]",
		Short: "List an environment's snapshots, newest first",
		Long: snapshotLong +
			"The listing reads the files on disk, so a snapshot you copied in\n" +
			"from elsewhere appears like any other.\n\n" + envArgHelp,
		Args: optionalEnvArg,
		RunE: func(_ *cobra.Command, args []string) error {
			m, err := d.manager()
			if err != nil {
				return err
			}
			return m.SnapshotList(envArg(args))
		},
	}
}

func newSnapshotRestoreCommand(d deps) *cobra.Command {
	var name string
	var yes bool

	cmd := &cobra.Command{
		Use:   "restore [env]",
		Short: "Bring a snapshot's sites back",
		Long: snapshotLong +
			"Without --name tamp restores the newest snapshot. Sites the\n" +
			"environment no longer has are recreated; each one is restored and\n" +
			"migrated, so a snapshot from an older checkout still works.\n\n" +
			"Nothing changes until three questions are answered: every app the\n" +
			"snapshot's sites need is on the bench, no hostname belongs to another\n" +
			"environment, and --yes confirms writing over site data that is there\n" +
			"now.\n\n" +
			"The environment must be running.\n\n" + envArgHelp,
		Args: optionalEnvArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := d.manager()
			if err != nil {
				return err
			}
			return m.SnapshotRestore(cmd.Context(), env.SnapshotRestoreRequest{
				Env:  envArg(args),
				Name: name,
				Yes:  yes,
			})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "the snapshot to restore (the newest otherwise)")
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm writing over site data that is there now")
	return cmd
}
