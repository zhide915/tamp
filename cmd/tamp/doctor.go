package main

import (
	"context"
	"time"

	"github.com/spf13/cobra"
	"github.com/zhide915/tamp/internal/doctor"
	"github.com/zhide915/tamp/internal/env"
	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/router"
)

// diagnosisTimeout bounds the whole report, not each check: the user is
// waiting at a prompt, and no honest answer takes this long.
const diagnosisTimeout = 15 * time.Second

func newDoctorCommand(d deps) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check that tamp's prerequisites are in place",
		Long: "Check that tamp's prerequisites are in place.\n\n" +
			"Every check reports pass, warn or fail; a failing check names the fix.\n" +
			"tamp exits 0 unless something failed.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// A diagnosis that never returns is the worst possible one, and a
			// half-started Docker Desktop can leave a socket that accepts a
			// connection and then says nothing.
			ctx, cancel := context.WithTimeout(cmd.Context(), diagnosisTimeout)
			defer cancel()

			home, err := env.Home()
			if err != nil {
				return err
			}

			report := doctor.Run(ctx, d.eng, router.New(home, d.eng), d.sync)
			report.Print(d.p)

			if report.OK() {
				return nil
			}
			// The report above is the explanation; anything tamp added here
			// would be a worse restatement of it.
			return exitcode.Reported(report.ExitCode())
		},
	}
}
