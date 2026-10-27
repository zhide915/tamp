package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zhide915/tamp/internal/exitcode"
)

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func assertNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("%s exists (stat error %v), want nothing written", path, err)
	}
}

// assertEnvironmentWritten checks the recipe and the .gitignore that new
// writes into dir.
func assertEnvironmentWritten(t *testing.T, dir, frappe string) {
	t.Helper()
	if got, want := readFile(t, filepath.Join(dir, "tamp.toml")), "frappe = \""+frappe+"\"\n"; got != want {
		t.Errorf("tamp.toml = %q, want %q", got, want)
	}
	if got, want := readFile(t, filepath.Join(dir, ".gitignore")), "apps/\ncompose.yaml\n"; got != want {
		t.Errorf(".gitignore = %q, want %q", got, want)
	}
}

func TestNewWritesTheRecipeAndAGitignoreIntoANewDirectory(t *testing.T) {
	c := sandbox(t)

	r := c.run(t, "new", "erp-dev", "--frappe", "version-15")

	r.assertCode(t, exitcode.CodeOK)
	assertEnvironmentWritten(t, filepath.Join(c.dir, "erp-dev"), "version-15")
	if r.stdout == "" {
		t.Error("stdout is empty, want a result line")
	}
}

func TestNewAcceptsEverySupportedFrappeBranch(t *testing.T) {
	for _, branch := range []string{"version-15", "version-16", "develop"} {
		t.Run(branch, func(t *testing.T) {
			c := sandbox(t)
			c.run(t, "new", "erp-dev", "--frappe", branch).assertCode(t, exitcode.CodeOK)
			assertEnvironmentWritten(t, filepath.Join(c.dir, "erp-dev"), branch)
		})
	}
}

func TestNewDotWritesIntoTheEmptyWorkingDirectory(t *testing.T) {
	c := sandbox(t)
	dir := filepath.Join(c.dir, "erp-dev")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	c.run(t, "new", ".", "--frappe", "version-16").assertCode(t, exitcode.CodeOK)

	assertEnvironmentWritten(t, dir, "version-16")
}

func TestNewIntoAnExistingEmptyDirectoryWritesTheRecipe(t *testing.T) {
	c := sandbox(t)
	dir := filepath.Join(c.dir, "erp-dev")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	c.run(t, "new", "erp-dev", "--frappe", "version-15").assertCode(t, exitcode.CodeOK)

	assertEnvironmentWritten(t, dir, "version-15")
}

func TestNewWithoutAPathExits2WithAFixShowingThePath(t *testing.T) {
	c := sandbox(t)

	r := c.run(t, "new")

	r.assertCode(t, exitcode.CodeUsage)
	r.assertOneLineError(t, "tamp new <path>")
}

func TestNewWithTwoPathsExits2(t *testing.T) {
	c := sandbox(t)

	r := c.run(t, "new", "erp-dev", "shop", "--frappe", "version-15")

	r.assertCode(t, exitcode.CodeUsage)
	r.assertOneLineError(t, "shop", "tamp new <path>")
	assertNotExist(t, filepath.Join(c.dir, "erp-dev"))
}

func TestNewWithoutFrappeExits2NamingTheFlagAndTheSupportedBranchesAndWritesNothing(t *testing.T) {
	c := sandbox(t)

	r := c.run(t, "new", "erp-dev")

	r.assertCode(t, exitcode.CodeUsage)
	r.assertOneLineError(t, "missing `--frappe`", "version-15, version-16, develop")
	assertNotExist(t, filepath.Join(c.dir, "erp-dev"))
}

func TestNewWithAnUnsupportedFrappeExits2ListingTheSupportedBranches(t *testing.T) {
	c := sandbox(t)

	r := c.run(t, "new", "erp-dev", "--frappe", "version-14")

	r.assertCode(t, exitcode.CodeUsage)
	r.assertOneLineError(t, "version-14", "version-15, version-16, develop")
	assertNotExist(t, filepath.Join(c.dir, "erp-dev"))
}

func TestNewIntoANonEmptyDirectoryExits2AndWritesNothing(t *testing.T) {
	c := sandbox(t)
	dir := filepath.Join(c.dir, "erp-dev")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := c.run(t, "new", "erp-dev", "--frappe", "version-15")

	r.assertCode(t, exitcode.CodeUsage)
	r.assertOneLineError(t, "erp-dev", "new or empty directory")
	assertNotExist(t, filepath.Join(dir, "tamp.toml"))
	assertNotExist(t, filepath.Join(dir, ".gitignore"))
}

func TestNewDotInANonEmptyWorkingDirectoryExits2(t *testing.T) {
	c := sandbox(t)
	dir := filepath.Join(c.dir, "erp-dev")
	if err := os.MkdirAll(filepath.Join(dir, "apps"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	r := c.run(t, "new", ".", "--frappe", "version-15")

	r.assertCode(t, exitcode.CodeUsage)
	r.assertOneLineError(t, "new or empty directory")
	assertNotExist(t, filepath.Join(dir, "tamp.toml"))
}

func TestNewOverAFileExits2(t *testing.T) {
	c := sandbox(t)
	if err := os.WriteFile(filepath.Join(c.dir, "erp-dev"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := c.run(t, "new", "erp-dev", "--frappe", "version-15")

	r.assertCode(t, exitcode.CodeUsage)
	r.assertOneLineError(t, "new or empty directory")
	if got := readFile(t, filepath.Join(c.dir, "erp-dev")); got != "mine" {
		t.Errorf("file = %q, want it unchanged", got)
	}
}

func TestNewInADirectoryHoldingARecipeExits2WithTheFixTampStart(t *testing.T) {
	c := sandbox(t)
	dir := filepath.Join(c.dir, "erp-dev")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	recipe := filepath.Join(dir, "tamp.toml")
	if err := os.WriteFile(recipe, []byte("frappe = \"version-16\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	r := c.run(t, "new", ".", "--frappe", "version-15")

	r.assertCode(t, exitcode.CodeUsage)
	r.assertOneLineError(t, "recipe", "`tamp start`")
	if got := readFile(t, recipe); got != "frappe = \"version-16\"\n" {
		t.Errorf("tamp.toml = %q, want it unchanged", got)
	}
	assertNotExist(t, filepath.Join(dir, ".gitignore"))
}

func TestNewWithALastPathSegmentThatIsNotAComposeProjectNameExits2(t *testing.T) {
	for _, name := range []string{"Erp-Dev", "erp.dev", "erp dev", "-erp", "_erp"} {
		t.Run(name, func(t *testing.T) {
			c := sandbox(t)

			r := c.run(t, "new", filepath.Join("work", name), "--frappe", "version-15")

			r.assertCode(t, exitcode.CodeUsage)
			r.assertOneLineError(t, name)
			assertNotExist(t, filepath.Join(c.dir, "work"))
		})
	}
}

func TestNewDotInADirectoryWhoseNameIsNotAComposeProjectNameExits2(t *testing.T) {
	c := sandbox(t)
	dir := filepath.Join(c.dir, "ERP")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	r := c.run(t, "new", ".", "--frappe", "version-15")

	r.assertCode(t, exitcode.CodeUsage)
	r.assertOneLineError(t, "ERP")
	assertNotExist(t, filepath.Join(dir, "tamp.toml"))
}

func TestNewAcceptsDigitsDashesAndUnderscoresInTheName(t *testing.T) {
	c := sandbox(t)

	c.run(t, "new", "15_erp-dev", "--frappe", "version-15").assertCode(t, exitcode.CodeOK)

	assertEnvironmentWritten(t, filepath.Join(c.dir, "15_erp-dev"), "version-15")
}

// The recipe check comes first: the fix is `tamp start`, whatever else the
// command line got wrong.
func TestNewDotWithoutFrappeInADirectoryHoldingARecipeExits2WithTheFixTampStart(t *testing.T) {
	c := sandbox(t)
	dir := filepath.Join(c.dir, "erp-dev")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tamp.toml"), []byte("frappe = \"version-16\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	r := c.run(t, "new", ".")

	r.assertCode(t, exitcode.CodeUsage)
	r.assertOneLineError(t, "recipe", "`tamp start`")
}

func TestNewNamedLikeTheRouterExits2AndWritesNothing(t *testing.T) {
	c := sandbox(t)

	r := c.run(t, "new", "tamp-router", "--frappe", "version-15")

	r.assertCode(t, exitcode.CodeUsage)
	r.assertOneLineError(t, "the name of tamp's router", "run `tamp new` with another name")
	if _, err := os.Stat(filepath.Join(c.dir, "tamp-router")); !os.IsNotExist(err) {
		t.Errorf("tamp-router exists: %v", err)
	}
}
