package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/zhide915/tamp/internal/engine/enginetest"
	"github.com/zhide915/tamp/internal/env"
	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/router"
)

// The router is what makes tamp's promise true from the outside: an
// environment is reached by hostname, and nothing on this machine is ever
// reached by typing a port. These tests drive the real commands and then read
// the two things that decide it — the assembled Caddyfile, and which networks
// the router container is on.

// caddyfile is the machine's assembled routes.
func (c *cli) caddyfile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(c.home, env.HomeDirName, router.DirName, router.CaddyfileName)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no assembled Caddyfile at %s: %v", path, err)
	}
	return string(body)
}

// network is the Docker network of an environment created in this sandbox.
func (c *cli) network(t *testing.T, name string) string {
	t.Helper()
	res, err := env.NewResources(env.Name(name), c.path(name))
	if err != nil {
		t.Fatal(err)
	}
	return res.Network()
}

func (c *cli) routerIsOn(t *testing.T, name string) bool {
	t.Helper()
	return slices.Contains(c.engine.Attached(c.network(t, name)), router.Container)
}

// The mail UI is reachable the moment the environment exists: no site, no
// port, nothing to look up.
func TestCreateStartsTheRouterAndRoutesTheMailUI(t *testing.T) {
	c := sandbox(t)

	r := c.run(t, "create", "demo", "--frappe", "version-15")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "http://mail.demo.localhost")

	if ups := c.routerOps("ComposeUp"); len(ups) != 1 {
		t.Fatalf("tamp ran %d compose ups on the router, want 1", len(ups))
	}
	if !strings.Contains(c.caddyfile(t), "http://mail.demo.localhost {") {
		t.Errorf("demo's mail UI is not routed:\n%s", c.caddyfile(t))
	}
	if !c.routerIsOn(t, "demo") {
		t.Error("the router is not attached to demo's network, so it can reach nothing in it")
	}
}

// There is one router per machine, however many environments it holds — and
// the second environment must not restart it, because that would drop every
// connection the first one is serving.
func TestASecondEnvironmentReusesTheOneRouter(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.engine.Ops = nil

	c.create(t, "other")

	if ups := c.routerOps("ComposeUp"); len(ups) != 0 {
		t.Errorf("the second create started the router again: %v", ups)
	}
	if !c.engine.Ran("caddy reload") {
		t.Error("the second create never reloaded the router with its routes")
	}
	for _, name := range []string{"demo", "other"} {
		if !strings.Contains(c.caddyfile(t), "http://mail."+name+".localhost {") {
			t.Errorf("%s is not routed:\n%s", name, c.caddyfile(t))
		}
	}
}

// This is the reason the routes are assembled from tamp's registry rather
// than from what happens to be running: one environment going down must not
// take another's routes with it.
func TestStoppingOneEnvironmentLeavesTheOthersRoutes(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.create(t, "other")

	c.run(t, "stop", "demo").assertCode(t, exitcode.CodeOK)

	for _, name := range []string{"demo", "other"} {
		if !strings.Contains(c.caddyfile(t), "http://mail."+name+".localhost {") {
			t.Errorf("%s lost its routes when demo stopped:\n%s", name, c.caddyfile(t))
		}
	}
}

// Removing an environment takes its routes and its network attachment, and
// nothing else. The attachment has to go first: Docker refuses to remove a
// network anything is still on.
func TestRemovingAnEnvironmentTakesOnlyItsRoutes(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.create(t, "other")
	demoNetwork := c.network(t, "demo")

	c.run(t, "rm", "demo", "--yes").assertCode(t, exitcode.CodeOK)

	if strings.Contains(c.caddyfile(t), "mail.demo.localhost") {
		t.Errorf("demo is still routed after rm:\n%s", c.caddyfile(t))
	}
	if !strings.Contains(c.caddyfile(t), "mail.other.localhost") {
		t.Errorf("removing demo took other's routes with it:\n%s", c.caddyfile(t))
	}
	if slices.Contains(c.engine.Attached(demoNetwork), router.Container) {
		t.Error("the router is still attached to the removed environment's network")
	}
	// Detached before the teardown, not after: the other order is one Docker
	// refuses.
	detach := slices.Index(c.engine.Calls, "DisconnectNetwork")
	down := slices.Index(c.engine.Calls, "ComposeDown")
	if detach < 0 || down < 0 || detach > down {
		t.Errorf("rm detached the router after tearing the environment down: %v", c.engine.Calls)
	}
}

// No container tamp runs has a restart policy, so a host reboot leaves the
// machine with no router. Starting any environment has to bring it back.
func TestStartBringsTheRouterBackWhenTheMachineHasLostIt(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.engine.Down(router.Project)
	c.engine.Ops = nil

	r := c.run(t, "start", "demo")

	r.assertCode(t, exitcode.CodeOK)
	if ups := c.routerOps("ComposeUp"); len(ups) != 1 {
		t.Errorf("start ran %d compose ups on the router, want 1", len(ups))
	}
}

// Starting an environment that is already up is a no-op for its containers —
// but not for the router, which is machine-global and may have gone since.
func TestStartRoutesEvenWhenTheEnvironmentIsAlreadyRunning(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.engine.Down(router.Project)
	c.engine.Ops = nil

	r := c.run(t, "start", "demo")

	r.assertStdoutContains(t, "already running", "http://mail.demo.localhost")
	if ups := c.routerOps("ComposeUp"); len(ups) != 1 {
		t.Errorf("an already-running environment did not bring the router back: %v", c.engine.Ops)
	}
}

func TestListReportsTheRouterAndEveryMailURL(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")

	r := c.run(t, "list")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "router", "running", "MAIL", "http://mail.demo.localhost")
}

// list is mostly a report on tamp's own state, and the router's port is part
// of it. A URL printed without the port it is actually on points at whatever
// else took port 80.
func TestListPrintsTheRoutersRealPortWithDockerDown(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	// The router took the fallback port on this machine, whatever the sandbox
	// found free when it started.
	writeRouterPort(t, c, router.FallbackPort)
	c.engine = enginetest.Unavailable()

	r := c.run(t, "list")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "http://mail.demo.localhost:8080")
}

func writeRouterPort(t *testing.T, c *cli, port int) {
	t.Helper()
	path := filepath.Join(c.home, env.HomeDirName, router.DirName, "router.json")
	body := fmt.Sprintf("{\n  \"port\": %d\n}\n", port)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// With the router down nothing on the machine answers to a hostname, which is
// the answer to the question that brought the user to doctor.
func TestDoctorReportsTheRouterState(t *testing.T) {
	c := sandbox(t)

	before := c.run(t, "doctor")
	before.assertCode(t, exitcode.CodeOK)
	before.assertStdoutContains(t, "! Router", "not running")

	c.create(t, "demo")

	after := c.run(t, "doctor")
	after.assertCode(t, exitcode.CodeOK)
	after.assertStdoutContains(t, "✓ Router", "running on http://localhost")
}
