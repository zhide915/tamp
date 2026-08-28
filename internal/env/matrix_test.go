package env

import (
	"strings"
	"testing"

	"github.com/zhide915/tamp/internal/exitcode"
)

// The matrix is the whole point of --frappe: the user names a Frappe release
// and tamp knows which Python, Node and MariaDB go with it.
func TestParseFrappeVersionResolvesTheDocumentedToolchains(t *testing.T) {
	cases := map[string]Toolchain{
		"version-15": {Python: "3.11", Node: "18", MariaDB: "10.11"},
		"version-16": {Python: "3.14", Node: "24", MariaDB: "11.8"},
		"develop":    {Python: "3.14", Node: "24", MariaDB: "11.8"},
	}
	for version, want := range cases {
		_, got, err := ParseFrappeVersion(version)
		if err != nil {
			t.Fatalf("ParseFrappeVersion(%q) = %v", version, err)
		}
		if got != want {
			t.Errorf("ParseFrappeVersion(%q) toolchain = %+v, want %+v", version, got, want)
		}
	}
}

// version-14 is near EOL and out of scope; the error has to name what
// tamp does support rather than leave the user guessing.
func TestUnsupportedFrappeVersionNamesTheSupportedSet(t *testing.T) {
	for _, version := range []string{"version-14", "v15", "", "nightly"} {
		_, _, err := ParseFrappeVersion(version)
		if err == nil {
			t.Fatalf("ParseFrappeVersion(%q) = nil, want an error", version)
		}
		if got := exitcode.Of(err); got != exitcode.CodeFailed {
			t.Errorf("ParseFrappeVersion(%q) exit code = %d, want %d", version, got, exitcode.CodeFailed)
		}
		for _, supported := range []string{"version-15", "version-16", "develop"} {
			if !strings.Contains(err.Error(), supported) {
				t.Errorf("ParseFrappeVersion(%q) error %q does not mention %q", version, err, supported)
			}
		}
	}
}

// A generated compose file that says "latest" is not a pin.
func TestEveryImageIsPinnedToATag(t *testing.T) {
	images := map[string]string{
		"bench":   BenchImage,
		"redis":   RedisImage,
		"mailpit": MailpitImage,
		"mariadb": MariaDBImage("10.11"),
	}
	for what, image := range images {
		_, tag, ok := strings.Cut(image, ":")
		if !ok || tag == "" || tag == "latest" {
			t.Errorf("%s image %q is not pinned to a tag", what, image)
		}
	}
}
