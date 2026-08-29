package main

import (
	"github.com/spf13/cobra"
)

// syncLong heads every subcommand's help, because the distinction it draws —
// a session is one way of moving source, not the only one — is what makes
// these commands report rather than fail on Linux.
const syncLong = "On Windows and macOS an environment's source reaches its container\n" +
	"through a Mutagen session, because bind mounts there are slow and\n" +
	"deliver no file events. On Linux the host's apps/ directory is bound\n" +
	"straight in and there is no session at all — a mode, not a fault, and\n" +
	"every subcommand here says so and exits 0.\n\n"

func newSyncCommand(d deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Report on, flush and rebuild an environment's sync session",
		Long:  syncLong,
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return unknownCommand("sync " + args[0])
			}
			return cmd.Help()
		},
	}

	cmd.AddCommand(newSyncStatusCommand(d))
	cmd.AddCommand(newSyncFlushCommand(d))
	cmd.AddCommand(newSyncResetCommand(d))
	cmd.AddCommand(newSyncStopCommand(d))
	return cmd
}

func newSyncStatusCommand(d deps) *cobra.Command {
	return &cobra.Command{
		Use:   "status [env]",
		Short: "Report the session's endpoints, ignores and state",
		Long: syncLong +
			"The endpoints and the ignore list are tamp's own; everything after\n" +
			"them is Mutagen's report, quoted as Mutagen prints it.\n\n" +
			"This never downloads Mutagen: a machine that has never synced says\n" +
			"so instead.\n\n" + envArgHelp,
		Args: optionalEnvArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := d.manager()
			if err != nil {
				return err
			}
			return m.SyncStatus(cmd.Context(), envArg(args))
		},
	}
}

func newSyncFlushCommand(d deps) *cobra.Command {
	return &cobra.Command{
		Use:   "flush [env]",
		Short: "Force a full synchronization pass and wait for it",
		Long: syncLong +
			"Mutagen synchronizes continuously; this waits for a full pass, so\n" +
			"when it returns both sides agree.\n\n" + envArgHelp,
		Args: optionalEnvArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := d.manager()
			if err != nil {
				return err
			}
			return m.SyncFlush(cmd.Context(), envArg(args))
		},
	}
}

func newSyncResetCommand(d deps) *cobra.Command {
	return &cobra.Command{
		Use:   "reset [env]",
		Short: "Terminate the session and create it again",
		Long: syncLong +
			"The recovery after a large host-side change — a branch checkout,\n" +
			"say — leaves the session reconciling more than it can settle. The\n" +
			"new session mirrors the whole tree again, so it takes as long as the\n" +
			"first one did.\n\n" +
			"The environment must be running: the session's far end is its\n" +
			"container.\n\n" + envArgHelp,
		Args: optionalEnvArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := d.manager()
			if err != nil {
				return err
			}
			return m.SyncReset(cmd.Context(), envArg(args))
		},
	}
}

func newSyncStopCommand(d deps) *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the Mutagen daemon tamp runs",
		Long: syncLong +
			"tamp runs its own Mutagen daemon rather than the user's. Removing\n" +
			"the last environment takes its session but leaves the daemon idling;\n" +
			"this stops it. The next start of a synced environment starts it\n" +
			"again.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			m, err := d.manager()
			if err != nil {
				return err
			}
			return m.SyncStopDaemon(cmd.Context())
		},
	}
}
