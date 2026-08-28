package assets

import (
	"bytes"
	"fmt"
	"os"
	"text/template"

	"github.com/zhide915/tamp/internal/exitcode"
)

// Write renders the named template to path.
//
// It lives beside the templates rather than in either caller because two
// packages generate files from them — an environment's compose file, and the
// router's — and a template that will not parse is the same broken tamp build
// whichever one asked for it.
func Write(name, path string, data any) error {
	tmpl, err := template.ParseFS(FS, name)
	if err != nil {
		// The templates are compiled into the binary, so this is a broken
		// build rather than anything the user did.
		return broken(name, err)
	}

	// Rendered whole before anything is written: a template that fails halfway
	// must not leave a truncated file where the old one was.
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
