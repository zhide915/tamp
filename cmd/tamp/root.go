package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zhide915/tamp/internal/engine"
	"github.com/zhide915/tamp/internal/env"
	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/syncer"
	"github.com/zhide915/tamp/internal/ui"
)

// deps is the shared wiring handed to every command constructor.
type deps struct {
	p    *ui.Printer
	eng  engine.Engine
	sync syncer.Mutagen
}

func (d deps) manager() (*env.Manager, error) {
	return env.NewManager(d.eng, d.sync, d.p)
}

// newRootCommand builds the command tree; one shared Printer applies
// --quiet and --no-color in a single place.
func newRootCommand(p *ui.Printer, eng engine.Engine, sync syncer.Mutagen, stdin io.Reader) *cobra.Command {
	d := deps{p: p, eng: eng, sync: sync}
	var noColor, quiet bool

	root := &cobra.Command{
		Use:   "tamp",
		Short: "tamp — environment manager for Frappe Framework",
		Long: "tamp — environment manager for Frappe Framework.\n\n" +
			"tamp creates and manages containerized Frappe environments,\n" +
			"each with its own pinned toolchain, reachable by hostname.",
		// tamp formats its own errors; cobra's default adds usage noise.
		SilenceErrors: true,
		SilenceUsage:  true,
		// Handle unknown commands ourselves so they get tamp's usage error.
		Args: cobra.ArbitraryArgs,
		// These flags can only narrow what ui.NewPrinter already decided, so
		// output stays sane even when parsing fails.
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

	root.AddCommand(newCreateCommand(d))
	root.AddCommand(newInitCommand(d))
	root.AddCommand(newListCommand(d))
	root.AddCommand(newStartCommand(d))
	root.AddCommand(newStopCommand(d))
	root.AddCommand(newRestartCommand(d))
	root.AddCommand(newRemoveCommand(d))
	root.AddCommand(newSiteCommand(d))
	root.AddCommand(newExecCommand(d, stdin))
	root.AddCommand(newLogsCommand(d))
	root.AddCommand(newDBCommand(d))
	root.AddCommand(newDoctorCommand(d))
	root.AddCommand(newVersionCommand(p))

	return root
}

// newHelpCommand replaces cobra's help, which exits 0 on an unknown topic;
// here that is a usage error like any other misspelled command.
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

// noArgs replaces cobra.NoArgs so the failure carries tamp's exit code and hint.
func noArgs(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		return exitcode.Usage(fmt.Sprintf("unexpected argument %q", args[0]), usageHint(cmd))
	}
	return nil
}

func usageHint(cmd *cobra.Command) string {
	return fmt.Sprintf("run '%s --help' for usage", cmd.CommandPath())
}
