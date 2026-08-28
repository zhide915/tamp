package toolchain

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"path"

	"github.com/zhide915/tamp/internal/exitcode"
)

// uvVersion is pinned together with uvChecksums: bump all three lines as one,
// with the end-to-end suite green. The pin is why tamp never runs uv's shell
// installer — a piped download cannot be verified against anything.
const uvVersion = "0.12.6"

// uvChecksums are the published SHA-256 digests of the pinned release's Linux
// tarballs, keyed by uv target triple. Only Linux: the binary runs inside the
// bench image whatever the host is.
var uvChecksums = map[string]string{
	"x86_64-unknown-linux-gnu":  "8681d8921e7d520fb368991dcf5f9c1905b80f5bf2a265a0ed085c8d8e342477",
	"aarch64-unknown-linux-gnu": "d58030acd26159499ac82f32da12d1b3c12a3a1bfc414232d9082070c03e128d",
}

// releaseBaseURL is a fetchUV parameter so tests can serve the tarball locally.
const releaseBaseURL = "https://github.com/astral-sh/uv/releases/download"

// uvDir embeds the version in the path, so a bumped pin installs alongside
// the old one instead of deciding whether what is there is stale.
func uvDir() string { return Dir + "/uv/" + uvVersion }

func uvPath() string { return uvDir() + "/uv" }

// uvTarget maps `uname -m` output to uv's target triple.
func uvTarget(arch string) (string, error) {
	switch arch {
	case "x86_64":
		return "x86_64-unknown-linux-gnu", nil
	case "aarch64", "arm64":
		return "aarch64-unknown-linux-gnu", nil
	}
	return "", exitcode.New(exitcode.CodeFailed,
		fmt.Sprintf("tamp has no uv build for a %s container", arch),
		"tamp supports x86_64 and aarch64 Linux containers")
}

func fetchUV(ctx context.Context, baseURL, arch string) ([]byte, error) {
	target, err := uvTarget(arch)
	if err != nil {
		return nil, err
	}
	name := "uv-" + target + ".tar.gz"
	url := fmt.Sprintf("%s/%s/%s", baseURL, uvVersion, name)
	return fetchRelease(ctx, url, uvChecksums[target], name)
}

// fetchRelease downloads the tarball, refuses it unless it matches the
// shipped digest, and returns the uv binary from inside it. A mismatch is
// never unpacked.
func fetchRelease(ctx context.Context, url, want, name string) ([]byte, error) {
	body, err := download(ctx, url)
	if err != nil {
		return nil, err
	}

	digest := sha256.Sum256(body)
	if sum := hex.EncodeToString(digest[:]); sum != want {
		return nil, exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("%s does not match the checksum tamp ships: got %s, want %s", name, sum, want),
			"try again — if it keeps failing, do not use the download, and report it")
	}
	return extractUV(body, name)
}

func download(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot request %s: %v", url, err), "")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot download %s: %v", url, err),
			"check this machine's internet connection and try again")
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode != http.StatusOK {
		return nil, exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot download %s: the server answered %s", url, res.Status),
			"check this machine's internet connection and try again")
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot download %s: %v", url, err),
			"check this machine's internet connection and try again")
	}
	return body, nil
}

// extractUV takes uv from the tarball, which also ships uvx.
func extractUV(body []byte, name string) ([]byte, error) {
	corrupt := func(err error) error {
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("the uv %s download is not readable: %v", uvVersion, err),
			"try again — the download matched its checksum, so this is a bug in tamp")
	}

	gz, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return nil, corrupt(err)
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, corrupt(err)
		}
		if header.Typeflag == tar.TypeReg && path.Base(header.Name) == "uv" {
			return io.ReadAll(tr)
		}
	}
	return nil, corrupt(fmt.Errorf("no uv binary in %s", name))
}
