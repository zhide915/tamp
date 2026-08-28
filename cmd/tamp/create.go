package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zhide915/tamp/internal/engine"
	"github.com/zhide915/tamp/internal/env"
	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/syncer"
	"github.com/zhide915/tamp/internal/ui"
)

func newCreateCommand(p *ui.Printer, eng engine.Engine, sync syncer.Mutagen) *cobra.Command {
	var frappe, apps, sync_, dir string

	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a new environment and start it",
		Long: "Create a new environment and start it.\n\n" +
			"tamp creates ./<name>/ where you run it, writes tamp.toml and the\n" +
			"files generated from it, and brings up the environment's containers.\n" +
			"No site is created — sites are always explicit.\n\n" +
			"Apps are fetched onto the bench, not installed to any site. Pin the\n" +
			"branch you mean — erpnext:version-15 — because an app given without\n" +
			"one is fetched at its repository's default branch, usually develop.",
		Args: exactlyOneName,
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := env.NewManager(eng, sync, p)
			if err != nil {
				return err
			}
			return m.Create(cmd.Context(), env.CreateRequest{
				Name:   args[0],
				Parent: dir,
				Frappe: frappe,
				Apps:   apps,
				Sync:   sync_,
			})
		},
	}

	cmd.Flags().StringVar(&frappe, "frappe", string(env.DefaultFrappeVersion),
		"Frappe version: version-15, version-16 or develop")
	cmd.Flags().StringVar(&apps, "apps", "",
		"apps to fetch onto the bench, comma-separated: erpnext:version-15, or a git URL")
	cmd.Flags().StringVar(&sync_, "sync", string(syncer.ModeAuto),
		"how source reaches the container: "+strings.Join(syncer.ModeNames(), ", "))
	cmd.Flags().StringVar(&dir, "dir", "",
		"create the environment under this directory instead of the current one")
	return cmd
}

// exactlyOneName is create's argument rule: the environment name, and nothing
// else. Unlike every other environment command, create cannot fall back to the
// current directory — there is nothing there yet to fall back to.
func exactlyOneName(cmd *cobra.Command, args []string) error {
	switch {
	case len(args) == 0:
		return exitcode.Usage("tamp create needs a name for the environment", usageHint(cmd))
	case len(args) > 1:
		return exitcode.Usage(fmt.Sprintf("unexpected argument %q", args[1]), usageHint(cmd))
	}
	return nil
}
