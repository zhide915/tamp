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

// A missing or malformed checksum would leave the download unverified.
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

func TestAnUnknownArchitectureIsRefused(t *testing.T) {
	_, err := uvTarget("riscv64")
	if err == nil {
		t.Fatal("uvTarget(riscv64) = nil, want an error")
	}
	if !strings.Contains(err.Error(), "x86_64") {
		t.Errorf("error %q does not name what tamp does support", err)
	}
}

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

// uvTarball mimics a real release: the directory, then uvx, then uv.
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

// serveRelease keeps the test off the network.
func serveRelease(t *testing.T, body []byte) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)
	return server.URL + "/uv.tar.gz"
}
