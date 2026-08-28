package env

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/zhide915/tamp/internal/exitcode"
)

// Name is an environment name that has been checked against tamp's rules.
//
// It is a distinct type so that a name reaches resource naming and the
// registry only by way of ParseName: an unvalidated string would end up in a
// hostname (mail.<name>.localhost) and a container name, where the damage is
// discovered much later and much less clearly.
type Name string

func (n Name) String() string { return string(n) }

// namePattern is the naming rule: a valid DNS label, capped at 32 characters.
// Names appear in hostnames and in Docker resource names, which is what
// forbids uppercase, underscores, dots and a leading hyphen.
var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// reservedNames are tamp's command words, listed literally rather than
// read off the command tree on purpose: deriving them would mean that adding a
// command in a later release silently invalidates an environment somebody
// already created.
var reservedNames = []string{
	"clean", "completion", "context", "create", "db", "doctor", "exec", "help", "hosts",
	"init", "list", "logs", "ls", "mail", "open", "rebuild", "restart",
	"restore", "rm", "router", "site", "snapshot", "start", "stop", "sync",
	"version",
}

// ParseName validates an environment name. The failures are exit 1 rather than
// a usage error: the command line was well-formed, tamp just cannot use the
// name in the places it has to appear.
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
