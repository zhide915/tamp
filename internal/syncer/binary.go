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
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/zhide915/tamp/internal/exitcode"
)

// Version is the pinned Mutagen release. Bump it and checksums together, with
// the end-to-end suite green. Pinned, verifiable archives are also why tamp
// never pipes an install script from the network.
const Version = "0.18.1"

// checksums are the pinned archives' SHA-256 digests, keyed by Mutagen's
// <os>_<arch> naming. Mutagen runs on the host, so these are host platforms.
var checksums = map[string]string{
	"windows_amd64": "3e237e77f69959ed520a0f877330a431507bb0a85d9da7919764ba0c87b702c7",
	"windows_arm64": "9ac53447e46f019be9d37f49c00eeed8635966b885ed29ef06b3ff19afdee532",
	"darwin_amd64":  "7d06f7d8fcfe90bc7e55cc834a2f2f20c2e0af9ea9bc35911fc4341ad56a9bbf",
	"darwin_arm64":  "6f810416d9e5fc4fd5e18431146f8b3c5a2056ba5a24f76c1e66da86eb3257e2",
	"linux_amd64":   "7735286c778cc438418209f24d03a64f3a0151c8065ef0fe079cfaf093af6f8f",
	"linux_arm64":   "bcba735aebf8cbc11da9b3742118a665599ac697fa06bc5751cac8dcd540db8a",
}

// releaseBaseURL is the download source; CLI.ReleaseBaseURL overrides it in
// tests.
const releaseBaseURL = "https://github.com/mutagen-io/mutagen/releases/download"

// agentBundle holds the container-side agents. Mutagen finds it by looking
// next to its own executable, so it must be unpacked beside the binary.
const agentBundle = "mutagen-agents.tar.gz"

// BinDirName is tamp's managed-binary directory under the tamp home. The
// version sits in the path beneath it, so a pin bump installs alongside the
// old release instead of over it.
const BinDirName = "bin"

func (c *CLI) binDir() string { return filepath.Join(c.Home, BinDirName, "mutagen-"+Version) }

func (c *CLI) managedPath() string { return filepath.Join(c.binDir(), executableName()) }

func executableName() string {
	if runtime.GOOS == "windows" {
		return "mutagen.exe"
	}
	return "mutagen"
}

// Find prefers a PATH binary only at exactly the pinned version: Mutagen's
// client and daemon speak a versioned protocol and refuse each other across
// releases.
func (c *CLI) Find(ctx context.Context) (Binary, error) {
	lookPath := c.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	if path, err := lookPath("mutagen"); err == nil {
		if version, err := c.version(ctx, path); err == nil && version == Version {
			return Binary{Path: path, Version: version}, nil
		}
	}

	managed := c.managedPath()
	if _, err := os.Stat(managed); err == nil {
		return Binary{Path: managed, Version: Version, Managed: true}, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return Binary{}, exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot look at %s: %v", managed, err),
			"check the permissions on your ~/.tamp directory")
	}

	return Binary{}, exitcode.New(exitcode.CodeNotFound,
		fmt.Sprintf("this machine has no Mutagen %s", Version),
		"tamp downloads it into "+c.binDir()+" the first time it syncs an environment")
}

func (c *CLI) Ensure(ctx context.Context) (Binary, error) {
	found, err := c.Find(ctx)
	if err == nil {
		return found, nil
	}
	if exitcode.Of(err) != exitcode.CodeNotFound {
		return Binary{}, err
	}
	if err := c.download(ctx); err != nil {
		return Binary{}, err
	}
	return Binary{Path: c.managedPath(), Version: Version, Managed: true}, nil
}

func (c *CLI) version(ctx context.Context, path string) (string, error) {
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, path, "version")
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(out.String()), nil
}

// download fetches the pinned archive, refuses it on checksum mismatch, and
// unpacks it.
func (c *CLI) download(ctx context.Context) error {
	platform := runtime.GOOS + "_" + runtime.GOARCH
	want, ok := checksums[platform]
	if !ok {
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("tamp has no Mutagen build for %s", platform),
			"set the environment's sync mode to bind — it works, without hot reload")
	}

	name := fmt.Sprintf("mutagen_%s_v%s.tar.gz", platform, Version)
	body, err := download(ctx, fmt.Sprintf("%s/v%s/%s", c.baseURL(), Version, name))
	if err != nil {
		return err
	}
	digest := sha256.Sum256(body)
	if sum := hex.EncodeToString(digest[:]); sum != want {
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("%s does not match the checksum tamp ships: got %s, want %s", name, sum, want),
			"try again — if it keeps failing, do not use the download, and report it")
	}

	if err := os.MkdirAll(c.binDir(), 0o755); err != nil {
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot create %s: %v", c.binDir(), err),
			"check the permissions on your ~/.tamp directory")
	}
	return extract(body, c.binDir(), name)
}

func (c *CLI) baseURL() string {
	if c.ReleaseBaseURL != "" {
		return c.ReleaseBaseURL
	}
	return releaseBaseURL
}

// extract unpacks the executable and the agent bundle into dir. Both are
// required: without the bundle beside the binary, Mutagen can start no
// session.
func extract(body []byte, dir, name string) error {
	corrupt := func(err error) error {
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("the Mutagen %s download is not readable: %v", Version, err),
			"try again — the download matched its checksum, so this is a bug in tamp")
	}

	gz, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return corrupt(err)
	}
	defer func() { _ = gz.Close() }()

	found := 0
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return corrupt(err)
		}
		base := filepath.Base(header.Name)
		if header.Typeflag != tar.TypeReg || (base != executableName() && base != agentBundle) {
			continue
		}

		mode := fs.FileMode(0o644)
		if base == executableName() {
			mode = 0o755
		}
		file, err := io.ReadAll(tr)
		if err != nil {
			return corrupt(err)
		}
		if err := os.WriteFile(filepath.Join(dir, base), file, mode); err != nil {
			return exitcode.New(exitcode.CodeFailed,
				fmt.Sprintf("cannot write %s: %v", filepath.Join(dir, base), err),
				"check the permissions on your ~/.tamp directory")
		}
		found++
	}

	if found != 2 {
		return corrupt(fmt.Errorf("%s holds %d of the 2 files tamp needs", name, found))
	}
	return nil
}

func download(ctx context.Context, url string) ([]byte, error) {
	blocked := func(err error) error {
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot download %s: %v", url, err),
			"check this machine's internet connection, or set the environment's sync mode to bind")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot request %s: %v", url, err), "")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, blocked(err)
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode != http.StatusOK {
		return nil, blocked(fmt.Errorf("the server answered %s", res.Status))
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, blocked(err)
	}
	return body, nil
}
