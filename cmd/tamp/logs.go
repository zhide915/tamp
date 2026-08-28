package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zhide915/tamp/internal/engine"
	"github.com/zhide915/tamp/internal/env"
	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/ui"
)

// defaultTail is how much of a log tamp starts with when told nothing.
// Everything is rarely what anyone wants from a bench that has been up for a
// week, and it is what --tail 0 is for.
const defaultTail = 200

func newLogsCommand(p *ui.Printer, eng engine.Engine) *cobra.Command {
	var follow bool
	var tail int

	cmd := &cobra.Command{
		Use:   "logs [env] [service]",
		Short: "Show what one of an environment's services is saying",
		Long: "Show what one of an environment's services is saying.\n\n" +
			"Services: " + strings.Join(env.LogServiceNames(), ", ") + ".\n" +
			"The first five are the bench's own processes: they share one container\n" +
			"and one log, and tamp shows the lines belonging to the one you asked\n" +
			"for. " + env.DefaultLogService + " is the default. The router belongs to no environment,\n" +
			"so 'tamp logs router' works from anywhere.\n\n" +
			"One bare word is read as the environment when you have one by that\n" +
			"name, and as the service otherwise. Say both to be sure.\n\n" +
			"--tail counts the container's lines. For a bench process that is the\n" +
			"whole bench's output, of which tamp then shows one process's share.\n\n" + envArgHelp,
		Args: logsArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if tail < 0 {
				return exitcode.Usage(fmt.Sprintf("--tail %d asks for a negative number of lines", tail),
					"use a positive count, or 0 for the whole log")
			}
			m, err := env.NewManager(eng, p)
			if err != nil {
				return err
			}
			return m.Logs(cmd.Context(), env.LogsRequest{
				Target: args,
				Follow: follow,
				Tail:   tail,
			})
		},
	}

	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "keep the log open as new lines arrive")
	cmd.Flags().IntVar(&tail, "tail", defaultTail, "how many lines to start with; 0 is the whole log")
	return cmd
}

// logsArgs accepts an optional environment followed by an optional service.
func logsArgs(cmd *cobra.Command, args []string) error {
	if len(args) > 2 {
		return exitcode.Usage(fmt.Sprintf("unexpected argument %q", args[2]), usageHint(cmd))
	}
	return nil
}
