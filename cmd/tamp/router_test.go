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

// Hostname routing is decided by two things these tests read directly: the
// assembled Caddyfile and which networks the router container is on.

func (c *cli) caddyfile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(c.home, env.HomeDirName, router.DirName, router.CaddyfileName)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no assembled Caddyfile at %s: %v", path, err)
	}
	return string(body)
}

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

// Restarting the router would drop connections the first environment serves.
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

// Routes come from the registry, not from what happens to be running.
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
	// Detach must precede teardown: Docker refuses to remove a network
	// something is still attached to.
	detach := slices.Index(c.engine.Calls, "DisconnectNetwork")
	down := slices.Index(c.engine.Calls, "ComposeDown")
	if detach < 0 || down < 0 || detach > down {
		t.Errorf("rm detached the router after tearing the environment down: %v", c.engine.Calls)
	}
}

// No tamp container has a restart policy, so a reboot loses the router.
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

// The router is machine-global and may have gone since; the containers'
// no-op does not cover it.
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

// A URL without its real port points at whatever else took port 80.
func TestListPrintsTheRoutersRealPortWithDockerDown(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	// Simulate the router having taken the fallback port.
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

// With the router down, no hostname on the machine answers.
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
