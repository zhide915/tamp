package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/zhide915/tamp/internal/env"
	"github.com/zhide915/tamp/internal/exitcode"
)

func newOpenCommand(d deps) *cobra.Command {
	return &cobra.Command{
		Use:   "open [env] [host|mail]",
		Short: "Open a site, or the mail UI, in the default browser",
		Long: "Open a site, or the mail UI, in the default browser.\n\n" +
			"With no target tamp opens the environment's first site; the literal\n" +
			"word 'mail' opens its Mailpit UI. A hostname always has a dot in it\n" +
			"and an environment name never does, so tamp can tell one lone\n" +
			"argument from the other.\n\n" +
			"Nothing opens unless the address would answer: a stopped environment\n" +
			"serves nothing, and a custom domain with no hosts entry resolves\n" +
			"nowhere.\n\n" + envArgHelp,
		Args: openArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := d.manager()
			if err != nil {
				return err
			}
			return m.Open(cmd.Context(), env.ParseOpenArgs(args))
		},
	}
}

func openArgs(cmd *cobra.Command, args []string) error {
	if len(args) > 2 {
		return exitcode.Usage(fmt.Sprintf("unexpected argument %q", args[2]), usageHint(cmd))
	}
	return nil
}
