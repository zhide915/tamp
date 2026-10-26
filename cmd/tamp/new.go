package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/zhide915/tamp/internal/env"
	"github.com/zhide915/tamp/internal/exitcode"
)

func newNewCommand(manager func() *env.Manager) *cobra.Command {
	var frappe string
	cmd := &cobra.Command{
		Use:   "new <path> --frappe <branch>",
		Short: "Create an environment at a new or empty directory",
		Args: func(cmd *cobra.Command, args []string) error {
			const fix = "run `tamp new <path> --frappe <branch>`"
			switch {
			case len(args) == 0:
				return exitcode.Usage("missing the environment's path", fix)
			case len(args) > 1:
				return exitcode.Usage(fmt.Sprintf("unexpected argument %q", args[1]), fix)
			}
			return nil
		},
		RunE: func(_ *cobra.Command, args []string) error {
			return manager().New(args[0], frappe)
		},
	}
	cmd.Flags().StringVar(&frappe, "frappe", "", "the Frappe branch: "+env.FrappeBranchList())
	return cmd
}
