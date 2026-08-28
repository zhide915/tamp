package syncer

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/zhide915/tamp/internal/exitcode"
)

func TestFindReportsNoMutagenOnAMachineThatHasNone(t *testing.T) {
	c := &CLI{Home: t.TempDir(), LookPath: nothingOnPath}

	_, err := c.Find(context.Background())

	if exitcode.Of(err) != exitcode.CodeNotFound {
		t.Fatalf("Find = %v, want a not-found error", err)
	}
	// The error must say tamp installs it itself, not point the user at an
	// installer.
	if !bytes.Contains([]byte(err.Error()), []byte("tamp downloads it")) {
		t.Errorf("Find does not say tamp handles this itself: %v", err)
	}
}

func TestFindReportsTheBinaryTampAlreadyDownloaded(t *testing.T) {
	c := &CLI{Home: t.TempDir(), LookPath: nothingOnPath}
	install(t, c)

	binary, err := c.Find(context.Background())

	if err != nil {
		t.Fatalf("Find = %v", err)
	}
	if !binary.Managed || binary.Version != Version {
		t.Errorf("Find = %+v, want tamp's own Mutagen %s", binary, Version)
	}
}

func TestEnsureRefusesADownloadThatDoesNotMatchTheChecksum(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not the release tamp pinned"))
	}))
	defer server.Close()

	c := &CLI{Home: t.TempDir(), ReleaseBaseURL: server.URL, LookPath: nothingOnPath}

	_, err := c.Ensure(context.Background())

	if err == nil {
		t.Fatal("Ensure accepted an archive that is not the pinned release")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("checksum")) {
		t.Errorf("Ensure failed for the wrong reason: %v", err)
	}
	if _, err := os.Stat(c.managedPath()); !errors.Is(err, fs.ErrNotExist) {
		t.Error("Ensure left a binary behind from an archive it refused")
	}
}

// A blocked download is not fatal — the error must name the bind fallback.
func TestEnsureSaysWhatToDoWhenTheDownloadIsBlocked(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "blocked by proxy", http.StatusForbidden)
	}))
	defer server.Close()

	c := &CLI{Home: t.TempDir(), ReleaseBaseURL: server.URL, LookPath: nothingOnPath}

	_, err := c.Ensure(context.Background())

	if err == nil {
		t.Fatal("Ensure = nil, want the download to have failed")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("bind")) {
		t.Errorf("the error does not name the fallback: %v", err)
	}
}

// Mutagen locates its container-side agents next to its own executable.
func TestExtractPutsTheAgentBundleBesideTheExecutable(t *testing.T) {
	dir := t.TempDir()

	if err := extract(release(t), dir, "release.tar.gz"); err != nil {
		t.Fatalf("extract = %v", err)
	}

	for _, name := range []string{executableName(), agentBundle} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s is not in %s: %v", name, dir, err)
		}
	}
}

func TestExtractRefusesAnArchiveMissingHalfOfMutagen(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	writeEntry(t, tw, executableName(), "binary")
	closeArchive(t, tw, gz)

	if err := extract(buf.Bytes(), t.TempDir(), "release.tar.gz"); err == nil {
		t.Fatal("extract accepted an archive with no agent bundle")
	}
}

// nothingOnPath hides whatever Mutagen the test machine has.
func nothingOnPath(string) (string, error) { return "", fmt.Errorf("not found") }

// install plants a managed binary where a download would land.
func install(t *testing.T, c *CLI) {
	t.Helper()
	if err := os.MkdirAll(c.binDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.managedPath(), []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// release builds the two-file archive a real Mutagen release is.
func release(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	writeEntry(t, tw, executableName(), "binary")
	writeEntry(t, tw, agentBundle, "agents")
	closeArchive(t, tw, gz)
	return buf.Bytes()
}

func writeEntry(t *testing.T, tw *tar.Writer, name, body string) {
	t.Helper()
	header := &tar.Header{Typeflag: tar.TypeReg, Name: name, Mode: 0o755, Size: int64(len(body))}
	if err := tw.WriteHeader(header); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
}

func closeArchive(t *testing.T, tw *tar.Writer, gz *gzip.Writer) {
	t.Helper()
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
}
