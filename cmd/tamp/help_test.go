package main

import (
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/zhide915/tamp/internal/docker"
	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/ui"
)

var updateREADME = flag.Bool("update-readme", false, "rewrite the command section of README.md from --help")

// newTestRoot is the command tree as Execute sees it: cobra adds the help
// command only when it runs.
func newTestRoot() *cobra.Command {
	root := newRootCommand(ui.NewPrinter(io.Discard, io.Discard, os.LookupEnv), &docker.Fake{}, nil, nil)
	root.InitDefaultHelpCmd()
	return root
}

// commandPaths is every command of the tree, root first, as the words after
// `tamp`.
func commandPaths() [][]string {
	var out [][]string
	var walk func(cmd *cobra.Command, path []string)
	walk = func(cmd *cobra.Command, path []string) {
		out = append(out, path)
		for _, sub := range cmd.Commands() {
			if !sub.Hidden {
				walk(sub, append(append([]string{}, path...), sub.Name()))
			}
		}
	}
	walk(newTestRoot(), []string{})
	return out
}

func TestEveryCommandsHelpEndsWithTwoExamplesOfItselfThatParse(t *testing.T) {
	c := sandbox(t)
	for _, path := range commandPaths() {
		name := strings.Join(append([]string{"tamp"}, path...), " ")
		r := c.run(t, append(path, "--help")...)
		r.assertCode(t, exitcode.CodeOK)
		_, section, ok := strings.Cut(r.stdout, "\nExamples:\n")
		lines := strings.Split(strings.TrimSuffix(section, "\n"), "\n")
		if !ok || len(lines) != 2 {
			t.Errorf("`%s --help` does not end with an Examples section of two lines:\n%s", name, r.stdout)
			continue
		}
		for _, line := range lines {
			args := strings.Fields(line)
			if !strings.HasPrefix(line, "  tamp ") {
				t.Errorf("%s: example %q is not an indented tamp command", name, line)
				continue
			}
			root := newTestRoot()
			cmd, rest, err := root.Find(args[1:])
			if err != nil {
				t.Errorf("%s: example %q names no command: %v", name, line, err)
				continue
			}
			// A parent shows its children; any other command shows itself.
			if cmd.CommandPath() != name && (!cmd.HasParent() || cmd.Parent().CommandPath() != name) {
				t.Errorf("%s: example %q runs `%s`, neither this command nor a child of it", name, line, cmd.CommandPath())
				continue
			}
			if err := cmd.ParseFlags(rest); err != nil {
				t.Errorf("%s: example %q has a bad flag: %v", name, line, err)
				continue
			}
			if err := cmd.ValidateArgs(cmd.Flags().Args()); err != nil {
				t.Errorf("%s: example %q has bad arguments: %v", name, line, err)
			}
		}
	}
}

const (
	commandsBegin = "<!-- commands:begin: generated from --help by `go test ./cmd/tamp -run TestTheREADMECommandSectionIsTheHelpOfEveryCommand -update-readme` -->\n"
	commandsEnd   = "<!-- commands:end -->\n"
)

func TestTheREADMECommandSectionIsTheHelpOfEveryCommand(t *testing.T) {
	readme, err := filepath.Abs(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	c := sandbox(t)
	var b strings.Builder
	b.WriteString(commandsBegin + "\n")
	for _, path := range commandPaths() {
		help := c.run(t, append(path, "--help")...).stdout
		// Each command's global flags are the root's, which its help shows once.
		if start := strings.Index(help, "\n\nGlobal Flags:\n"); start >= 0 {
			if end := strings.Index(help[start+2:], "\n\n"); end >= 0 {
				help = help[:start] + help[start+2+end:]
			}
		}
		b.WriteString("### `" + strings.Join(append([]string{"tamp"}, path...), " ") + "`\n\n```text\n" + help + "```\n\n")
	}
	b.WriteString(commandsEnd)
	want := b.String()

	body, err := os.ReadFile(readme)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(body), "\r\n", "\n")
	before, rest, ok1 := strings.Cut(text, commandsBegin)
	got, after, ok2 := strings.Cut(rest, commandsEnd)
	if !ok1 || !ok2 {
		t.Fatalf("README.md lacks the markers %q and %q", commandsBegin, commandsEnd)
	}
	if commandsBegin+got+commandsEnd == want {
		return
	}
	if *updateREADME {
		if err := os.WriteFile(readme, []byte(before+want+after), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Errorf("README.md's command section is stale: run `go test ./cmd/tamp -run TestTheREADMECommandSectionIsTheHelpOfEveryCommand -update-readme`")
}
