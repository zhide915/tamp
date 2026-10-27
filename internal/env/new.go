package env

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"

	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/router"
)

// New writes the recipe and the .gitignore of a new environment at path, then
// builds it. It checks every input before it writes anything. A recipe at
// path comes first: its fix, `tamp start`, applies whatever else is wrong.
func (m *Manager) New(path, frappe string, appSpecs, hosts []string) error {
	dir, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(dir, RecipeFile)); err == nil {
		return exitcode.Usage(fmt.Sprintf("%s already holds a recipe", dir),
			"run `tamp start` in "+dir)
	}
	if err := checkFrappeBranch(frappe); err != nil {
		return err
	}
	var apps []App
	for _, s := range appSpecs {
		url, branch, err := parseAppSpec(s)
		if err != nil {
			return err
		}
		if slices.ContainsFunc(apps, func(a App) bool { return a.URL == url }) {
			return exitcode.Usage(fmt.Sprintf("`--app` names the app %s twice", url), "name each app once")
		}
		// The build clones it and records its name, and its default branch
		// when none is given.
		apps = append(apps, App{URL: url, Branch: branch})
	}
	sites, err := siteList(hosts)
	if err != nil {
		return err
	}
	if err := checkName(filepath.Base(dir), true); err != nil {
		return err
	}
	if err := checkNewDirectory(dir); err != nil {
		return err
	}
	where := filepath.Clean(path)
	if where == "." {
		where = ""
	}
	t := target{name: filepath.Base(dir), dir: dir, where: where}
	// A name another environment holds is refused before anything is written.
	ctx := context.Background()
	if err := m.docker.Ping(ctx); err != nil {
		return err
	}
	if _, err := m.owner(ctx, t); err != nil {
		return err
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return exitcode.New(exitcode.CodeFailed, fmt.Sprintf("cannot create %s: %v", dir, err),
			"check that you can write to the parent directory")
	}
	recipe := Recipe{Frappe: frappe, Apps: apps, Sites: sites}
	if err := writeRecipe(dir, recipe); err != nil {
		return err
	}
	if err := writeFile(dir, ".gitignore", []byte("apps/\ncompose.yaml\n")); err != nil {
		return err
	}
	m.p.OK(fmt.Sprintf("wrote the recipe of %s to %s", filepath.Base(dir), filepath.Join(dir, RecipeFile)))
	return m.build(t, recipe)
}

func siteList(hosts []string) ([]Site, error) {
	var sites []Site
	seen := map[string]bool{}
	for _, host := range hosts {
		if !localhostName.MatchString(host) {
			return nil, exitcode.Usage(fmt.Sprintf("%q is not a *.localhost hostname", host), siteHostFix)
		}
		if seen[host] {
			return nil, exitcode.Usage(fmt.Sprintf("`--site` names the site %s twice", host), "name each site once")
		}
		seen[host] = true
		sites = append(sites, Site{Host: host})
	}
	return sites, nil
}

func checkFrappeBranch(frappe string) error {
	if frappe == "" {
		return exitcode.Usage("missing `--frappe`",
			"add `--frappe <branch>`, with one of: "+FrappeBranchList())
	}
	for _, b := range frappeBranches {
		if frappe == b {
			return nil
		}
	}
	return exitcode.Usage(fmt.Sprintf("tamp does not support the Frappe branch %q", frappe),
		"give `--frappe` one of: "+FrappeBranchList())
}

// composeName is Docker Compose's rule for a project name, which an
// environment's name must follow.
var composeName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// checkName checks an environment's name. fromNew says that the user typed
// it, as the path of `tamp new`, rather than that a directory holds it.
func checkName(name string, fromNew bool) error {
	rename, chars := "give the directory another name", "rename the directory with"
	if fromNew {
		rename, chars = "run `tamp new` with another name", "run `tamp new` with a name of"
	}
	if name == router.Project {
		// The router's own compose project and network take this name.
		return exitcode.Usage(fmt.Sprintf("%q is the name of tamp's router, so no environment can take it", name), rename)
	}
	if composeName.MatchString(name) {
		return nil
	}
	return exitcode.Usage(fmt.Sprintf("%q is not a valid environment name", name),
		chars+" only lowercase letters, digits, `-`, and `_`, starting with a letter or a digit")
}

func checkNewDirectory(dir string) error {
	info, err := os.Stat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	notEmpty := exitcode.Usage(fmt.Sprintf("%s is not a new or empty directory", dir),
		"give `tamp new` the path of a new or empty directory")
	if err != nil {
		return exitcode.New(exitcode.CodeFailed, fmt.Sprintf("cannot read %s: %v", dir, err),
			"check that you can read the directory")
	}
	if !info.IsDir() {
		return notEmpty
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return exitcode.New(exitcode.CodeFailed, fmt.Sprintf("cannot read %s: %v", dir, err),
			"check that you can read the directory")
	}
	if len(entries) > 0 {
		return notEmpty
	}
	return nil
}
