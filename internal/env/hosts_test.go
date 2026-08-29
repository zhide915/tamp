package env

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zhide915/tamp/internal/hosts"
	"github.com/zhide915/tamp/internal/ui"
)

// ApplyHostsFile is the only thing tamp ever runs with the system's rights,
// so what it refuses matters more than what it writes. The CLI harness cannot
// reach it — it would need a real elevation — so it is exercised here.

const theirs = "127.0.0.1\tlocalhost\n10.1.2.3\tintranet.corp\n"

func TestApplyHostsFileWritesAStagedBlock(t *testing.T) {
	dir := t.TempDir()
	target := writeFile(t, dir, "hosts", theirs)
	staged := writeFile(t, dir, "staged", hosts.Reconcile(theirs, []string{"abc.xyz.com"}))
	out := &bytes.Buffer{}

	if err := ApplyHostsFile(printer(out), staged, target); err != nil {
		t.Fatalf("ApplyHostsFile = %v, want nil", err)
	}

	if got := readFile(t, target); !strings.Contains(got, "127.0.0.1  abc.xyz.com") {
		t.Errorf("the staged block never reached the target:\n%q", got)
	}
}

// The elevated half must be unable to do anything but move tamp's block,
// whatever it is handed — that is what makes elevating it safe.
func TestApplyHostsFileRefusesContentThatChangesAnythingElse(t *testing.T) {
	dir := t.TempDir()
	target := writeFile(t, dir, "hosts", theirs)
	staged := writeFile(t, dir, "staged", "127.0.0.1\tlocalhost\n10.9.9.9\tintranet.corp\n")
	out := &bytes.Buffer{}

	err := ApplyHostsFile(printer(out), staged, target)

	if err == nil {
		t.Fatal("ApplyHostsFile accepted a file that rewrites somebody else's line")
	}
	if !strings.Contains(err.Error(), "outside tamp's block") {
		t.Errorf("the refusal does not say why: %v", err)
	}
	if got := readFile(t, target); got != theirs {
		t.Errorf("the refused write reached the file anyway:\n%q", got)
	}
}

func TestApplyHostsFileWithoutAStagedFileIsAnError(t *testing.T) {
	dir := t.TempDir()
	target := writeFile(t, dir, "hosts", theirs)

	err := ApplyHostsFile(printer(&bytes.Buffer{}), filepath.Join(dir, "nothing"), target)

	if err == nil {
		t.Fatal("ApplyHostsFile accepted a staged file that is not there")
	}
	if got := readFile(t, target); got != theirs {
		t.Errorf("the target was touched anyway:\n%q", got)
	}
}

func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func printer(out *bytes.Buffer) *ui.Printer {
	return &ui.Printer{Out: out, Err: out}
}
