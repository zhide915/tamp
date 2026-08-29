package hosts

import (
	"context"
	"io"
	"os/exec"
	"strings"
)

// Elevate re-runs tamp through Start-Process -Verb RunAs, which is what
// raises the UAC prompt; -Wait plus the child's exit code makes the elevated
// write a synchronous step of this one. The elevated process keeps the
// user's own profile, so it reads the same ~/.tamp.
func Elevate(ctx context.Context, exe string, args []string, out io.Writer) error {
	script := "$p = Start-Process -FilePath " + psQuote(exe) +
		" -ArgumentList " + psArgumentList(args) +
		" -Verb RunAs -Wait -PassThru;" +
		// A declined prompt fails Start-Process without stopping the script,
		// and 'exit $null' would then report the write as a success.
		" if ($null -eq $p) { exit 1 }; exit $p.ExitCode"

	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		return elevationFailed(err)
	}
	return nil
}

// psQuote makes a PowerShell single-quoted literal, where the only escape is
// a doubled quote.
func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// psArgumentList renders the arguments as one pre-quoted command line.
// Deliberately not the array form: PowerShell joins an array's elements with
// spaces and quotes none of them, so a home directory with a space in it
// reaches the elevated tamp as two arguments.
func psArgumentList(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, `"`+arg+`"`)
	}
	return psQuote(strings.Join(quoted, " "))
}
