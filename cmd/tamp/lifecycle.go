package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/zhide915/tamp/internal/engine"
	"github.com/zhide915/tamp/internal/env"
	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/ui"
)

func newStartCommand(p *ui.Printer, eng engine.Engine) *cobra.Command {
	return newLifecycleCommand(p, eng, "start", "Start an environment",
		"Start an environment.\n\n"+
			"tamp regenerates the environment's generated files from tamp.toml\n"+
			"first, so the containers always match the config — hand-edits to\n"+
			"compose.yaml do not survive.",
		(*env.Manager).Start)
}

func newStopCommand(p *ui.Printer, eng engine.Engine) *cobra.Command {
	return newLifecycleCommand(p, eng, "stop", "Stop an environment's containers",
		"Stop an environment's containers.\n\n"+
			"Volumes always survive: stopping an environment never touches its data.",
		(*env.Manager).Stop)
}

func newRestartCommand(p *ui.Printer, eng engine.Engine) *cobra.Command {
	return newLifecycleCommand(p, eng, "restart", "Stop an environment and start it again",
		"Stop an environment and start it again.", (*env.Manager).Restart)
}

// newLifecycleCommand builds the commands that differ only in which manager
// method they call: they share an argument rule, an environment resolution and
// a help shape, and writing that three times would be three chances to drift.
func newLifecycleCommand(
	p *ui.Printer, eng engine.Engine,
	use, short, long string,
	run func(*env.Manager, context.Context, string) error,
) *cobra.Command {
	return &cobra.Command{
		Use:   use + " [env]",
		Short: short,
		Long:  long + "\n\n" + envArgHelp,
		Args:  optionalEnvArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := env.NewManager(eng, p)
			if err != nil {
				return err
			}
			return run(m, cmd.Context(), envArg(args))
		},
	}
}

// envArgHelp is the same paragraph on every command that takes an optional
// environment, so the rule is learned once.
const envArgHelp = "The environment may be named, or left out when you are inside its\n" +
	"directory — tamp finds the nearest tamp.toml the way git finds .git."

// optionalEnvArg accepts the environment name, or nothing at all.
func optionalEnvArg(cmd *cobra.Command, args []string) error {
	if len(args) > 1 {
		return exitcode.Usage(fmt.Sprintf("unexpected argument %q", args[1]), usageHint(cmd))
	}
	return nil
}

func envArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}
