// Package assets embeds the templates tamp generates environment files from.
// They are .tmpl files rather than Go string literals so a generated file's
// shape can be read directly, and another profile would be a second template
// pack rather than a second code path.
package assets

import "embed"

// FS holds every generated-file template, named <file>.tmpl.
//
//go:embed *.tmpl
var FS embed.FS
