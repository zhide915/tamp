package env

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
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
}

var frappeBranches = []string{"version-15", "version-16", "develop"}

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
