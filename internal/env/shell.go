package env

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/zhide915/tamp/internal/docker"
	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/frappe"
	"github.com/zhide915/tamp/internal/router"
	"github.com/zhide915/tamp/internal/ui"
)

// Shell runs cmd in the frappe container as the bench user in the bench
// directory, on stdin and the Printer's streams, or opens bash there when
// cmd is empty. dashed tells whether cmd came after `--`.
func (m *Manager) Shell(envFlag string, cmd []string, dashed, force bool, stdin io.Reader) error {
	// A TTY merges stderr into stdout and puts CRLF line ends into what a
	// pipe or file receives, so it needs all three streams on a terminal.
	terminal := ui.IsTerminal(stdin) && ui.IsTerminal(m.p.Out) && ui.IsTerminal(m.p.Err)
	switch {
	case len(cmd) > 0 && !dashed:
		return exitcode.Usage("`tamp shell` takes its command after `--`",
			"run "+shellLine(envFlag, force, shellWords(cmd)))
	case len(cmd) == 0 && !terminal:
		return exitcode.Usage("`tamp shell` without a command needs a terminal",
			"run one command with "+shellLine(envFlag, false, "<command>"))
	}
	ctx := context.Background()
	t, err := m.locate(ctx, envFlag)
	if err != nil {
		return err
	}
	if !force {
		if err := refuse(t, cmd); err != nil {
			return err
		}
	}
	container := frappeContainer(t.name)
	c, err := m.docker.Inspect(ctx, container)
	if err != nil && !errors.Is(err, docker.ErrNoContainer) {
		return err
	}
	if !c.Running {
		return exitcode.New(exitcode.CodeFailed, fmt.Sprintf("the frappe container of %s is not running", t.name),
			"start it with "+t.command("start"))
	}
	if !force {
		m.warnShell(t, cmd)
	}

	if len(cmd) == 0 {
		cmd = []string{"bash"}
	}
	code, err := m.docker.Shell(ctx, docker.Shell{
		Container: container, User: frappe.User, Dir: frappe.BenchDir, Cmd: cmd,
		TTY: terminal, Stdin: stdin, Stdout: m.p.Out, Stderr: m.p.Err,
	})
	if err != nil {
		return err
	}
	if code != 0 {
		return &exitcode.Exit{Code: exitcode.Code(code)}
	}
	return nil
}

// refuse stops the bench commands that fight the frappe container. Only the
// first command is checked, so `bash -c "bench update"` passes.
func refuse(t target, cmd []string) error {
	anyway := ", or run it anyway with " + shellLine(t.flag, true, shellWords(cmd))
	switch benchCommand(cmd) {
	case "start":
		return exitcode.New(exitcode.CodeFailed,
			"`bench start` would run a second copy of the processes the frappe container already runs",
			"see their output with "+t.command("logs -f")+anyway)
	case "update":
		return exitcode.New(exitcode.CodeFailed,
			"`bench update` would pull every app inside the container, where git has none of the host's credentials",
			"pull with git on the host in "+filepath.Join(t.dir, AppsDir, "<app>")+", then run "+
				shellLine(t.flag, false, "bench --site all migrate")+anyway)
	}
	return nil
}

// benchCommand is cmd's bench subcommand, past bench's global flags, or ""
// when cmd does not run bench.
func benchCommand(cmd []string) string {
	if len(cmd) == 0 || cmd[0] != "bench" {
		return ""
	}
	for i := 1; i < len(cmd); i++ {
		switch {
		case cmd[i] == "--site":
			i++ // its value
		case !strings.HasPrefix(cmd[i], "-"):
			return cmd[i]
		}
	}
	return ""
}

// warnShell warns on commands that work but belong elsewhere.
func (m *Manager) warnShell(t target, cmd []string) {
	switch {
	case benchCommand(cmd) == "new-site":
		m.p.Warn("a site made by `bench new-site` is not in the recipe, and the router does not serve it: add sites with " +
			t.command("site add <host>"))
	case len(cmd) >= 1 && cmd[0] == "git":
		m.p.Warn("git in the container has none of the host's credentials: run git on the host, in " +
			filepath.Join(t.dir, AppsDir, "<app>"))
	}
}

// shellLine is a `tamp shell` command line for a fix. The tamp flags go
// before `--`, where tamp reads them.
func shellLine(envFlag string, force bool, command string) string {
	line := "tamp shell"
	if envFlag != "" {
		line += " -e " + envFlag
	}
	if force {
		line += " --force"
	}
	return "`" + line + " -- " + command + "`"
}

// shellWords joins argv back into a command line, quoting the words a POSIX
// shell or PowerShell would split or expand.
func shellWords(cmd []string) string {
	words := make([]string, len(cmd))
	for i, w := range cmd {
		if w == "" || strings.ContainsAny(w, " \t\n'\"$`\\|&;<>()*?[]{}#~") {
			w = "'" + strings.ReplaceAll(w, "'", `'\''`) + "'"
		}
		words[i] = w
	}
	return strings.Join(words, " ")
}

// services are what `tamp logs` reads: the environment's containers by
// compose service, and the machine's router.
var services = append(slices.Clone(envServices), "router")

// Logs writes a service's logs to the Printer's streams: the frappe
// container's when service is empty. tail is a line count, or empty for all.
func (m *Manager) Logs(envFlag, service string, follow bool, tail string) error {
	if service == "" {
		service = "frappe"
	}
	if !slices.Contains(services, service) {
		return exitcode.Usage(fmt.Sprintf("unknown service %q", service),
			"name one of "+strings.Join(services, ", "))
	}
	if tail != "" {
		n, err := strconv.Atoi(tail)
		if err != nil || n < 0 {
			return exitcode.Usage(fmt.Sprintf("--tail %s is not a line count", tail),
				"give --tail a whole number of lines, such as `--tail 100`")
		}
		tail = strconv.Itoa(n)
	}
	ctx := context.Background()
	container, missing := router.Container, "the router has no container"
	fix := "run `tamp start` in an environment's directory"
	// The router is the machine's, so its logs need no environment.
	if service != "router" {
		t, err := m.locate(ctx, envFlag)
		if err != nil {
			return err
		}
		container = serviceContainer(t.name, service)
		missing = fmt.Sprintf("%s has no %s container", t.name, service)
		fix = "build it with " + t.command("start")
	}
	if _, err := m.docker.Inspect(ctx, container); errors.Is(err, docker.ErrNoContainer) {
		return exitcode.New(exitcode.CodeFailed, missing, fix)
	} else if err != nil {
		return err
	}
	return m.docker.Logs(ctx, docker.Logs{Container: container, Follow: follow, Tail: tail, Stdout: m.p.Out, Stderr: m.p.Err})
}
