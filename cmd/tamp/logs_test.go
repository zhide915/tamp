package main

import (
	"strings"
	"testing"

	"github.com/zhide915/tamp/internal/env"
	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/router"
)

// The user names services, never containers — and the five bench processes
// sharing one container must come back apart.

// benchLog mixes the two line shapes tamp must separate: a process's own
// output and honcho's system lines about it.
const benchLog = `12:00:01 system    | web.1 started (pid=41)
12:00:01 web.1     | * Running on http://0.0.0.0:8000
12:00:02 worker.1  | *** Listening on default ***
12:00:03 web.1     | 127.0.0.1 - - "GET / HTTP/1.1" 200
12:00:04 schedule.1 | scheduler enabled
12:00:05 system    | worker.1 stopped (rc=1)
`

func TestLogsShowsOnlyTheProcessAskedFor(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.engine.Log = benchLog

	r := c.run(t, "logs", "demo", "web")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "Running on http://0.0.0.0:8000", `"GET / HTTP/1.1" 200`)
	// honcho's system line about web belongs to web's log.
	r.assertStdoutContains(t, "web.1 started")
	for _, other := range []string{"Listening on default", "scheduler enabled", "worker.1 stopped"} {
		if strings.Contains(r.stdout, other) {
			t.Errorf("tamp showed another process's line %q in web's log:\n%s", other, r.stdout)
		}
	}
}

// Pre-honcho output (a failing entrypoint) is not in honcho's format but is
// exactly what a bench that will not start needs shown.
func TestLogsShowsWhatTheContainerSaidBeforeHonchoStarted(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.engine.Log = "sh: honcho: not found\n" + benchLog

	r := c.run(t, "logs", "demo", "web")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "sh: honcho: not found")
}

func TestLogsRefusesANegativeTail(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")

	r := c.run(t, "logs", "demo", "--tail", "-5")

	r.assertCode(t, exitcode.CodeUsage)
}

// A typoed environment must not silently show the router's log.
func TestLogsRouterStillChecksTheEnvironmentItWasGiven(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")

	r := c.run(t, "logs", "nonexistent", "router")

	r.assertCode(t, exitcode.CodeNotFound)
}

func TestLogsShowsTheWebProcessWhenToldOnlyTheEnvironment(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.engine.Log = benchLog

	r := c.run(t, "logs", "demo")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "Running on http://0.0.0.0:8000")
	if strings.Contains(r.stdout, "scheduler enabled") {
		t.Errorf("tamp defaulted to something other than %s:\n%s", env.DefaultLogService, r.stdout)
	}
}

// A service with its own container has no honcho in front to filter.
func TestLogsReadsTheContainerOfTheServiceAsked(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.engine.Log = "ready for connections\n"

	r := c.run(t, "logs", "demo", "mariadb")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "ready for connections")
	if got := c.lastLogContainer(t); !strings.HasSuffix(got, "-mariadb-1") {
		t.Errorf("tamp read %s, want the environment's mariadb container", got)
	}
}

// The router belongs to no environment, so no environment need resolve.
func TestLogsReadsTheRouterFromAnywhere(t *testing.T) {
	c := sandbox(t)
	c.engine.Log = "serving\n"

	r := c.run(t, "logs", "router")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "serving")
	if got := c.lastLogContainer(t); got != router.Container {
		t.Errorf("tamp read %s, want %s", got, router.Container)
	}
}

// Service names are not reserved environment names, so an environment called
// "web" keeps answering to its own name.
func TestLogsReadsABareWordAsTheEnvironmentWhenThereIsOne(t *testing.T) {
	c := sandbox(t)
	c.create(t, "web", "--sync", "bind")
	c.engine.Log = benchLog

	r := c.run(t, "logs", "web")

	r.assertCode(t, exitcode.CodeOK)
	if got := c.lastLogContainer(t); !strings.HasPrefix(got, "tamp-web-") {
		t.Errorf("tamp read %s, want the environment called web", got)
	}
	r.assertStdoutContains(t, "Running on http://0.0.0.0:8000")
}

func TestLogsSaysWhenABareWordIsNeitherThingItCouldBe(t *testing.T) {
	c := sandbox(t)

	r := c.run(t, "logs", "typo")

	r.assertCode(t, exitcode.CodeUsage)
	r.assertStderrContains(t, "typo", "neither an environment", "tamp list")
}

func TestLogsPassesFollowAndTailThrough(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")

	c.run(t, "logs", "demo", "mariadb", "-f", "--tail", "5").assertCode(t, exitcode.CodeOK)

	req := c.engine.LogReqs[len(c.engine.LogReqs)-1]
	if !req.Follow || req.Tail != 5 {
		t.Errorf("tamp asked for follow=%v tail=%d, want follow=true tail=5", req.Follow, req.Tail)
	}
}

func TestLogsRejectsAServiceTampHasNoLogFor(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")

	r := c.run(t, "logs", "demo", "nginx")

	r.assertCode(t, exitcode.CodeUsage)
	r.assertStderrContains(t, "nginx", "web", "socketio", "mariadb", "router")
}

func (c *cli) lastLogContainer(t *testing.T) string {
	t.Helper()
	if len(c.engine.LogReqs) == 0 {
		t.Fatal("tamp never asked for a container's log")
	}
	return c.engine.LogReqs[len(c.engine.LogReqs)-1].Container
}
