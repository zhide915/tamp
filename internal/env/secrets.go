package env

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/zhide915/tamp/internal/exitcode"
)

// SecretsDirName holds an environment's generated credentials. It is inside
// .tamp/, which the generated .gitignore excludes — on NTFS a 0600 mode means
// nothing, so keeping secrets out of git is the protection that actually
// works.
const SecretsDirName = "secrets"

// DBRootPasswordFile is the MariaDB root credential. It is a file rather than
// a value in compose.yaml so the secret lives in exactly one place: compose
// mounts it, and `tamp db` reads it back to show the user.
const DBRootPasswordFile = "db_root_password"

// SecretsDir is an environment's secrets directory.
func SecretsDir(dir string) string { return filepath.Join(StateDir(dir), SecretsDirName) }

// DBRootPasswordPath is the file holding the MariaDB root password.
func DBRootPasswordPath(dir string) string {
	return filepath.Join(SecretsDir(dir), DBRootPasswordFile)
}

// EnsureDBRootPassword generates the environment's MariaDB root password if it
// does not have one yet.
//
// It is generated once and never rotated: MariaDB stores the password in the
// data volume at first start, so a fresh password against a surviving volume
// would lock tamp out of its own database.
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
	// crypto/rand.Text is 26 base32 characters — around 130 bits, and
	// alphanumeric, which matters because this password travels through a
	// compose file, a container shell and a MariaDB client, and every
	// punctuation character is one more thing for one of them to mangle.
	// No trailing newline: the file's whole content is the password.
	if err := os.WriteFile(path, []byte(rand.Text()), 0o600); err != nil {
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot write %s: %v", path, err),
			"check the permissions on the environment directory")
	}
	return nil
}
