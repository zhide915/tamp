package toolchain

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The pin is tamp's whole supply chain for uv: a version with no checksum
// beside it, or a checksum that is not a SHA-256, is a download nothing
// actually verifies.
func TestEverySupportedArchitectureHasAPinnedChecksum(t *testing.T) {
	for _, arch := range []string{"x86_64", "aarch64", "arm64"} {
		target, err := uvTarget(arch)
		if err != nil {
			t.Fatalf("uvTarget(%q) = %v", arch, err)
		}
		sum, ok := uvChecksums[target]
		if !ok {
			t.Fatalf("no checksum pinned for %s", target)
		}
		if _, err := hex.DecodeString(sum); err != nil || len(sum) != 64 {
			t.Errorf("%s checksum %q is not a SHA-256 digest", target, sum)
		}
	}
}

// A container architecture tamp has no build for has to say so, rather than
// download something that will not run.
func TestAnUnknownArchitectureIsRefused(t *testing.T) {
	_, err := uvTarget("riscv64")
	if err == nil {
		t.Fatal("uvTarget(riscv64) = nil, want an error")
	}
	if !strings.Contains(err.Error(), "x86_64") {
		t.Errorf("error %q does not name what tamp does support", err)
	}
}

// The point of pinning: what comes off the network is used only if it is
// exactly the bytes tamp was built against.
func TestADownloadIsUsedOnlyWhenItMatchesThePinnedChecksum(t *testing.T) {
	release := uvTarball(t, "uv-x86_64-unknown-linux-gnu", "#!/uv\n")
	digest := sha256.Sum256(release)
	url := serveRelease(t, release)

	t.Run("a matching tarball yields the uv binary", func(t *testing.T) {
		body, err := fetchRelease(t.Context(), url, hex.EncodeToString(digest[:]), "uv.tar.gz")
		if err != nil {
			t.Fatalf("fetchRelease = %v", err)
		}
		if string(body) != "#!/uv\n" {
			t.Errorf("fetchRelease returned %q, want the uv binary", body)
		}
	})

	t.Run("a tarball that does not match is refused", func(t *testing.T) {
		wrong := strings.Repeat("0", 64)
		_, err := fetchRelease(t.Context(), url, wrong, "uv.tar.gz")
		if err == nil {
			t.Fatal("fetchRelease accepted a tarball that did not match the pin")
		}
		if !strings.Contains(err.Error(), "checksum") {
			t.Errorf("error %q does not say the checksum was the problem", err)
		}
	})
}

// The release carries uvx alongside uv, and only one of them is tamp's.
func TestOnlyTheUVBinaryIsTakenFromTheRelease(t *testing.T) {
	release := uvTarball(t, "uv-x86_64-unknown-linux-gnu", "the real uv")
	body, err := extractUV(release, "uv.tar.gz")
	if err != nil {
		t.Fatalf("extractUV = %v", err)
	}
	if string(body) != "the real uv" {
		t.Errorf("extractUV returned %q, want the uv binary rather than uvx", body)
	}
}

// uvTarball builds a stand-in for a uv release: the directory, uvx, and uv, in
// the order the real one has them.
func uvTarball(t *testing.T, dir, uv string) []byte {
	t.Helper()

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	write := func(name, body string, mode int64, kind byte) {
		t.Helper()
		header := &tar.Header{Typeflag: kind, Name: name, Mode: mode, Size: int64(len(body))}
		if err := tw.WriteHeader(header); err != nil {
			t.Fatalf("cannot write %s: %v", name, err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatalf("cannot write %s: %v", name, err)
		}
	}
	write(dir+"/", "", 0o755, tar.TypeDir)
	write(dir+"/uvx", "not the one tamp wants", 0o755, tar.TypeReg)
	write(dir+"/uv", uv, 0o755, tar.TypeReg)

	if err := tw.Close(); err != nil {
		t.Fatalf("cannot close the tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("cannot close the gzip: %v", err)
	}
	return buf.Bytes()
}

// serveRelease stands in for the release host, so the test never reaches the
// network. It returns the URL of the tarball it is serving.
func serveRelease(t *testing.T, body []byte) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)
	return server.URL + "/uv.tar.gz"
}

// A download that is not there at all is a network problem, not a checksum
// one, and the message has to say which.
func TestAMissingReleaseIsReportedAsADownloadFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	t.Cleanup(server.Close)

	_, err := fetchRelease(t.Context(), server.URL+"/uv.tar.gz", strings.Repeat("0", 64), "uv.tar.gz")
	if err == nil {
		t.Fatal("fetchRelease = nil, want an error")
	}
	if !strings.Contains(err.Error(), "download") {
		t.Errorf("error %q does not read as a download failure", err)
	}
}
