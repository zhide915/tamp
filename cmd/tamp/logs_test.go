package main

import (
	"strings"
	"testing"

	"github.com/zhide915/tamp/internal/env"
	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/router"
)

// Observability without knowing anything about Docker: what these tests hold
// in place is that a service name is all the user ever supplies, and that the
// five processes sharing the bench's one container come back apart again.

// benchLog is what honcho writes when every process on a bench has something
// to say. The lines are the shapes tamp has to tell apart: a process's own
// output, and honcho's system line about that process.
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
	// honcho's own line about the process is part of that process's story.
	r.assertStdoutContains(t, "web.1 started")
	for _, other := range []string{"Listening on default", "scheduler enabled", "worker.1 stopped"} {
		if strings.Contains(r.stdout, other) {
			t.Errorf("tamp showed another process's line %q in web's log:\n%s", other, r.stdout)
		}
	}
}

// A line the container wrote before honcho started — the entrypoint failing,
// most likely — is not in honcho's format, and it is exactly what someone
// asking for any process's log needs to see when the bench will not start.
func TestLogsShowsWhatTheContainerSaidBeforeHonchoStarted(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.engine.Log = "sh: honcho: not found\n" + benchLog

	r := c.run(t, "logs", "demo", "web")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "sh: honcho: not found")
}

// --tail counts lines, and a negative count is a mistyped command line, not a
// request tamp can guess a meaning for.
func TestLogsRefusesANegativeTail(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")

	r := c.run(t, "logs", "demo", "--tail", "-5")

	r.assertCode(t, exitcode.CodeUsage)
}

// The router belongs to no environment, but an environment the user named
// still has to exist: a typo silently ignored would show the router's log as
// if the environment were there.
func TestLogsRouterStillChecksTheEnvironmentItWasGiven(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")

	r := c.run(t, "logs", "nonexistent", "router")

	r.assertCode(t, exitcode.CodeNotFound)
}

// One bare argument is a service when tamp has one by that name, and the
// environment otherwise — which is unambiguous because every service name is a
// reserved environment name.
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

// A service with a container of its own is shown whole: there is no honcho in
// front of it and nothing to filter.
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

// The router belongs to no environment, so this has to work from a directory
// tamp would otherwise refuse to resolve an environment in.
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

// Nothing stops an environment being called "web": tamp's reserved words are
// its command words, and are deliberately not derived from a list that grows.
// So the environment somebody made has to keep answering to its own name.
func TestLogsReadsABareWordAsTheEnvironmentWhenThereIsOne(t *testing.T) {
	c := sandbox(t)
	c.create(t, "web", "--sync", "bind")
	c.engine.Log = benchLog

	r := c.run(t, "logs", "web")

	r.assertCode(t, exitcode.CodeOK)
	if got := c.lastLogContainer(t); !strings.HasPrefix(got, "tamp-web-") {
		t.Errorf("tamp read %s, want the environment called web", got)
	}
	// Its default service still applies, and the service is still reachable by
	// saying both.
	r.assertStdoutContains(t, "Running on http://0.0.0.0:8000")
}

// A word that is neither has to say so as both, rather than sending the user
// to look for a service they never meant.
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

// Nothing was attempted and the command line itself was wrong, so this is a
// usage error — and the answer is the list of services.
func TestLogsRejectsAServiceTampHasNoLogFor(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")

	r := c.run(t, "logs", "demo", "nginx")

	r.assertCode(t, exitcode.CodeUsage)
	r.assertStderrContains(t, "nginx", "web", "socketio", "mariadb", "router")
}

// lastLogContainer is the container tamp most recently asked for a log of.
func (c *cli) lastLogContainer(t *testing.T) string {
	t.Helper()
	if len(c.engine.LogReqs) == 0 {
		t.Fatal("tamp never asked for a container's log")
	}
	return c.engine.LogReqs[len(c.engine.LogReqs)-1].Container
}
