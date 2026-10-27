package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/zhide915/tamp/internal/env"
	"github.com/zhide915/tamp/internal/exitcode"
)

func newShellCommand(manager func() *env.Manager, g *globals) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "shell [--force] [-- <command>...]",
		Short: "Open a shell as the bench user in the bench directory, or run one command there",
		Long: "Without a command, shell opens bash in the frappe container, and needs a terminal.\n" +
			"With a command after `--`, it runs that command and exits with its code.\n" +
			"It refuses `bench start` and `bench update`, and warns on `bench new-site` and git.",
		Example: "  tamp shell -- bench --site shop.localhost migrate\n" +
			"  tamp shell -e erp-dev -- bench build --app frappe",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return manager().Shell(g.env, args, cmd.ArgsLenAtDash() == 0, force, cmd.InOrStdin())
		},
	}
	// Every word from the command on is the command's, --force included.
	cmd.Flags().SetInterspersed(false)
	cmd.Flags().BoolVar(&force, "force", false, "skip the checks on bench start, bench update, bench new-site and git")
	return cmd
}

func newLogsCommand(manager func() *env.Manager, g *globals) *cobra.Command {
	var follow bool
	var tail string
	cmd := &cobra.Command{
		Use:   "logs [service] [-f] [--tail n]",
		Short: "Show a service's logs: frappe, mariadb, redis-cache, redis-queue or router",
		Example: "  tamp logs -f\n" +
			"  tamp logs mariadb --tail 50",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 1 {
				return exitcode.Usage(fmt.Sprintf("unexpected argument %q", args[1]), usageFix(cmd))
			}
			return nil
		},
		RunE: func(_ *cobra.Command, args []string) error {
			service := ""
			if len(args) == 1 {
				service = args[0]
			}
			return manager().Logs(g.env, service, follow, tail)
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "keep printing new lines until Ctrl-C")
	cmd.Flags().StringVar(&tail, "tail", "", "show only the last `n` lines")
	return cmd
}
