package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/zhide915/tamp/internal/env"
	"github.com/zhide915/tamp/internal/exitcode"
)

func newOpenCommand(manager func() *env.Manager) *cobra.Command {
	return &cobra.Command{
		Use:   "open [host]",
		Short: "Open a site in the default browser: the host given, or the only site",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 1 {
				return exitcode.Usage(fmt.Sprintf("unexpected argument %q", args[1]), usageFix(cmd))
			}
			return nil
		},
		RunE: func(_ *cobra.Command, args []string) error {
			host := ""
			if len(args) == 1 {
				host = args[0]
			}
			return manager().Open(host)
		},
	}
}
