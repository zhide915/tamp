// Package assets holds the files tamp generates environments from.
//
// They are templates on disk rather than string literals in Go so that the
// shape of a tamp environment can be read as the compose file it becomes, and
// so that a future "prod" profile is a second template pack rather than a
// second code path.
package assets

import "embed"

// FS holds every generated-file template, named <file>.tmpl.
//
//go:embed *.tmpl
var FS embed.FS
