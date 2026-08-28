package env

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/zhide915/tamp/internal/exitcode"
)

// Host is a validated site hostname. A distinct type so an unchecked string
// never reaches the router's configuration or a directory name on the bench.
type Host string

func (h Host) String() string { return string(h) }

// LocalhostSuffix resolves to loopback in every evergreen browser with no
// configuration at all.
const LocalhostSuffix = ".localhost"

// IsLocal reports whether the host resolves without a hosts-file entry.
func (h Host) IsLocal() bool { return strings.HasSuffix(string(h), LocalhostSuffix) }

// hostLabelPattern is one DNS label: 1-63 alphanumerics and hyphens, no
// leading or trailing hyphen.
var hostLabelPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

var digitsPattern = regexp.MustCompile(`^[0-9]+$`)

// maxHostLength is DNS's limit on a full name.
const maxHostLength = 253

// ParseHost applies DNS's rules plus two of tamp's: uppercase is rejected
// rather than folded (the hostname is also the site's directory name), and a
// bare label is rejected (it resolves nowhere in a browser). Failures are
// exit 1, not usage errors — the command line was well-formed.
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
	// The router matches the Host header, and a browser sent to an IP never
	// sends a name.
	if digitsPattern.MatchString(labels[len(labels)-1]) {
		return "", invalid("looks like an IP address, and the router routes by hostname",
			"give the site a name, like shop.localhost")
	}
	return Host(s), nil
}
