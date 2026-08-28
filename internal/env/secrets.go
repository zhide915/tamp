package env

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/zhide915/tamp/internal/exitcode"
)

// SecretsDirName lives inside .tamp/, which the generated .gitignore
// excludes — on NTFS a 0600 mode means nothing, so staying out of git is the
// protection that works.
const SecretsDirName = "secrets"

// DBRootPasswordFile is a file rather than a compose value so the secret has
// one home: compose mounts it, `tamp db` reads it back.
const DBRootPasswordFile = "db_root_password"

func SecretsDir(dir string) string { return filepath.Join(StateDir(dir), SecretsDirName) }

func DBRootPasswordPath(dir string) string {
	return filepath.Join(SecretsDir(dir), DBRootPasswordFile)
}

func ReadDBRootPassword(dir string) (string, error) {
	path := DBRootPasswordPath(dir)
	body, err := os.ReadFile(path)
	if err != nil {
		return "", exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot read %s: %v", path, err),
			"the environment's database credential is missing — recreate the environment")
	}
	return strings.TrimSpace(string(body)), nil
}

// EnsureDBRootPassword generates the MariaDB root password once and never
// rotates it: MariaDB bakes it into the data volume at first start, and a
// fresh one against a surviving volume would lock tamp out.
func EnsureDBRootPassword(dir string) error {
	path := DBRootPasswordPath(dir)
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot read %s: %v", path, err),
			"check the permissions on the environment directory")
	}

	if err := os.MkdirAll(SecretsDir(dir), 0o700); err != nil {
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot create %s: %v", SecretsDir(dir), err),
			"check the permissions on the environment directory")
	}
	// rand.Text: ~130 bits, alphanumeric — the password travels through a
	// compose file, a shell and a MariaDB client, any of which could mangle
	// punctuation. No trailing newline: the whole file is the password.
	if err := os.WriteFile(path, []byte(rand.Text()), 0o600); err != nil {
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot write %s: %v", path, err),
			"check the permissions on the environment directory")
	}
	return nil
}
