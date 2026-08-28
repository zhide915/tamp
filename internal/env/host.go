package env

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/zhide915/tamp/internal/exitcode"
)

// Host is a site hostname that has been checked against tamp's rules.
//
// It is a distinct type for the same reason Name is: a hostname reaches the
// router's configuration and a directory name on the bench, and an unchecked
// one is discovered as a Caddy that will not reload or a site Frappe cannot
// resolve, long after the command that accepted it.
type Host string

func (h Host) String() string { return string(h) }

// LocalhostSuffix is the domain that needs no configuration anywhere: every
// evergreen browser resolves *.localhost to loopback on its own, which is what
// makes a tamp site browsable the moment it exists.
const LocalhostSuffix = ".localhost"

// IsLocal reports whether the host resolves without a hosts-file entry.
func (h Host) IsLocal() bool { return strings.HasSuffix(string(h), LocalhostSuffix) }

// hostLabelPattern is one DNS label: alphanumerics and hyphens, never starting
// or ending with a hyphen, at most 63 characters.
var hostLabelPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// digitsPattern matches a label that is only digits.
var digitsPattern = regexp.MustCompile(`^[0-9]+$`)

// maxHostLength is the length limit DNS puts on a name.
const maxHostLength = 253

// ParseHost validates a site hostname.
//
// The rules are DNS's, narrowed in two places. Uppercase is rejected rather
// than folded, because the hostname is also the site's directory name and
// tamp quietly renaming what the user typed is worse than saying no. And a
// bare single label is rejected, because a site has to be reachable by the
// name it is given: shop resolves to nothing, shop.localhost resolves to
// loopback everywhere.
//
// Failures are exit 1, not a usage error: the command line was well-formed,
// tamp just cannot put this name where it has to go.
func ParseHost(s string) (Host, error) {
	invalid := func(msg, fix string) error {
		return exitcode.New(exitcode.CodeFailed, fmt.Sprintf("%q %s", s, msg), fix)
	}

	switch {
	case s == "":
		return "", exitcode.New(exitcode.CodeFailed, "a site needs a hostname",
			"name it after the address you want to browse, like shop.localhost")
	case len(s) > maxHostLength:
		return "", invalid(fmt.Sprintf("is %d characters, and a hostname may be at most %d", len(s), maxHostLength),
			"pick a shorter name")
	case s != strings.ToLower(s):
		return "", invalid("has uppercase letters, and a site's hostname is its directory name",
			"write it in lowercase: "+strings.ToLower(s))
	case !strings.Contains(s, "."):
		return "", invalid("is a single label, which resolves to nothing in a browser",
			"give it a domain: "+s+LocalhostSuffix)
	}

	labels := strings.Split(s, ".")
	for _, label := range labels {
		if !hostLabelPattern.MatchString(label) {
			return "", invalid(fmt.Sprintf("has %q where a hostname wants a label", label),
				"labels are 1-63 characters of a-z, 0-9 and '-', starting and ending with a letter or digit")
		}
	}
	// An address tamp cannot route by name: the router matches on the Host
	// header, and a browser sent to an IP never sends the name tamp routed.
	if digitsPattern.MatchString(labels[len(labels)-1]) {
		return "", invalid("looks like an IP address, and the router routes by hostname",
			"give the site a name, like shop.localhost")
	}
	return Host(s), nil
}
