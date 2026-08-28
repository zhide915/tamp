package main

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"github.com/zhide915/tamp/internal/engine"
	"github.com/zhide915/tamp/internal/env"
	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/syncer"
	"github.com/zhide915/tamp/internal/ui"
	"golang.org/x/term"
)

func newExecCommand(p *ui.Printer, eng engine.Engine, sync syncer.Mutagen, stdin io.Reader) *cobra.Command {
	var raw bool

	cmd := &cobra.Command{
		Use:   "exec [env] -- <command>...",
		Short: "Run a command inside an environment's bench container",
		Long: "Run a command inside an environment's bench container.\n\n" +
			"The command runs as the bench's own user from the bench directory,\n" +
			"its output is tamp's output, and its exit code is tamp's exit code.\n" +
			"The environment has to be running: tamp never starts one for you.\n\n" +
			"tamp refuses the few commands that would fight the environment and\n" +
			"comments on a couple more; --raw removes all of that.\n\n" + envArgHelp,
		// Cobra would otherwise tack "[flags]" onto the end of the usage line,
		// which is the one place in this grammar a tamp flag cannot go.
		DisableFlagsInUseLine: true,
		Args:                  execArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := env.NewManager(eng, sync, p)
			if err != nil {
				return err
			}
			// execArgs has already checked the separator is there and has a
			// command after it, so this splits where the user did.
			dash := cmd.Flags().ArgsLenAtDash()
			return m.Exec(cmd.Context(), env.ExecRequest{
				Name:     envArg(args[:dash]),
				Cmd:      args[dash:],
				Raw:      raw,
				Stdin:    stdin,
				Terminal: attachedConsole(stdin, p.Out),
			})
		},
	}

	cmd.Flags().BoolVar(&raw, "raw", false, "run the command with no refusals, warnings or hints")
	return cmd
}

// execArgs enforces exec's grammar: an optional environment, then --, then the
// command.
//
// The separator is required rather than inferred, because everything after it
// belongs to the command — including the flags tamp would otherwise read as
// its own. `tamp exec -- bench --version` has to reach bench intact.
func execArgs(cmd *cobra.Command, args []string) error {
	dash := cmd.Flags().ArgsLenAtDash()
	switch {
	case dash < 0:
		return exitcode.Usage("tamp exec needs '--' before the command to run",
			"write it like 'tamp exec -- bench --version'")
	case dash > 1:
		return exitcode.Usage(fmt.Sprintf("unexpected argument %q", args[1]), usageHint(cmd))
	case len(args) == dash:
		return exitcode.Usage("tamp exec needs a command after '--'", usageHint(cmd))
	}
	return nil
}

// console is the terminal tamp's own process is attached to.
type console struct{ fd int }

// attachedConsole reports the terminal tamp can hand an interactive command,
// or nil when there is not one on both ends — a pipe on either side means
// asking for a pseudo-terminal would only sprinkle escape sequences into
// output nothing is there to interpret.
//
// The type assertion is the seam a test uses: proving tamp asks for a
// terminal by allocating a pty would be a test about the pty.
func attachedConsole(stdin io.Reader, stdout io.Writer) env.Terminal {
	if t, ok := stdin.(env.Terminal); ok {
		return t
	}
	in, isFile := stdin.(*os.File)
	out, isAlsoFile := stdout.(*os.File)
	if !isFile || !isAlsoFile {
		return nil
	}
	if !term.IsTerminal(int(in.Fd())) || !term.IsTerminal(int(out.Fd())) {
		return nil
	}
	return &console{fd: int(in.Fd())}
}

func (c *console) Size() (width, height uint) {
	w, h, err := term.GetSize(c.fd)
	if err != nil {
		// The daemon picks a default for a pseudo-terminal it is given no size
		// for, which beats refusing to run the command over it.
		return 0, 0
	}
	return uint(w), uint(h)
}

func (c *console) Raw() (func(), error) {
	state, err := term.MakeRaw(c.fd)
	if err != nil {
		return nil, exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot hand this terminal to the command: %v", err),
			"redirect the command's input from a file or a pipe instead")
	}
	return func() { _ = term.Restore(c.fd, state) }, nil
}
