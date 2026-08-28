package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/zhide915/tamp/internal/env"
	"github.com/zhide915/tamp/internal/exitcode"
)

func newStartCommand(d deps) *cobra.Command {
	return newLifecycleCommand(d, "start", "Start an environment",
		"Start an environment.\n\n"+
			"tamp regenerates the environment's generated files from tamp.toml\n"+
			"first, so the containers always match the config — hand-edits to\n"+
			"compose.yaml do not survive.",
		(*env.Manager).Start)
}

func newStopCommand(d deps) *cobra.Command {
	return newLifecycleCommand(d, "stop", "Stop an environment's containers",
		"Stop an environment's containers.\n\n"+
			"Volumes always survive: stopping an environment never touches its data.",
		(*env.Manager).Stop)
}

func newRestartCommand(d deps) *cobra.Command {
	return newLifecycleCommand(d, "restart", "Stop an environment and start it again",
		"Stop an environment and start it again.", (*env.Manager).Restart)
}

// newLifecycleCommand builds start/stop/restart, which differ only in the
// manager method they call.
func newLifecycleCommand(
	d deps,
	use, short, long string,
	run func(*env.Manager, context.Context, string) error,
) *cobra.Command {
	return &cobra.Command{
		Use:   use + " [env]",
		Short: short,
		Long:  long + "\n\n" + envArgHelp,
		Args:  optionalEnvArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := d.manager()
			if err != nil {
				return err
			}
			return run(m, cmd.Context(), envArg(args))
		},
	}
}

// envArgHelp is shared by every command taking an optional environment.
const envArgHelp = "The environment may be named, or left out when you are inside its\n" +
	"directory — tamp finds the nearest tamp.toml the way git finds .git."

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
