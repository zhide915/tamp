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

func newTestRouter(t *testing.T, fake *enginetest.Fake) *Router {
	t.Helper()
	r := New(t.TempDir(), fake)
	// Fixed answer: which ports the test machine has spare is irrelevant here.
	r.PortIsFree = func(int) bool { return true }
	return r
}

func demo() Env {
	return Env{
		Name:    "demo",
		Network: "tamp-demo-ab12cd",
		Web:     "tamp-demo-ab12cd-frappe-1:8000",
		Socket:  "tamp-demo-ab12cd-frappe-1:9000",
		Mail:    "tamp-demo-ab12cd-mailpit-1:8025",
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

// The mail route exists before the environment has any site.
func TestEveryEnvironmentGetsAMailRoute(t *testing.T) {
	got := Caddyfile([]Env{demo()})

	if !strings.Contains(got, "http://mail.demo.localhost {") {
		t.Errorf("no mail route for demo:\n%s", got)
	}
	if !strings.Contains(got, "reverse_proxy tamp-demo-ab12cd-mailpit-1:8025") {
		t.Errorf("the mail route does not reach mailpit:\n%s", got)
	}
}

// Without a separate socket.io route the desk loses live updates.
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

	// The socket.io matcher must precede the catch-all reverse_proxy.
	if strings.Index(got, "@socketio tamp-demo-ab12cd-frappe-1:9000") >
		strings.Index(got, "\treverse_proxy tamp-demo-ab12cd-frappe-1:8000") {
		t.Errorf("the socket.io route comes after the catch-all one:\n%s", got)
	}
}

func TestTheAssembledFileDoesNotDependOnTheOrderSitesArriveIn(t *testing.T) {
	a, b := demo(), demo()
	a.Sites = []string{"shop.localhost", "abc.localhost"}
	b.Sites = []string{"abc.localhost", "shop.localhost"}

	if Caddyfile([]Env{a}) != Caddyfile([]Env{b}) {
		t.Error("the same sites in a different order produced a different Caddyfile")
	}
}

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

// Routes for stopped environments are kept, so a missing network is routine.
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

func TestTheRouterFallsBackWhenPortEightyIsTaken(t *testing.T) {
	port, err := choosePort(func(p int) bool { return p != DefaultPort })
	if err != nil {
		t.Fatal(err)
	}
	if port != FallbackPort {
		t.Errorf("port = %d, want %d", port, FallbackPort)
	}
}

// A Windows excluded port range refuses the bind while the connect probe sees
// nothing, so a failed up on 80 must still reach the fallback.
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

	// The recorded port must be the one that actually worked.
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

func TestTheDefaultPortIsNotSpelledOutInURLs(t *testing.T) {
	got := Status{Running: true, Port: DefaultPort}.URL("shop.localhost")
	if want := "http://shop.localhost"; got != want {
		t.Errorf("URL = %q, want %q", got, want)
	}
}

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
