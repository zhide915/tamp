package env

import (
	"bytes"
	"strings"
	"testing"

	"github.com/zhide915/tamp/internal/router"
	"github.com/zhide915/tamp/internal/ui"
)

// The router knows nothing about tamp.toml or the registry; this is the
// translation, and it is where a wrong container name would send every request
// for a site into the void.
func TestRoutesAddressEachEnvironmentsOwnContainers(t *testing.T) {
	reg := Registry{
		"demo": {Path: "/work/demo", Hash: "ab12cd", Sites: []string{"shop.localhost"}},
	}

	got := routes(reg)

	if len(got) != 1 {
		t.Fatalf("routes = %v, want one environment", got)
	}
	e := got[0]
	if e.Network != "tamp-demo-ab12cd" {
		t.Errorf("network = %q, want the environment's own", e.Network)
	}
	if e.Web != "tamp-demo-ab12cd-frappe-1:8000" {
		t.Errorf("web upstream = %q", e.Web)
	}
	if e.Socket != "tamp-demo-ab12cd-frappe-1:9000" {
		t.Errorf("socket.io upstream = %q", e.Socket)
	}
	if e.Mail != "tamp-demo-ab12cd-mailpit-1:8025" {
		t.Errorf("mail upstream = %q", e.Mail)
	}
	if len(e.Sites) != 1 || e.Sites[0] != "shop.localhost" {
		t.Errorf("sites = %v, want the registry's cached list", e.Sites)
	}
}

// The cached site list is what lets the routes of a stopped environment be
// assembled at all, so an environment with no sites yet is a normal case
// rather than a missing one — its mail UI is still routed.
func TestAnEnvironmentWithNoSitesStillHasRoutes(t *testing.T) {
	got := routes(Registry{"demo": {Path: "/work/demo", Hash: "ab12cd"}})

	if len(got) != 1 || got[0].Mail == "" {
		t.Fatalf("routes = %v, want a mail route", got)
	}
}

func announced(t *testing.T, status router.Status) string {
	t.Helper()
	var out bytes.Buffer
	m := &Manager{Out: &ui.Printer{Out: &out, Err: &out}}
	m.announceRoutes(&Environment{Config: &Config{Name: "demo"}}, status)
	return out.String()
}

func TestTheMailURLIsPrintedWithoutAPortOnTheDefaultOne(t *testing.T) {
	got := announced(t, router.Status{Running: true, Port: router.DefaultPort})

	if !strings.Contains(got, "http://mail.demo.localhost\n") {
		t.Errorf("output = %q, want a URL with no port in it", got)
	}
}

// A user whose port 80 was taken has to be told twice over: once that it
// happened, and once in every URL — a URL missing the port points at whatever
// else is on 80.
func TestTheFallbackPortIsAnnouncedAndCarriedByTheURLs(t *testing.T) {
	got := announced(t, router.Status{Running: true, Port: router.FallbackPort})

	if !strings.Contains(got, "port 80 was taken") {
		t.Errorf("output = %q, want a notice that port 80 was not available", got)
	}
	if !strings.Contains(got, "http://mail.demo.localhost:8080") {
		t.Errorf("output = %q, want the mail URL to carry the port", got)
	}
}
