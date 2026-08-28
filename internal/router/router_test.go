package router

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/zhide915/tamp/internal/engine/enginetest"
	"github.com/zhide915/tamp/internal/exitcode"
)

// These tests hold the router to its two promises: that a hostname reaches the
// right container inside an environment that publishes nothing, and that one
// environment's comings and goings never take another's routes away.

func newTestRouter(t *testing.T, fake *enginetest.Fake) *Router {
	t.Helper()
	r := New(t.TempDir(), fake)
	// Settled rather than probed: which ports the machine running the test has
	// spare is not what any of these tests is about.
	r.PortIsFree = func(int) bool { return true }
	return r
}

// demo is an environment as the router sees one.
func demo() Env {
	return Env{
		Name:    "demo",
		Network: "tamp-demo-ab12cd",
		Web:     "tamp-demo-ab12cd-frappe-1:8000",
		Socket:  "tamp-demo-ab12cd-frappe-1:9000",
		Mail:    "tamp-demo-ab12cd-mailpit-1:8025",
	}
}

func other() Env {
	return Env{
		Name:    "other",
		Network: "tamp-other-ef34gh",
		Web:     "tamp-other-ef34gh-frappe-1:8000",
		Socket:  "tamp-other-ef34gh-frappe-1:9000",
		Mail:    "tamp-other-ef34gh-mailpit-1:8025",
	}
}

func (r *Router) assembled(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(r.caddyfilePath())
	if err != nil {
		t.Fatalf("no assembled Caddyfile: %v", err)
	}
	return string(body)
}

// --- the assembled Caddyfile ------------------------------------------------

// The mail UI is the one route an environment has before it has any site, and
// it is what makes "no ports typed" true from the moment the environment
// exists.
func TestEveryEnvironmentGetsAMailRoute(t *testing.T) {
	got := Caddyfile([]Env{demo()})

	if !strings.Contains(got, "http://mail.demo.localhost {") {
		t.Errorf("no mail route for demo:\n%s", got)
	}
	if !strings.Contains(got, "reverse_proxy tamp-demo-ab12cd-mailpit-1:8025") {
		t.Errorf("the mail route does not reach mailpit:\n%s", got)
	}
}

// Frappe serves the site and socket.io on two different ports, and the desk
// stops updating in real time if the second one is not routed separately.
func TestASiteIsRoutedToBothTheWebServerAndSocketIO(t *testing.T) {
	e := demo()
	e.Sites = []string{"shop.localhost"}

	got := Caddyfile([]Env{e})

	for _, want := range []string{
		"http://shop.localhost {",
		"@socketio path /socket.io /socket.io/*",
		"reverse_proxy @socketio tamp-demo-ab12cd-frappe-1:9000",
		"reverse_proxy tamp-demo-ab12cd-frappe-1:8000",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the site route is missing %q:\n%s", want, got)
		}
	}

	// The socket.io route has to be matched before the catch-all one, or every
	// websocket goes to the web server instead.
	if strings.Index(got, "@socketio tamp-demo-ab12cd-frappe-1:9000") >
		strings.Index(got, "\treverse_proxy tamp-demo-ab12cd-frappe-1:8000") {
		t.Errorf("the socket.io route comes after the catch-all one:\n%s", got)
	}
}

// The whole point of assembling every environment at once: what one
// environment is doing cannot subtract from another's routes.
func TestEnvironmentsRoutesCoexist(t *testing.T) {
	got := Caddyfile([]Env{demo(), other()})

	for _, host := range []string{"mail.demo.localhost", "mail.other.localhost"} {
		if !strings.Contains(got, "http://"+host+" {") {
			t.Errorf("no route for %s:\n%s", host, got)
		}
	}
}

// The file is rewritten on every environment and site change, so an unstable
// ordering would make every one of them look like a change.
func TestTheAssembledFileDoesNotDependOnTheOrderSitesArriveIn(t *testing.T) {
	a, b := demo(), demo()
	a.Sites = []string{"shop.localhost", "abc.localhost"}
	b.Sites = []string{"abc.localhost", "shop.localhost"}

	if Caddyfile([]Env{a}) != Caddyfile([]Env{b}) {
		t.Error("the same sites in a different order produced a different Caddyfile")
	}
}

// A machine with no environments still has a router, and it has to be a
// configuration Caddy can serve.
func TestAnEmptyMachineStillProducesRoutableConfiguration(t *testing.T) {
	got := Caddyfile(nil)

	if !strings.Contains(got, ":80 {") {
		t.Errorf("nothing listens at all:\n%s", got)
	}
	if !strings.Contains(got, "respond") {
		t.Errorf("an unrouted hostname gets no answer:\n%s", got)
	}
}

// --- the container ----------------------------------------------------------

func TestApplyStartsTheRouterWhenTheMachineHasNone(t *testing.T) {
	fake := enginetest.Running()
	r := newTestRouter(t, fake)
	fake.Up(demo().Network) // the environment is up; its network exists

	status, err := r.Apply(context.Background(), []Env{demo()}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if !status.Running || status.Port != DefaultPort {
		t.Errorf("status = %+v, want a router running on %d", status, DefaultPort)
	}
	ups := 0
	for _, op := range fake.Ops {
		if op.Method == "ComposeUp" && op.Project.Name == Project {
			ups++
		}
	}
	if ups != 1 {
		t.Errorf("tamp ran %d compose ups on the router, want 1", ups)
	}
	if got := fake.Attached(demo().Network); !slices.Contains(got, Container) {
		t.Errorf("the router is not on the environment's network; it holds %v", got)
	}
}

// A reload keeps every connection the router is already serving. Recreating
// the container to pick up new routes would drop all of them, and every tamp
// command that touches a site reaches this path.
func TestApplyReloadsARunningRouterInsteadOfRestartingIt(t *testing.T) {
	fake := enginetest.Running()
	r := newTestRouter(t, fake)
	fake.Up(Project)
	fake.Up(demo().Network)

	if _, err := r.Apply(context.Background(), []Env{demo()}, nil); err != nil {
		t.Fatal(err)
	}

	if slices.Contains(fake.Calls, "ComposeUp") {
		t.Errorf("tamp restarted a router that was already running: %v", fake.Calls)
	}
	if !fake.Ran("caddy reload") {
		t.Errorf("tamp never reloaded the router: %v", fake.Calls)
	}
}

// Removing an environment is no reason to start a router this machine was not
// using — but its routes still have to go.
func TestRefreshLeavesAStoppedRouterStopped(t *testing.T) {
	fake := enginetest.Running()
	r := newTestRouter(t, fake)

	status, err := r.Refresh(context.Background(), []Env{demo()})
	if err != nil {
		t.Fatal(err)
	}

	if status.Running {
		t.Error("Refresh reported a router it never started as running")
	}
	if slices.Contains(fake.Calls, "ComposeUp") {
		t.Errorf("Refresh started the router: %v", fake.Calls)
	}
	if !strings.Contains(r.assembled(t), "mail.demo.localhost") {
		t.Error("Refresh did not write the routes it was given")
	}
}

// The router carries routes for stopped environments, so it is routinely asked
// to attach to a network that is not there. That is an answer, not a failure.
func TestAttachingToAnEnvironmentThatIsNotUpIsNotAnError(t *testing.T) {
	fake := enginetest.Running()
	r := newTestRouter(t, fake)

	if err := r.Attach(context.Background(), "tamp-demo-ab12cd"); err != nil {
		t.Errorf("attaching to a network that is gone failed: %v", err)
	}
	if slices.Contains(fake.Calls, "ConnectNetwork") {
		t.Errorf("tamp tried to attach to a network that does not exist: %v", fake.Calls)
	}
}

func TestAttachingTwiceConnectsOnce(t *testing.T) {
	fake := enginetest.Running()
	r := newTestRouter(t, fake)
	fake.Up(demo().Network)
	ctx := context.Background()

	if err := r.Attach(ctx, demo().Network); err != nil {
		t.Fatal(err)
	}
	if err := r.Attach(ctx, demo().Network); err != nil {
		t.Fatal(err)
	}

	connects := 0
	for _, call := range fake.Calls {
		if call == "ConnectNetwork" {
			connects++
		}
	}
	if connects != 1 {
		t.Errorf("tamp connected %d times, want 1", connects)
	}
}

// Docker refuses to remove a network that still has something attached, so an
// environment's teardown depends on this happening first.
func TestDetachTakesTheRouterOffTheNetwork(t *testing.T) {
	fake := enginetest.Running()
	r := newTestRouter(t, fake)
	fake.Up(demo().Network)
	ctx := context.Background()
	if err := r.Attach(ctx, demo().Network); err != nil {
		t.Fatal(err)
	}

	if err := r.Detach(ctx, demo().Network); err != nil {
		t.Fatal(err)
	}

	if got := fake.Attached(demo().Network); slices.Contains(got, Container) {
		t.Errorf("the router is still on the network: %v", got)
	}
}

// --- the host port ----------------------------------------------------------

func TestTheRouterTakesPortEightyWhenItCan(t *testing.T) {
	port, err := choosePort(func(int) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if port != DefaultPort {
		t.Errorf("port = %d, want %d", port, DefaultPort)
	}
}

// A machine already serving something on port 80 is common, and it is no
// reason for tamp to have no router at all.
func TestTheRouterFallsBackWhenPortEightyIsTaken(t *testing.T) {
	port, err := choosePort(func(p int) bool { return p != DefaultPort })
	if err != nil {
		t.Fatal(err)
	}
	if port != FallbackPort {
		t.Errorf("port = %d, want %d", port, FallbackPort)
	}
}

// A port can pass the connect probe and still refuse the bind — Windows keeps
// excluded port ranges that swallow 80 with nothing listening on it. The
// fallback has to cover that case too, or the likeliest "80 can't bind"
// machine gets no router at all.
func TestTheRouterFallsBackWhenPortEightyRefusesTheBind(t *testing.T) {
	fake := enginetest.Running()
	fake.UpErrOnce = exitcode.New(exitcode.CodeFailed, "bind: access denied", "")
	r := newTestRouter(t, fake)

	status, err := r.Apply(context.Background(), []Env{demo()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Running || status.Port != FallbackPort {
		t.Errorf("status = %+v, want running on %d", status, FallbackPort)
	}

	// The remembered port has to be the one that worked, or every URL tamp
	// prints from now on points at the port that refused.
	st, err := r.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Port != FallbackPort {
		t.Errorf("remembered port = %d, want %d", st.Port, FallbackPort)
	}
}

func TestNoUsablePortIsAFailureWithAFix(t *testing.T) {
	_, err := choosePort(func(int) bool { return false })

	if exitcode.Of(err) != exitcode.CodeFailed {
		t.Fatalf("err = %v, want a failure", err)
	}
	if !strings.Contains(err.Error(), "8080") {
		t.Errorf("the error does not say which ports tamp tried: %v", err)
	}
}

// The URLs tamp prints have to carry the fallback port, or they point at
// whatever else is on 80.
func TestTheFallbackPortReachesEveryURL(t *testing.T) {
	fake := enginetest.Running()
	r := newTestRouter(t, fake)
	r.PortIsFree = func(p int) bool { return p != DefaultPort }

	status, err := r.Apply(context.Background(), []Env{demo()}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if status.Port != FallbackPort {
		t.Fatalf("port = %d, want %d", status.Port, FallbackPort)
	}
	if got, want := status.URL(MailHost("demo")), "http://mail.demo.localhost:8080"; got != want {
		t.Errorf("URL = %q, want %q", got, want)
	}

	compose, err := os.ReadFile(filepath.Join(r.Dir, composeFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(compose), "127.0.0.1:8080:80") {
		t.Errorf("the router does not publish the fallback port:\n%s", compose)
	}
}

// An unreachable engine costs the "is it up" half of the answer and nothing
// else. The port is tamp's own record, and a status that dropped it would
// print URLs on port 80 — pointing at whatever else took it.
func TestAnUnreachableEngineStillReportsTheRoutersPort(t *testing.T) {
	fake := enginetest.Running()
	r := newTestRouter(t, fake)
	r.PortIsFree = func(p int) bool { return p != DefaultPort }
	if _, err := r.Apply(context.Background(), []Env{demo()}, nil); err != nil {
		t.Fatal(err)
	}

	down := New(filepath.Dir(r.Dir), enginetest.Unavailable())
	status, err := down.Status(context.Background())

	if err == nil {
		t.Fatal("Status hid an unreachable engine")
	}
	if status.Port != FallbackPort {
		t.Errorf("port = %d, want the %d tamp recorded", status.Port, FallbackPort)
	}
}

// Port 80 is the product: a URL nobody has to remember a number for.
func TestTheDefaultPortIsNotSpelledOutInURLs(t *testing.T) {
	got := Status{Running: true, Port: DefaultPort}.URL("shop.localhost")
	if want := "http://shop.localhost"; got != want {
		t.Errorf("URL = %q, want %q", got, want)
	}
}

// The port is settled once, when the router starts, and every later command
// prints URLs from it — including on a machine whose Docker has since stopped.
func TestTheRouterRemembersItsPortForTheNextCommand(t *testing.T) {
	fake := enginetest.Running()
	r := newTestRouter(t, fake)
	r.PortIsFree = func(p int) bool { return p != DefaultPort }
	if _, err := r.Apply(context.Background(), []Env{demo()}, nil); err != nil {
		t.Fatal(err)
	}

	// A second command, reading only what the first left on disk.
	next := New(filepath.Dir(r.Dir), enginetest.Running())
	status, err := next.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if status.Port != FallbackPort {
		t.Errorf("port = %d, want the %d the router actually took", status.Port, FallbackPort)
	}
}
