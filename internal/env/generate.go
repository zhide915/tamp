package env

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"text/template"

	"github.com/zhide915/tamp/assets"
	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/frappe"
	"github.com/zhide915/tamp/internal/toolchain"
)

// ComposeFile is the compose file tamp generates. It is regenerated from
// tamp.toml on every start — hand-edits do not survive.
const ComposeFile = "compose.yaml"

// GitignoreFile is written once, at create. Unlike compose.yaml it is
// the user's file afterwards: tamp never rewrites it, because the directory
// it lives in may well be the root of the user's own repository.
const GitignoreFile = ".gitignore"

// ComposePath is the generated compose file inside an environment directory.
func ComposePath(dir string) string { return filepath.Join(dir, ComposeFile) }

// composeData is what the compose template is rendered from. Every value in it
// comes from tamp.toml or from the environment's resource names, which is the
// mechanical form of "tamp.toml is the source of truth".
type composeData struct {
	Name         Name
	Project      string
	Network      string
	BenchImage   string
	MariaDBImage string
	RedisImage   string
	MailpitImage string
	DBPort       int
	DBSecretName string
	DBSecretFile string

	// The environment's own storage layers.
	DBVolume    string
	CodeVolume  string
	DepsVolume  string
	SitesVolume string

	// The volumes every environment on the machine shares, and where the bench
	// container mounts them.
	ToolchainVolume string
	ToolchainDir    string
	PipCacheVolume  string
	PipCacheDir     string
	YarnCacheVolume string
	YarnCacheDir    string

	// Where the bench lives inside the container, and the two files that
	// decide what its container does when it boots.
	WorkspaceDir string
	BenchDir     string
	EnvDir       string
	SitesDir     string
	ProcfilePath string
	EnvScript    string
}

// Generate rewrites every file tamp generates from tamp.toml.
//
// It runs at create and again at the start of every start, so an
// environment's containers always match its config — including after tamp
// itself is upgraded and the templates beneath it change.
func (e *Environment) Generate() error {
	return render("compose.yaml.tmpl", ComposePath(e.Dir), composeData{
		Name:         e.Config.Name,
		Project:      e.Resources.Project(),
		Network:      e.Resources.Network(),
		BenchImage:   BenchImage,
		MariaDBImage: MariaDBImage(e.Config.Toolchain.MariaDB),
		RedisImage:   RedisImage,
		MailpitImage: MailpitImage,
		DBPort:       e.Config.Ports.DB,
		DBSecretName: DBRootPasswordFile,
		// Compose resolves a relative secret path against the compose file's
		// own directory, and accepts forward slashes on every platform.
		DBSecretFile: "./" + StateDirName + "/" + SecretsDirName + "/" + DBRootPasswordFile,

		DBVolume:    e.Resources.Volume(DataVolume),
		CodeVolume:  e.Resources.Volume(CodeVolume),
		DepsVolume:  e.Resources.Volume(DepsVolume),
		SitesVolume: e.Resources.Volume(SitesVolume),

		ToolchainVolume: toolchain.Volume,
		ToolchainDir:    toolchain.Dir,
		PipCacheVolume:  frappe.PipCacheVolume,
		PipCacheDir:     frappe.PipCacheDir,
		YarnCacheVolume: frappe.YarnCacheVolume,
		YarnCacheDir:    frappe.YarnCacheDir,

		WorkspaceDir: frappe.WorkspaceDir,
		BenchDir:     frappe.BenchDir,
		EnvDir:       frappe.EnvDir,
		SitesDir:     frappe.SitesDir,
		ProcfilePath: frappe.ProcfilePath,
		EnvScript:    toolchain.EnvScript,
	})
}

// WriteGitignore writes the environment's .gitignore, leaving an existing one
// alone. tamp never runs `git init`: whether this directory is a
// repository is the user's call, and this file is only there so that if it
// becomes one, the secrets and the generated files stay out of it.
func WriteGitignore(dir string) error {
	path := filepath.Join(dir, GitignoreFile)
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	return render("gitignore.tmpl", path, nil)
}

func render(name, path string, data any) error {
	tmpl, err := template.ParseFS(assets.FS, name)
	if err != nil {
		// The templates are compiled into the binary, so this is a broken
		// build rather than anything the user did.
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("tamp's %s template is broken: %v", name, err),
			"report this — it is a bug in tamp, not in your environment")
	}

	// Rendered whole before anything is written: a template that fails halfway
	// must not leave a truncated compose.yaml where the old one was.
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("tamp's %s template is broken: %v", name, err),
			"report this — it is a bug in tamp, not in your environment")
	}

	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot write %s: %v", path, err),
			"check that the environment directory is writable")
	}
	return nil
}
