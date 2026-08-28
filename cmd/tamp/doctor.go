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

// diagnosisTimeout caps the whole report; the user is waiting at a prompt.
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
			// A half-started Docker Desktop can accept a connection and then hang.
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
			// The printed report is the explanation; add no error line.
			return exitcode.Reported(report.ExitCode())
		},
	}
}
