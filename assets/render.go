package assets

import (
	"bytes"
	"fmt"
	"os"
	"text/template"

	"github.com/zhide915/tamp/internal/exitcode"
)

// Write renders the named template to path. It lives with the templates
// because both environment and router generation use them.
func Write(name, path string, data any) error {
	tmpl, err := template.ParseFS(FS, name)
	if err != nil {
		// Templates are embedded, so failing to parse is a broken build, not
		// user error.
		return broken(name, err)
	}

	// Render fully before writing, so a mid-template failure cannot leave a
	// truncated file.
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return broken(name, err)
	}

	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot write %s: %v", path, err),
			"check that the directory is writable")
	}
	return nil
}

func broken(name string, err error) error {
	return exitcode.New(exitcode.CodeFailed,
		fmt.Sprintf("tamp's %s template is broken: %v", name, err),
		"report this — it is a bug in tamp, not in your environment")
}
