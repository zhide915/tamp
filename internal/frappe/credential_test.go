package frappe_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zhide915/tamp/internal/frappe"
	"github.com/zhide915/tamp/internal/gitcred"
)

// fillWith runs real git's credential fill under an injection, so the scope
// is git's own URL matching. It returns fill's answer for a dev.azure.com path.
func fillWith(t *testing.T, injection []string) func(path string) string {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git on this machine")
	}
	env := append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+filepath.Join(t.TempDir(), "gitconfig"),
		"GIT_ASKPASS=true")
	env = append(env, injection...)
	return func(path string) string {
		cmd := exec.Command(git, "credential", "fill")
		cmd.Env = env
		cmd.Stdin = strings.NewReader("protocol=https\nhost=dev.azure.com\npath=" + path + "\n")
		out, _ := cmd.Output()
		return string(out)
	}
}

// A helper scoped to one repository must not answer for its neighbour.
func TestACredentialScopedToARepositoryServesNoOtherRepositoryOnItsHost(t *testing.T) {
	fill := fillWith(t, frappe.CredentialEnv("https://dev.azure.com/orga/p/_git/alpha", "dev", "s3cret-A", false, nil))

	if got := fill("orga/p/_git/alpha"); !strings.Contains(got, "password=s3cret-A") {
		t.Errorf("the credential's own repository was not served it:\n%s", got)
	}
	if got := fill("orgb/p/_git/beta"); strings.Contains(got, "s3cret-A") {
		t.Errorf("another repository on the host was served the credential:\n%s", got)
	}
}

// The key spells the repository as git matches it, reserved characters and all.
func TestARepositoryCredentialServesAPathWithReservedCharacters(t *testing.T) {
	fill := fillWith(t, frappe.CredentialEnv("https://dev.azure.com/orga/p/_git/a=b+c[d]", "dev", "s3cret-A", false, nil))

	if got := fill("orga/p/_git/a=b+c[d]"); !strings.Contains(got, "password=s3cret-A") {
		t.Errorf("the credential's own repository was not served it:\n%s", got)
	}
}

// A host credential serves the host except where the replayed useHttpPath
// config scopes a URL by path, however that config says so.
func TestAHostCredentialDeclinesTheURLsTheHostScopesByPath(t *testing.T) {
	for name, httpPath := range map[string][]gitcred.Setting{
		"per URL": {{Key: "credential.https://dev.azure.com/orgb.usehttppath", Value: "true"}},
		"global with override": {
			{Key: "credential.usehttppath", Value: "true"},
			{Key: "credential.https://dev.azure.com/orga.usehttppath", Value: "false"},
		},
		"wildcard": {{Key: "credential.https://*.azure.com/orgb.usehttppath", Value: "true"}},
	} {
		t.Run(name, func(t *testing.T) {
			fill := fillWith(t, frappe.CredentialEnv("https://dev.azure.com", "dev", "s3cret-A", true, httpPath))

			if got := fill("orga/p/_git/alpha"); !strings.Contains(got, "password=s3cret-A") {
				t.Errorf("a repository the host scopes by host was not served the host's credential:\n%s", got)
			}
			if got := fill("orgb/p/_git/beta"); strings.Contains(got, "s3cret-A") {
				t.Errorf("a repository the host scopes by path was served the host's credential:\n%s", got)
			}
		})
	}
}
