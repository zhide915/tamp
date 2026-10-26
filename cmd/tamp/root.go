package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zhide915/tamp/internal/docker"
	"github.com/zhide915/tamp/internal/env"
	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/ui"
)

// globals are the flags every command takes.
type globals struct {
	env   string
	quiet bool
}

func newRootCommand(p *ui.Printer, dc docker.Client) *cobra.Command {
	var g globals
	manager := func() *env.Manager { return env.NewManager(p, dc) }

	root := &cobra.Command{
		Use:   "tamp",
		Short: "tamp: environment manager for Frappe Framework",
		Long: "tamp builds each Frappe bench in Docker from its recipe, tamp.toml,\n" +
			"and serves every site by hostname.",
		// tamp prints its own one-line errors; cobra would add usage text.
		SilenceErrors: true,
		SilenceUsage:  true,
		// Without this, cobra answers an unknown command itself with exit 0.
		Args:             cobra.ArbitraryArgs,
		PersistentPreRun: func(*cobra.Command, []string) { p.Quiet = g.quiet },
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
		return exitcode.Usage(err.Error(), usageFix(cmd))
	})
	root.SetHelpCommand(newHelpCommand())
	// Cobra's completion command is not part of tamp's command tree.
	root.CompletionOptions.DisableDefaultCmd = true

	flags := root.PersistentFlags()
	flags.StringVarP(&g.env, "env", "e", "", "the environment to act on, by name")
	flags.BoolVar(&g.quiet, "quiet", false, "print only results and errors")
	// run applies --no-color from the raw arguments.
	flags.Bool("no-color", false, "print without color")

	root.AddCommand(newNewCommand(manager))
	root.AddCommand(newStartCommand(manager))
	root.AddCommand(newVersionCommand(p))
	return root
}

// newHelpCommand replaces cobra's help, which exits 0 on an unknown topic.
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
	return exitcode.Usage(fmt.Sprintf("unknown command %q", name),
		"run `tamp --help` to see the commands")
}

// noArgs replaces cobra.NoArgs so the error carries exit 2 and a fix.
func noArgs(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		return exitcode.Usage(fmt.Sprintf("unexpected argument %q", args[0]), usageFix(cmd))
	}
	return nil
}

func usageFix(cmd *cobra.Command) string {
	return fmt.Sprintf("run `%s --help` for usage", cmd.CommandPath())
}
