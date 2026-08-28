package main

import (
	"github.com/spf13/cobra"
	"github.com/zhide915/tamp/internal/ui"
)

func newVersionCommand(p *ui.Printer) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the tamp version, commit and build date",
		Args:  noArgs,
		RunE: func(*cobra.Command, []string) error {
			// A result, not progress: --quiet must not swallow it.
			p.Print("tamp " + version)
			p.Print("commit:     " + commit)
			p.Print("build date: " + buildDate)
			return nil
		},
	}
}
