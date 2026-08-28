package env

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/zhide915/tamp/internal/exitcode"
)

// Name is a validated environment name. A distinct type so an unchecked
// string never reaches a hostname (mail.<name>.localhost) or a Docker
// resource name.
type Name string

func (n Name) String() string { return string(n) }

// namePattern is a DNS label capped at 32 characters — names appear in
// hostnames and Docker resource names.
var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// reservedNames are tamp's command words, listed literally rather than
// derived: a command added in a later release must not invalidate an
// environment somebody already has.
var reservedNames = []string{
	"clean", "completion", "context", "create", "db", "doctor", "exec", "help", "hosts",
	"init", "list", "logs", "ls", "mail", "open", "rebuild", "restart",
	"restore", "rm", "router", "site", "snapshot", "start", "stop", "sync",
	"version",
}

// ParseName rejections are exit 1, not usage errors: the command line was
// well-formed, tamp just cannot use the name where it has to appear.
func ParseName(s string) (Name, error) {
	if slices.Contains(reservedNames, s) {
		return "", exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("%q is a tamp command word and cannot be an environment name", s),
			"pick another name — reserved: "+strings.Join(reservedNames, ", "))
	}
	if !namePattern.MatchString(s) {
		return "", exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("%q is not a valid environment name", s),
			"use 1-32 characters of a-z, 0-9 and '-', starting with a letter or digit")
	}
	return Name(s), nil
}
