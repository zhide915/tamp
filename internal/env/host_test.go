package env_test

import (
	"testing"

	"github.com/zhide915/tamp/internal/env"
	"github.com/zhide915/tamp/internal/exitcode"
)

// A hostname is the site's name, its bench directory and its route at once:
// what ParseHost accepts is what tamp can route.

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
				// Exit 1, not a usage error: the command line was well-formed.
				t.Errorf("ParseHost(%q) exits %d, want %d", host, got, exitcode.CodeFailed)
			}
		})
	}
}

// tamp has no business being stricter than DNS: 63 per label, 253 per name.
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
	// 63+1+63+1+63+1+61 = 253, DNS's longest name.
	limit := label(63) + "." + label(63) + "." + label(63) + "." + label(61)
	if _, err := env.ParseHost(limit); err != nil {
		t.Errorf("tamp refused a %d-character hostname: %v", len(limit), err)
	}
	if _, err := env.ParseHost(limit + "a"); err == nil {
		t.Errorf("tamp accepted a %d-character hostname", len(limit)+1)
	}
}

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
