package syncer

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/zhide915/tamp/internal/exitcode"
)

// Version is the pinned Mutagen release.
const Version = "0.18.1"

// sums are the SHA-256 sums of the pinned release's archives, from its
// SHA256SUMS, for the platforms that sync.
var sums = map[string]string{
	"darwin_amd64":  "7d06f7d8fcfe90bc7e55cc834a2f2f20c2e0af9ea9bc35911fc4341ad56a9bbf",
	"darwin_arm64":  "6f810416d9e5fc4fd5e18431146f8b3c5a2056ba5a24f76c1e66da86eb3257e2",
	"windows_amd64": "3e237e77f69959ed520a0f877330a431507bb0a85d9da7919764ba0c87b702c7",
	"windows_arm64": "9ac53447e46f019be9d37f49c00eeed8635966b885ed29ef06b3ff19afdee532",
}

// agents is the bundle of Mutagen's agents, which it copies into the
// container. Mutagen looks for it next to its binary.
const agents = "mutagen-agents.tar.gz"

// Mutagen is the real Client. Its zero value keeps Mutagen in ~/.tamp.
type Mutagen struct {
	// Dir holds Mutagen's binary and its data; empty means ~/.tamp.
	Dir string
	// URL and SHA256 name the archive to install; empty means the pinned
	// release for this platform.
	URL, SHA256 string
}

func (m *Mutagen) dir() (string, error) {
	if m.Dir != "" {
		return m.Dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", exitcode.New(exitcode.CodeFailed, fmt.Sprintf("cannot find the home directory: %v", err),
			"set HOME, or USERPROFILE on Windows")
	}
	return filepath.Join(home, ".tamp"), nil
}

// dataDir is Mutagen's own, which its daemon creates on first start.
func dataDir(dir string) string { return filepath.Join(dir, "mutagen") }

// binDir holds one Mutagen version's binary and agents.
func binDir(dir string) string { return filepath.Join(dir, "bin", "mutagen-"+Version) }

func exe() string {
	if runtime.GOOS == "windows" {
		return "mutagen.exe"
	}
	return "mutagen"
}

// Installed runs the installed binary, so a corrupt or quarantined one
// counts as missing and Install replaces it.
func (m *Mutagen) Installed(ctx context.Context) (bool, error) {
	dir, err := m.dir()
	if err != nil {
		return false, err
	}
	path := filepath.Join(binDir(dir), exe())
	if ok, err := present(path); err != nil || !ok {
		return false, err
	}
	return version(ctx, path, dir) == nil, nil
}

func present(path string) (bool, error) {
	_, err := os.Stat(path)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	}
	return false, exitcode.New(exitcode.CodeFailed, fmt.Sprintf("cannot read %s: %v", path, err),
		"check that you can read "+filepath.Dir(path))
}

// version checks that a binary runs and is the pinned version.
func version(ctx context.Context, bin, dir string) error {
	out, err := run(ctx, bin, dir, nil, "report its version", "version")
	var runErr *RunError
	switch {
	case errors.As(err, &runErr):
		return runErr.Err
	case err != nil:
		return err
	}
	if got := strings.TrimSpace(string(out)); got != Version {
		return fmt.Errorf("it reports the version %q", got)
	}
	return nil
}

func (m *Mutagen) source() (url, sum string, err error) {
	if m.URL != "" {
		return m.URL, m.SHA256, nil
	}
	platform := runtime.GOOS + "_" + runtime.GOARCH
	sum, ok := sums[platform]
	if !ok {
		return "", "", exitcode.New(exitcode.CodeFailed, "tamp has no Mutagen for "+platform,
			"run tamp on Windows or macOS, on amd64 or arm64")
	}
	return fmt.Sprintf("https://github.com/mutagen-io/mutagen/releases/download/v%s/mutagen_%s_v%s.tar.gz",
		Version, platform, Version), sum, nil
}

// Install downloads the archive into a temp folder, checks its sum, unpacks
// it, and checks that the binary runs before moving it into place.
func (m *Mutagen) Install(ctx context.Context) error {
	dir, err := m.dir()
	if err != nil {
		return err
	}
	url, sum, err := m.source()
	if err != nil {
		return err
	}
	bin := binDir(dir)
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		return exitcode.New(exitcode.CodeFailed, fmt.Sprintf("cannot create %s: %v", filepath.Dir(bin), err),
			"check that you can write to "+dir)
	}
	// Left by an install that was killed.
	stale, _ := filepath.Glob(filepath.Join(filepath.Dir(bin), ".mutagen-*"))
	for _, s := range stale {
		_ = os.RemoveAll(s)
	}
	tmp, err := os.MkdirTemp(filepath.Dir(bin), ".mutagen-")
	if err != nil {
		return exitcode.New(exitcode.CodeFailed, fmt.Sprintf("cannot create a folder in %s: %v", filepath.Dir(bin), err),
			"check that you can write to "+dir)
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	archive := filepath.Join(tmp, "mutagen.tar.gz")
	if err := download(ctx, url, archive, sum); err != nil {
		return err
	}
	out := filepath.Join(tmp, "mutagen")
	if err := unpack(archive, out); err != nil {
		return exitcode.New(exitcode.CodeFailed, fmt.Sprintf("cannot unpack the Mutagen archive from %s: %v", url, err),
			"check that you can write to "+dir)
	}
	if err := version(ctx, filepath.Join(out, exe()), dir); err != nil {
		return exitcode.New(exitcode.CodeFailed, fmt.Sprintf("the Mutagen downloaded from %s does not run: %v", url, err),
			"check that security software lets programs in "+filepath.Dir(bin)+" run")
	}
	// A copy that does not run, or one left half removed.
	if err := os.RemoveAll(bin); err != nil {
		return exitcode.New(exitcode.CodeFailed, fmt.Sprintf("cannot replace %s: %v", bin, err),
			"delete "+bin)
	}
	if err := os.Rename(out, bin); err != nil {
		return exitcode.New(exitcode.CodeFailed, fmt.Sprintf("cannot move Mutagen to %s: %v", bin, err),
			"check that you can write to "+dir)
	}
	return nil
}

// downloads gives up on a stalled download of the 100 MB archive.
var downloads = &http.Client{Timeout: 15 * time.Minute}

func download(ctx context.Context, url, path, sum string) error {
	failed := func(err error) error {
		return exitcode.New(exitcode.CodeFailed, fmt.Sprintf("cannot download Mutagen from %s: %v", url, err),
			"check this machine's connection to github.com")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	res, err := downloads.Do(req)
	if err != nil {
		return failed(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return failed(errors.New(res.Status))
	}
	f, err := os.Create(path)
	if err != nil {
		return exitcode.New(exitcode.CodeFailed, fmt.Sprintf("cannot write %s: %v", path, err),
			"check that you can write to "+filepath.Dir(path))
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, h), res.Body)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return failed(err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != sum {
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("the Mutagen download from %s has the SHA-256 %s, but tamp pins %s", url, got, sum),
			"check that no proxy or security software alters downloads from github.com")
	}
	return nil
}

// unpack writes the binary and the agents bundle from the archive into out.
func unpack(archive, out string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	if err := os.Mkdir(out, 0o755); err != nil {
		return err
	}
	want := map[string]os.FileMode{exe(): 0o755, agents: 0o644}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		mode, ok := want[h.Name]
		if !ok {
			continue
		}
		if err := writeFile(filepath.Join(out, h.Name), tr, mode); err != nil {
			return err
		}
		delete(want, h.Name)
	}
	if len(want) > 0 {
		return fmt.Errorf("it holds no %s", slices.Sorted(maps.Keys(want))[0])
	}
	return nil
}

func writeFile(path string, r io.Reader, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, r)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	return err
}

// run runs a Mutagen command with tamp's own data directory, so tamp's
// daemon and sessions never mix with a Mutagen the user runs. It runs in dir,
// as the daemon it starts keeps that folder, which Windows then cannot move.
// what names the command for an error, as in "cannot <what>".
func run(ctx context.Context, bin, dir string, env []string, what string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = append(append(os.Environ(), "MUTAGEN_DATA_DIRECTORY="+dataDir(dir)), env...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		return nil, exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("Mutagen cannot %s: %s", what, said(stderr.String())), mutagenFix)
	case err != nil:
		return nil, &RunError{Bin: bin, Err: err}
	}
	return stdout.Bytes(), nil
}

// said is the last line Mutagen wrote: its progress lines end in carriage
// returns, and its error comes last.
func said(stderr string) string {
	lines := strings.FieldsFunc(stderr, func(r rune) bool { return r == '\n' || r == '\r' })
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return strings.TrimPrefix(line, "Error: ")
		}
	}
	return "it exited with no message"
}

func (m *Mutagen) mutagen(ctx context.Context, what string, args ...string) ([]byte, error) {
	dir, err := m.dir()
	if err != nil {
		return nil, err
	}
	return run(ctx, filepath.Join(binDir(dir), exe()), dir, nil, what, args...)
}

func (m *Mutagen) Session(ctx context.Context, name string) (*Session, error) {
	dir, err := m.dir()
	if err != nil {
		return nil, err
	}
	if ok, err := present(filepath.Join(binDir(dir), exe())); err != nil || !ok {
		return nil, err
	}
	out, err := m.mutagen(ctx, "list the sync sessions", "sync", "list", "--template", "{{json .}}")
	if err != nil {
		return nil, err
	}
	return parseSession(out, name)
}

func (m *Mutagen) Peek(ctx context.Context, name string) (*Session, error) {
	dir, err := m.dir()
	if err != nil {
		return nil, err
	}
	bin := filepath.Join(binDir(dir), exe())
	if ok, err := present(bin); err != nil || !ok {
		return nil, err
	}
	// Without its data folder, no daemon ever ran here, so no session exists.
	if ok, err := present(dataDir(dir)); err != nil || !ok {
		return nil, err
	}
	out, err := run(ctx, bin, dir, []string{"MUTAGEN_DISABLE_AUTOSTART=1"}, "list the sync sessions",
		"sync", "list", "--template", "{{json .}}")
	// Mutagen 0.18's wording when autostart is off and no daemon runs.
	if err != nil && strings.Contains(err.Error(), "unable to connect to daemon") {
		return nil, ErrNoDaemon
	}
	if err != nil {
		return nil, err
	}
	return parseSession(out, name)
}

type endpoint struct {
	Host string `json:"host"`
	Path string `json:"path"`
}

// parseSession finds the named session in `mutagen sync list`'s JSON.
func parseSession(out []byte, name string) (*Session, error) {
	var sessions []struct {
		Name      string   `json:"name"`
		Paused    bool     `json:"paused"`
		Status    string   `json:"status"`
		Alpha     endpoint `json:"alpha"`
		Beta      endpoint `json:"beta"`
		Conflicts []struct {
			Root         string `json:"root"`
			AlphaChanges []struct {
				Path string    `json:"path"`
				New  *struct{} `json:"new"`
			} `json:"alphaChanges"`
		} `json:"conflicts"`
	}
	if err := json.Unmarshal(out, &sessions); err != nil {
		return nil, fmt.Errorf("mutagen sync list printed bad JSON: %w", err)
	}
	for _, s := range sessions {
		if s.Name != name {
			continue
		}
		got := &Session{Name: s.Name, Host: s.Alpha.Path, Container: s.Beta.Host, Path: s.Beta.Path,
			Paused: s.Paused, Status: s.Status}
		for _, c := range s.Conflicts {
			deleted := false
			for _, change := range c.AlphaChanges {
				deleted = deleted || (change.Path == c.Root && change.New == nil)
			}
			got.Conflicts = append(got.Conflicts, Conflict{Path: c.Root, HostDeleted: deleted})
		}
		return got, nil
	}
	return nil, nil
}

// Create makes the session two-way-resolved, where alpha, the host, wins
// every conflict.
func (m *Mutagen) Create(ctx context.Context, s Spec) error {
	_, err := m.mutagen(ctx, "create the sync session", createArgs(s.Name, "two-way-resolved", s)...)
	return err
}

func createArgs(name, mode string, s Spec) []string {
	args := []string{"sync", "create", "--name", name, "--sync-mode", mode}
	for _, p := range s.Ignore {
		args = append(args, "--ignore", p)
	}
	return append(args, s.Host, "docker://"+s.Container+s.Path)
}

// Replicate runs a one-way-replica session, where beta becomes a copy of
// alpha, until one full sync, then terminates it.
func (m *Mutagen) Replicate(ctx context.Context, s Spec) error {
	name := ReplicaName(s.Name)
	// One that a killed run left.
	if old, err := m.Session(ctx, name); err != nil {
		return err
	} else if old != nil {
		if err := m.Terminate(ctx, name); err != nil {
			return err
		}
	}
	if _, err := m.mutagen(ctx, "copy apps/ into the container", createArgs(name, "one-way-replica", s)...); err != nil {
		return err
	}
	if err := m.Flush(ctx, name); err != nil {
		return err
	}
	return m.Terminate(ctx, name)
}

func (m *Mutagen) Resume(ctx context.Context, name string) error {
	_, err := m.mutagen(ctx, "resume the sync session", "sync", "resume", name)
	return err
}

func (m *Mutagen) Flush(ctx context.Context, name string) error {
	_, err := m.mutagen(ctx, "finish syncing", "sync", "flush", name)
	return err
}

func (m *Mutagen) Pause(ctx context.Context, name string) error {
	_, err := m.mutagen(ctx, "pause the sync session", "sync", "pause", name)
	return err
}

func (m *Mutagen) Terminate(ctx context.Context, name string) error {
	_, err := m.mutagen(ctx, "terminate the sync session", "sync", "terminate", name)
	return err
}
