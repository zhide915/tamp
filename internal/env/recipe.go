package env

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/zhide915/tamp/internal/exitcode"
)

// RecipeFile is the name of the recipe in an environment's directory.
const RecipeFile = "tamp.toml"

// Recipe is the desired state of one environment. It holds no name,
// passwords, ports, or data, so it works on any machine.
type Recipe struct {
	// Frappe is the Frappe branch, one of frappeBranches.
	Frappe string `toml:"frappe"`
	// Apps are installed into the bench and on every site, in this order.
	Apps []App `toml:"apps,omitempty"`
	// Sites are the sites the bench holds.
	Sites []Site `toml:"sites,omitempty"`
}

// App is one app of the recipe, cloned into apps/<name>. An app given only
// by its url gets its name, and its default branch unless it names one, when
// the build clones it.
type App struct {
	// Name is the app's package name, which is also its folder.
	Name   string `toml:"name,omitempty"`
	URL    string `toml:"url"`
	Branch string `toml:"branch,omitempty"`
}

// Site is one site of the recipe.
type Site struct {
	// Host is the site's *.localhost hostname, which is also its name.
	Host string `toml:"host"`
}

var frappeBranches = []string{"version-15", "version-16", "develop"}

// toolchain is what one Frappe branch runs on.
type toolchain struct {
	Python, Node, MariaDB string
}

var matrix = map[string]toolchain{
	"version-15": {Python: "3.11", Node: "18", MariaDB: "10.11"},
	"version-16": {Python: "3.14", Node: "24", MariaDB: "11.8"},
	"develop":    {Python: "3.14", Node: "24", MariaDB: "11.8"},
}

// localhostName is a hostname under .localhost, which resolves to this
// machine with no hosts file.
var localhostName = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]*[a-z0-9])?\.)+localhost$`)

const siteHostFix = "name the site with lowercase letters, digits and `-`, ending in `.localhost`, such as `shop.localhost`"

// readRecipe reads the recipe in dir. A recipe that names an unsupported
// branch or a bad hostname is a conflict with the file, not the command line.
func readRecipe(dir string) (Recipe, error) {
	path := filepath.Join(dir, RecipeFile)
	var r Recipe
	md, err := toml.DecodeFile(path, &r)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// find has already checked the working directory, so dir is one
			// that a container's label names.
			return Recipe{}, exitcode.New(exitcode.CodeFailed, fmt.Sprintf("no recipe in %s", dir),
				fmt.Sprintf("run `tamp start` in the directory the environment moved to, or remove it with `tamp remove -e %s --yes`",
					filepath.Base(dir)))
		}
		return Recipe{}, exitcode.New(exitcode.CodeFailed, fmt.Sprintf("cannot read %s: %v", path, err),
			"fix the TOML in "+path)
	}
	// Commands rewrite the recipe from Recipe, so a key it lacks would be lost.
	if keys := md.Undecoded(); len(keys) > 0 {
		return Recipe{}, exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("%s holds the unknown key %q", path, keys[0].String()),
			"remove it from "+path)
	}
	if _, ok := matrix[r.Frappe]; !ok {
		return Recipe{}, exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("%s names the unsupported Frappe branch %q", path, r.Frappe),
			"set frappe to one of: "+FrappeBranchList())
	}
	for _, s := range r.Sites {
		if !localhostName.MatchString(s.Host) {
			return Recipe{}, exitcode.New(exitcode.CodeFailed,
				fmt.Sprintf("%s lists the site %q, which is not a *.localhost hostname", path, s.Host), siteHostFix)
		}
	}
	seen := map[string]bool{}
	for _, a := range r.Apps {
		switch {
		case a.URL == "":
			return Recipe{}, exitcode.New(exitcode.CodeFailed,
				fmt.Sprintf("%s lists an app without a url", path),
				"set the app's url in "+path+", or remove the app from it")
		case a.Name != "" && (!appName.MatchString(a.Name) || a.Name == "frappe"):
			return Recipe{}, exitcode.New(exitcode.CodeFailed,
				fmt.Sprintf("%s lists the app %q, which is not an app name", path, a.Name),
				"set the app's name to its package name, as in its pyproject.toml")
		case a.Name != "" && seen[a.Name]:
			return Recipe{}, exitcode.New(exitcode.CodeFailed,
				fmt.Sprintf("%s lists the app %s twice", path, a.Name), "remove one of them from "+path)
		}
		seen[a.Name] = true
	}
	return r, nil
}

// appName is a Python package name: bench uses it as the app's folder and as
// the name it installs.
var appName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// names are the recipe's app names, in order.
func (r Recipe) names() []string {
	var names []string
	for _, a := range r.Apps {
		names = append(names, a.Name)
	}
	return names
}

// FrappeBranchList is the supported Frappe branches, for help and errors.
func FrappeBranchList() string {
	return strings.Join(frappeBranches, ", ")
}

func writeRecipe(dir string, r Recipe) error {
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(r); err != nil {
		return err
	}
	return writeFile(dir, RecipeFile, buf.Bytes())
}

func writeFile(dir, name string, data []byte) error {
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return exitcode.New(exitcode.CodeFailed, fmt.Sprintf("cannot write %s: %v", path, err),
			"check that you can write to the directory")
	}
	return nil
}
