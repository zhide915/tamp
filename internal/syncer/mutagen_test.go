package syncer

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/zhide915/tamp/internal/exitcode"
)

// standIn makes the test binary stand in for an installed Mutagen: it logs
// each call to the file standInLog names, and answers version and sync list.
const (
	standIn     = "TAMP_TEST_MUTAGEN_STAND_IN"
	standInLog  = "TAMP_TEST_MUTAGEN_LOG"
	standInList = "TAMP_TEST_MUTAGEN_LIST"
)

func TestMain(m *testing.M) {
	if os.Getenv(standIn) == "1" {
		os.Exit(standInMutagen())
	}
	os.Exit(m.Run())
}

func standInMutagen() int {
	args := os.Args[1:]
	if log := os.Getenv(standInLog); log != "" {
		cwd, _ := os.Getwd()
		f, err := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return 1
		}
		fmt.Fprintf(f, "%s|%s\n", cwd, strings.Join(args, " "))
		_ = f.Close()
	}
	switch {
	case len(args) == 1 && args[0] == "version":
		fmt.Println(Version)
	case len(args) >= 2 && args[0] == "sync" && args[1] == "list":
		list := os.Getenv(standInList)
		if list == "" {
			list = "[]"
		}
		fmt.Println(list)
	}
	return 0
}

// archive is a release archive holding the binary and the agents bundle.
func archive(t *testing.T, binary []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range map[string][]byte{exe(): binary, agents: []byte("agents")} {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func serve(t *testing.T, body []byte) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) }))
	t.Cleanup(srv.Close)
	return srv.URL + "/mutagen.tar.gz"
}

func sum(body []byte) string {
	h := sha256.Sum256(body)
	return hex.EncodeToString(h[:])
}

func assertFailed(t *testing.T, err error, msg, fix string) {
	t.Helper()
	var e *exitcode.Error
	if !errors.As(err, &e) || e.Code != exitcode.CodeFailed {
		t.Fatalf("Install = %v, want an exit 1 error", err)
	}
	if !strings.Contains(e.Msg, msg) || !strings.Contains(e.Fix, fix) {
		t.Errorf("Install = %q, want %q with the fix %q", e.Error(), msg, fix)
	}
}

func assertNotInstalled(t *testing.T, m *Mutagen) {
	t.Helper()
	if ok, err := m.Installed(context.Background()); err != nil || ok {
		t.Errorf("Installed = %v, %v, want false", ok, err)
	}
	entries, err := os.ReadDir(filepath.Join(m.Dir, "bin"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("Install left %v in %s", entries, filepath.Join(m.Dir, "bin"))
	}
}

func TestInstallRefusesAnArchiveWhoseChecksumDiffersAndInstallsNothing(t *testing.T) {
	body := archive(t, []byte("a program"))
	m := &Mutagen{Dir: t.TempDir(), URL: serve(t, body), SHA256: sum([]byte("the pinned archive"))}

	err := m.Install(context.Background())

	assertFailed(t, err, "has the SHA-256 "+sum(body)+", but tamp pins "+m.SHA256, "alters downloads")
	assertNotInstalled(t, m)
}

func TestInstallStopsWithAFixWhenTheBinaryCannotRun(t *testing.T) {
	body := archive(t, []byte("not a program"))
	m := &Mutagen{Dir: t.TempDir(), URL: serve(t, body), SHA256: sum(body)}

	err := m.Install(context.Background())

	assertFailed(t, err, "does not run", "security software")
	assertNotInstalled(t, m)
}

func TestInstallPutsAVerifiedMutagenAndItsAgentsInPlace(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	body := archive(t, binary)
	m := &Mutagen{Dir: t.TempDir(), URL: serve(t, body), SHA256: sum(body)}
	t.Setenv(standIn, "1")

	if err := m.Install(context.Background()); err != nil {
		t.Fatal(err)
	}

	if ok, err := m.Installed(context.Background()); err != nil || !ok {
		t.Errorf("Installed = %v, %v, want true", ok, err)
	}
	if _, err := os.Stat(filepath.Join(binDir(m.Dir), agents)); err != nil {
		t.Errorf("the agents bundle is not next to the binary: %v", err)
	}
}

func TestTheChecksumsArePinnedToTheReleasesSHA256SUMS(t *testing.T) {
	// From https://github.com/mutagen-io/mutagen/releases/download/v0.18.1/SHA256SUMS.
	want := map[string]string{
		"darwin_amd64":  "7d06f7d8fcfe90bc7e55cc834a2f2f20c2e0af9ea9bc35911fc4341ad56a9bbf",
		"darwin_arm64":  "6f810416d9e5fc4fd5e18431146f8b3c5a2056ba5a24f76c1e66da86eb3257e2",
		"windows_amd64": "3e237e77f69959ed520a0f877330a431507bb0a85d9da7919764ba0c87b702c7",
		"windows_arm64": "9ac53447e46f019be9d37f49c00eeed8635966b885ed29ef06b3ff19afdee532",
	}
	if Version != "0.18.1" || !maps.Equal(sums, want) {
		t.Errorf("Mutagen %s pins %v, want 0.18.1 with %v", Version, sums, want)
	}
}

func TestOnlyWindowsAndMacOSSync(t *testing.T) {
	for goos, syncs := range map[string]bool{"windows": true, "darwin": true, "linux": false} {
		if got := ForPlatform(goos) != nil; got != syncs {
			t.Errorf("ForPlatform(%q) syncs = %v, want %v", goos, got, syncs)
		}
	}
}

// listed is `mutagen sync list --template '{{json .}}'` from Mutagen 0.18.1,
// cut to the fields tamp reads, after apps/acme was deleted on the host.
const listed = `[{"identifier":"sync_x","alpha":{"protocol":"local","path":"C:\\envs\\erp-dev\\apps","connected":true},` +
	`"beta":{"protocol":"docker","host":"erp-dev-frappe-1","path":"/home/frappe/frappe-bench/apps","connected":true},` +
	`"mode":"two-way-resolved","name":"tamp-erp--dev","paused":false,"status":"watching",` +
	`"conflicts":[{"root":"acme","alphaChanges":[{"path":"acme","old":{"kind":"directory","contents":null},"new":null}],` +
	`"betaChanges":[{"path":"acme/node_modules","old":null,"new":{"kind":"untracked"}}]},` +
	`{"root":"hr","alphaChanges":[{"path":"hr/node_modules","old":null,"new":{"kind":"untracked"}}],` +
	`"betaChanges":[{"path":"hr","old":{"kind":"directory"},"new":null}]}]},` +
	`{"name":"tamp-other","paused":true,"status":"disconnected"}]`

func TestASessionIsReadFromMutagensListWithTheConflictsTheHostCaused(t *testing.T) {
	got, err := parseSession([]byte(listed), "tamp-erp--dev")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("parseSession found no session")
	}
	want := Session{Name: "tamp-erp--dev", Host: `C:\envs\erp-dev\apps`, Container: "erp-dev-frappe-1",
		Path: "/home/frappe/frappe-bench/apps", Status: "watching",
		Conflicts: []Conflict{{Path: "acme", HostDeleted: true}, {Path: "hr"}}}
	if got.Name != want.Name || got.Host != want.Host || got.Container != want.Container || got.Path != want.Path ||
		got.Paused || got.Status != want.Status || !slices.Equal(got.Conflicts, want.Conflicts) {
		t.Errorf("session = %+v, want %+v", *got, want)
	}
	if other, err := parseSession([]byte(listed), "tamp-other"); err != nil || other == nil || !other.Paused {
		t.Errorf("tamp-other = %+v, %v, want it paused", other, err)
	}
	if none, err := parseSession([]byte(listed), "tamp-none"); err != nil || none != nil {
		t.Errorf("tamp-none = %+v, %v, want no session", none, err)
	}
}

func TestACopyThatDoesNotRunCountsAsMissingAndIsReplaced(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	body := archive(t, binary)
	m := &Mutagen{Dir: t.TempDir(), URL: serve(t, body), SHA256: sum(body)}
	t.Setenv(standIn, "1")
	if err := m.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	installed := filepath.Join(binDir(m.Dir), exe())
	if err := os.WriteFile(installed, []byte("corrupt"), 0o755); err != nil {
		t.Fatal(err)
	}

	if ok, err := m.Installed(context.Background()); err != nil || ok {
		t.Fatalf("Installed = %v, %v for a corrupt copy, want false", ok, err)
	}
	if err := m.Install(context.Background()); err != nil {
		t.Fatalf("Install over a corrupt copy = %v", err)
	}
	if ok, err := m.Installed(context.Background()); err != nil || !ok {
		t.Errorf("Installed = %v, %v after the new install, want true", ok, err)
	}
	if stale, _ := filepath.Glob(filepath.Join(m.Dir, "bin", ".mutagen-*")); len(stale) != 0 {
		t.Errorf("temp folders left: %v", stale)
	}
}

func TestASessionCommandOnACopyThatCannotRunIsARunError(t *testing.T) {
	m := &Mutagen{Dir: t.TempDir()}
	if err := os.MkdirAll(binDir(m.Dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir(m.Dir), exe()), []byte("corrupt"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := m.Session(context.Background(), "tamp-erp--dev")

	var run *RunError
	if !errors.As(err, &run) {
		t.Errorf("Session = %v, want a *RunError", err)
	}
}

// installStandIn puts the test binary in place as m's Mutagen, and returns
// the log of the calls it gets, each as "<working directory>|<arguments>".
func installStandIn(t *testing.T, m *Mutagen, list string) func() []string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(binDir(m.Dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir(m.Dir), exe()), binary, 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(t.TempDir(), "calls")
	t.Setenv(standIn, "1")
	t.Setenv(standInLog, log)
	t.Setenv(standInList, list)
	return func() []string {
		body, err := os.ReadFile(log)
		if err != nil {
			t.Fatal(err)
		}
		return strings.Split(strings.TrimSuffix(string(body), "\n"), "\n")
	}
}

func TestReplicateEndsALeftCopyThenCopiesTheHostOneWayUntilItsFlushedAndEndsIt(t *testing.T) {
	m := &Mutagen{Dir: t.TempDir()}
	calls := installStandIn(t, m, `[{"name":"tamp-erp--dev-replica","paused":true,"status":"disconnected"}]`)
	host := filepath.Join(m.Dir, "erp-dev", "apps")
	spec := Spec{Name: "tamp-erp--dev", Host: host, Container: "erp-dev-frappe-1", Path: "/home/frappe/frappe-bench/apps",
		Ignore: []string{"node_modules", "/.*/"}}

	if err := m.Replicate(context.Background(), spec); err != nil {
		t.Fatal(err)
	}

	want := []string{
		"sync list --template {{json .}}",
		"sync terminate tamp-erp--dev-replica",
		"sync create --name tamp-erp--dev-replica --sync-mode one-way-replica --ignore node_modules --ignore /.*/ " +
			host + " docker://erp-dev-frappe-1/home/frappe/frappe-bench/apps",
		"sync flush tamp-erp--dev-replica",
		"sync terminate tamp-erp--dev-replica",
	}
	got := calls()
	if len(got) != len(want) {
		t.Fatalf("calls = %q, want %q", got, want)
	}
	for i, call := range got {
		cwd, args, _ := strings.Cut(call, "|")
		if args != want[i] {
			t.Errorf("call %d = %q, want %q", i, args, want[i])
		}
		// The daemon a call starts keeps that folder, which Windows then cannot move.
		if !sameDir(cwd, m.Dir) {
			t.Errorf("call %d ran in %s, want %s", i, cwd, m.Dir)
		}
	}
}

func TestCreateMakesATwoWaySessionWhereTheHostWins(t *testing.T) {
	m := &Mutagen{Dir: t.TempDir()}
	calls := installStandIn(t, m, "")
	spec := Spec{Name: "tamp-erp--dev", Host: "host-apps", Container: "c", Path: "/apps", Ignore: []string{"*.pyc"}}

	if err := m.Create(context.Background(), spec); err != nil {
		t.Fatal(err)
	}

	want := "sync create --name tamp-erp--dev --sync-mode two-way-resolved --ignore *.pyc host-apps docker://c/apps"
	if got := calls(); len(got) != 1 || !strings.HasSuffix(got[0], "|"+want) {
		t.Errorf("calls = %q, want %q", got, want)
	}
}

func sameDir(a, b string) bool {
	ai, err := os.Stat(a)
	if err != nil {
		return false
	}
	bi, err := os.Stat(b)
	return err == nil && os.SameFile(ai, bi)
}
