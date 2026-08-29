package env

import (
	"fmt"
	"strings"

	"github.com/zhide915/tamp/internal/exitcode"
)

// Image pins — tamp hosts no images, so this is the entire supply chain.
// Change deliberately, e2e suite green, and never "latest".
const (
	// Verified against Docker Hub on 2026-08-27.
	BenchImage = "frappe/bench:v5.31.0"
	// One image backs both the cache and the queue services.
	RedisImage   = "redis:7-alpine"
	MailpitImage = "axllent/mailpit:v1.31"
)

// MariaDBImage is per-environment, at the version its Frappe release expects.
func MariaDBImage(version string) string { return "mariadb:" + version }

// Toolchain is the pinned language and database set a Frappe version needs.
type Toolchain struct {
	Python  string `toml:"python"`
	Node    string `toml:"node"`
	MariaDB string `toml:"mariadb"`
}

// FrappeVersion is a supported --frappe value.
type FrappeVersion string

const (
	Version15 FrappeVersion = "version-15"
	Version16 FrappeVersion = "version-16"
	Develop   FrappeVersion = "develop"

	DefaultFrappeVersion = Version15
)

// matrix is the version table; adding or bumping a Frappe version changes
// only this. version-14 is absent rather than commented out: rejections name
// these keys, so an unsupported version gets one honest answer.
var matrix = map[FrappeVersion]Toolchain{
	Version15: {Python: "3.11", Node: "18", MariaDB: "10.8"},
	Version16: {Python: "3.14", Node: "24", MariaDB: "11.8"},
	// develop tracks v16's floors until it moves past them.
	Develop: {Python: "3.14", Node: "24", MariaDB: "11.8"},
}

// supportedVersions fixes the display order, which the map cannot.
var supportedVersions = []FrappeVersion{Version15, Version16, Develop}

func ParseFrappeVersion(s string) (FrappeVersion, Toolchain, error) {
	v := FrappeVersion(s)
	tc, ok := matrix[v]
	if !ok {
		return "", Toolchain{}, exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("Frappe %s is not supported", s),
			"supported versions: "+strings.Join(versionNames(), ", "))
	}
	return v, tc, nil
}

// ToolchainFor misses only for a hand-edited tamp.toml — anything tamp wrote
// went through ParseFrappeVersion.
func ToolchainFor(v FrappeVersion) (Toolchain, bool) {
	tc, ok := matrix[v]
	return tc, ok
}

func versionNames() []string {
	names := make([]string, len(supportedVersions))
	for i, v := range supportedVersions {
		names[i] = string(v)
	}
	return names
}
