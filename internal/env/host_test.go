package env_test

import (
	"testing"

	"github.com/zhide915/tamp/internal/env"
	"github.com/zhide915/tamp/internal/exitcode"
)

// A site's hostname is three things at once — its name, its directory on the
// bench, and the address the router matches on — so what this accepts is what
// tamp is able to route, and what it rejects is a site nobody could reach.

func TestAHostnameTampCanRoute(t *testing.T) {
	for _, host := range []string{
		"shop.localhost",
		"books.shop.localhost",
		"abc.xyz.com",
		"a.b",
		"shop-2.localhost",
		"1shop.localhost",
	} {
		t.Run(host, func(t *testing.T) {
			got, err := env.ParseHost(host)
			if err != nil {
				t.Fatalf("ParseHost(%q) = %v", host, err)
			}
			if got.String() != host {
				t.Errorf("ParseHost(%q) = %q — tamp renamed the site", host, got)
			}
		})
	}
}

func TestAHostnameTampRefuses(t *testing.T) {
	cases := map[string]string{
		"nothing at all":                     "",
		"a single label resolves nowhere":    "shop",
		"uppercase is a different directory": "Shop.localhost",
		"a leading hyphen":                   "-shop.localhost",
		"a trailing hyphen":                  "shop-.localhost",
		"an empty label":                     "shop..localhost",
		"a trailing dot":                     "shop.localhost.",
		"an underscore":                      "my_shop.localhost",
		"a space":                            "my shop.localhost",
		"an IP address, which carries no name in the Host header": "127.0.0.1",
	}
	for why, host := range cases {
		t.Run(why, func(t *testing.T) {
			if _, err := env.ParseHost(host); err == nil {
				t.Fatalf("ParseHost(%q) = nil, want a refusal", host)
			} else if got := exitcode.Of(err); got != exitcode.CodeFailed {
				// The command line was well-formed; tamp just cannot put this
				// name where it has to go. That is exit 1, not a usage error.
				t.Errorf("ParseHost(%q) exits %d, want %d", host, got, exitcode.CodeFailed)
			}
		})
	}
}

// A label may be 63 characters and a name 253, and tamp has no business
// being stricter than DNS about either.
func TestAHostnameIsMeasuredTheWayDNSMeasuresOne(t *testing.T) {
	label := func(n int) string {
		s := make([]byte, n)
		for i := range s {
			s[i] = 'a'
		}
		return string(s)
	}

	if _, err := env.ParseHost(label(63) + ".localhost"); err != nil {
		t.Errorf("tamp refused a 63-character label: %v", err)
	}
	if _, err := env.ParseHost(label(64) + ".localhost"); err == nil {
		t.Error("tamp accepted a 64-character label")
	}
	// 63+1+63+1+63+1+61 = 253, the longest name DNS allows, and one more.
	limit := label(63) + "." + label(63) + "." + label(63) + "." + label(61)
	if _, err := env.ParseHost(limit); err != nil {
		t.Errorf("tamp refused a %d-character hostname: %v", len(limit), err)
	}
	if _, err := env.ParseHost(limit + "a"); err == nil {
		t.Errorf("tamp accepted a %d-character hostname", len(limit)+1)
	}
}

// Only .localhost resolves to loopback without anyone editing a file, and that
// is the difference the command reports to the user.
func TestOnlyALocalhostNameResolvesOnItsOwn(t *testing.T) {
	for host, want := range map[string]bool{
		"shop.localhost":       true,
		"books.shop.localhost": true,
		"abc.xyz.com":          false,
		"shop.localhost.dev":   false,
	} {
		if got := env.Host(host).IsLocal(); got != want {
			t.Errorf("Host(%q).IsLocal() = %v, want %v", host, got, want)
		}
	}
}
