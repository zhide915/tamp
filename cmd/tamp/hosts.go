package main

import (
	"github.com/spf13/cobra"
	"github.com/zhide915/tamp/internal/env"
	"github.com/zhide915/tamp/internal/hosts"
)

func newHostsCommand(d deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hosts",
		Short: "Manage tamp's block in the OS hosts file",
		Long: "Manage tamp's block in the operating system's hosts file.\n\n" +
			"Sites named outside *.localhost need an entry there before anything\n" +
			"on this machine resolves them. tamp keeps every one of them between\n" +
			"two markers it owns and never writes a byte outside them.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return unknownCommand("hosts " + args[0])
			}
			return cmd.Help()
		},
	}

	cmd.AddCommand(newHostsSyncCommand(d))
	cmd.AddCommand(newHostsApplyCommand(d))
	return cmd
}

func newHostsSyncCommand(d deps) *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Reconcile tamp's hosts-file block with every custom domain",
		Long: "Reconcile tamp's hosts-file block with every custom domain.\n\n" +
			"The block points every site hostname outside *.localhost, on every\n" +
			"environment on this machine, at " + hosts.Loopback + ". A site you removed loses\n" +
			"its line on the next sync. Everything outside tamp's two markers is\n" +
			"left byte for byte as it was.\n\n" +
			"The environments need not be running: tamp syncs from the hostnames\n" +
			"it recorded when the sites were made.\n\n" +
			"The hosts file belongs to the system, so the write asks for elevated\n" +
			"rights: a UAC prompt on Windows, sudo elsewhere. No other tamp\n" +
			"command elevates.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			m, err := d.manager()
			if err != nil {
				return err
			}
			return m.HostsSync(cmd.Context())
		},
	}
}

// newHostsApplyCommand is the elevated half of a sync, and nothing a user
// runs: hidden because 'tamp hosts sync' is the whole interface, and because
// running it by hand needs the elevation tamp otherwise arranges itself.
func newHostsApplyCommand(d deps) *cobra.Command {
	return &cobra.Command{
		Use:    "apply <file>",
		Short:  "Write a staged hosts file (run elevated by 'tamp hosts sync')",
		Hidden: true,
		Args:   exactlyOneArg("tamp hosts apply needs the staged file to write"),
		RunE: func(_ *cobra.Command, args []string) error {
			// The target is compiled in, never read from the environment:
			// this is the half that runs with the system's rights.
			return env.ApplyHostsFile(d.p, args[0], hosts.OSPath())
		},
	}
}
