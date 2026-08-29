package env

import (
	"os"
	"path/filepath"

	"github.com/zhide915/tamp/assets"
	"github.com/zhide915/tamp/internal/frappe"
	"github.com/zhide915/tamp/internal/syncer"
	"github.com/zhide915/tamp/internal/toolchain"
)

// ComposeFile is regenerated from tamp.toml on every start — hand-edits do
// not survive.
const ComposeFile = "compose.yaml"

// GitignoreFile is written once at create and never rewritten: the directory
// may be the root of the user's own repository.
const GitignoreFile = ".gitignore"

func ComposePath(dir string) string { return filepath.Join(dir, ComposeFile) }

// composeData feeds the compose template. Every value derives from tamp.toml
// or the environment's resource names.
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

	DBVolume    string
	CodeVolume  string
	DepsVolume  string
	SitesVolume string

	ToolchainVolume string
	ToolchainDir    string
	PipCacheVolume  string
	PipCacheDir     string
	YarnCacheVolume string
	YarnCacheDir    string
	TemplateVolume  string
	TemplateDir     string
	SeedVolume      string
	SeedDir         string

	WorkspaceDir string
	BenchDir     string
	EnvDir       string
	SitesDir     string
	AppsDir      string
	ProcfilePath string
	EnvScript    string

	// AppsBind is the host path bound over the bench's apps directory, or
	// empty when the source reaches the container another way.
	AppsBind string
	// BindAddr is the interface the bench's web server listens on.
	BindAddr string
}

// Generate rewrites every generated file from tamp.toml. It runs at create
// and on every start, so containers always match the config — including after
// a tamp upgrade changes the templates.
//
// sync is passed rather than read off the config: "auto" resolves differently
// per machine, and a machine without Mutagen falls back to the bind mount.
func (e *Environment) Generate(sync syncer.Effective) error {
	// Relative with forward slashes: compose resolves it against its own
	// directory and accepts slashes on every platform.
	appsBind := ""
	if sync == syncer.UseBind {
		appsBind = "./" + syncer.AppsDirName
	}

	return assets.Write("compose.yaml.tmpl", ComposePath(e.Dir), composeData{
		Name:         e.Config.Name,
		Project:      e.Resources.Project(),
		Network:      e.Resources.Network(),
		BenchImage:   BenchImage,
		MariaDBImage: MariaDBImage(e.Config.Toolchain.MariaDB),
		RedisImage:   RedisImage,
		MailpitImage: MailpitImage,
		DBPort:       e.Config.Ports.DB,
		DBSecretName: DBRootPasswordFile,
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
		TemplateVolume:  frappe.TemplateVolume,
		TemplateDir:     frappe.TemplateDir,
		SeedVolume:      frappe.SeedVolume,
		SeedDir:         frappe.SeedDir,

		WorkspaceDir: frappe.WorkspaceDir,
		BenchDir:     frappe.BenchDir,
		EnvDir:       frappe.EnvDir,
		SitesDir:     frappe.SitesDir,
		AppsDir:      frappe.AppsDir,
		ProcfilePath: frappe.ProcfilePath,
		EnvScript:    toolchain.EnvScript,
		AppsBind:     appsBind,
		BindAddr:     frappe.BindAddr,
	})
}

// WriteGitignore writes the environment's .gitignore, leaving an existing one
// alone. Whether the directory becomes a repository is the user's call; this
// file only keeps the secrets and generated files out if it does.
func WriteGitignore(dir string) error {
	path := filepath.Join(dir, GitignoreFile)
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	return assets.Write("gitignore.tmpl", path, nil)
}
