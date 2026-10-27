package docker

import (
	"net/netip"
	"testing"
)

// The fake stands in for Publishing, so only a unit test reaches its filter.
func TestOnlyAPublishOn127001OrOnEveryAddressHoldsTheRoutersPort(t *testing.T) {
	for ip, want := range map[string]bool{
		"":          true,
		"0.0.0.0":   true,
		"::":        true,
		"127.0.0.1": true,
		"127.0.0.2": false,
		"::1":       false,
		"10.0.0.5":  false,
	} {
		var addr netip.Addr
		if ip != "" {
			addr = netip.MustParseAddr(ip)
		}
		if got := clashesWithLoopback(addr); got != want {
			t.Errorf("a publish on %q holds 127.0.0.1 = %v, want %v", ip, got, want)
		}
	}
}
