package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zhide915/tamp/internal/engine"
	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/ui"
)

// newRootCommand assembles the tamp command tree. Every command in it shares
// one Printer, so --quiet and --no-color are applied in exactly one place.
func newRootCommand(p *ui.Printer, eng engine.Engine, stdin io.Reader) *cobra.Command {
	var noColor, quiet bool

	root := &cobra.Command{
		Use:   "tamp",
		Short: "tamp — environment manager for Frappe Framework",
		Long: "tamp — environment manager for Frappe Framework.\n\n" +
			"tamp creates and manages containerized Frappe environments,\n" +
			"each with its own pinned toolchain, reachable by hostname.",
		// tamp owns its own error and usage output: one "error:" line with the
		// fix, never cobra's "Error:" plus a wall of usage text.
		SilenceErrors: true,
		SilenceUsage:  true,
		// Take the args ourselves so an unknown command is a tamp usage error
		// rather than cobra's default phrasing.
		Args: cobra.ArbitraryArgs,
		// Colour and terminal-ness were already decided in ui.NewPrinter; the
		// flags below can only narrow that, so a command line that fails to
		// parse still errors with sane output.
		PersistentPreRun: func(*cobra.Command, []string) {
			if noColor {
				p.DisableColor()
			}
			p.Quiet = quiet
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return unknownCommand(args[0])
			}
			return cmd.Help()
		},
	}

	root.SetOut(p.Out)
	root.SetErr(p.Err)
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return exitcode.Usage(err.Error(), usageHint(cmd))
	})
	root.SetHelpCommand(newHelpCommand())

	root.PersistentFlags().BoolVar(&noColor, "no-color", false, "disable coloured output")
	root.PersistentFlags().BoolVar(&quiet, "quiet", false, "print only results and errors")

	root.AddCommand(newCreateCommand(p, eng))
	root.AddCommand(newListCommand(p, eng))
	root.AddCommand(newStartCommand(p, eng))
	root.AddCommand(newStopCommand(p, eng))
	root.AddCommand(newRestartCommand(p, eng))
	root.AddCommand(newRemoveCommand(p, eng))
	root.AddCommand(newExecCommand(p, eng, stdin))
	root.AddCommand(newDoctorCommand(p, eng))
	root.AddCommand(newVersionCommand(p))

	return root
}

// newHelpCommand replaces cobra's built-in help command, which reports an
// unknown topic as a note and still exits 0. Naming a command that does not
// exist is a usage error however it is spelled.
func newHelpCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "help [command]",
		Short: "Help about any command",
		RunE: func(cmd *cobra.Command, args []string) error {
			target, rest, err := cmd.Root().Find(args)
			if err != nil || target == nil || len(rest) > 0 {
				return unknownCommand(strings.Join(args, " "))
			}
			return target.Help()
		},
	}
}

func unknownCommand(name string) error {
	return exitcode.Usage(
		fmt.Sprintf("unknown command %q", name),
		"run 'tamp --help' to see the available commands",
	)
}

// noArgs rejects surplus positional arguments as a usage error. Commands use
// it instead of cobra.NoArgs so the failure carries tamp's exit code and fix.
func noArgs(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		return exitcode.Usage(fmt.Sprintf("unexpected argument %q", args[0]), usageHint(cmd))
	}
	return nil
}

func usageHint(cmd *cobra.Command) string {
	return fmt.Sprintf("run '%s --help' for usage", cmd.CommandPath())
}
