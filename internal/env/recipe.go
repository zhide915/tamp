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
	// Sites are the sites the bench holds.
	Sites []Site `toml:"sites,omitempty"`
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
	if _, err := toml.DecodeFile(path, &r); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Recipe{}, exitcode.Usage(fmt.Sprintf("no recipe in %s", dir),
				"run the command in an environment's directory, or create one with `tamp new <path> --frappe <branch>`")
		}
		return Recipe{}, exitcode.New(exitcode.CodeFailed, fmt.Sprintf("cannot read %s: %v", path, err),
			"fix the TOML in "+path)
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
	return r, nil
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
