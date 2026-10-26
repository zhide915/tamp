package env

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"runtime"
	"strconv"

	"github.com/zhide915/tamp/internal/docker"
	"github.com/zhide915/tamp/internal/frappe"
)

// Image pins: tamp hosts no images, so these are its whole supply chain.
const (
	BenchImage = "frappe/bench:v5.31.0"
	RedisImage = "redis:7-alpine"
)

// dockerfile builds the bench image for one Frappe branch: the official bench
// image with the matrix's Python and Node first on PATH.
const dockerfile = `FROM ` + BenchImage + `
ARG PYTHON
ARG NODE
ARG UID
ARG GID
USER root
RUN if [ "$UID" != "$(id -u frappe)" ] || [ "$GID" != "$(id -g frappe)" ]; then \
      groupmod --non-unique --gid "$GID" frappe \
      && usermod --non-unique --uid "$UID" --gid "$GID" frappe \
      && chown -R frappe:frappe ` + frappe.Home + `; \
    fi
# Bench runs git on clones the host made.
RUN git config --system --add safe.directory '*'
# The bench's MariaDB client demands TLS, which MariaDB before 11.4 lacks.
RUN printf '[client]\nskip-ssl\n' > /etc/mysql/conf.d/tamp.cnf
USER frappe
# The base image's yarn is plain JavaScript, so it runs on the matrix's Node.
RUN mkdir -p ` + frappe.Home + `/.tamp/bin ` + frappe.BenchDir + ` ` + frappe.CacheDir + ` \
 && uv python install "$PYTHON" \
 && ln -s "$(uv python find --managed-python "$PYTHON")" ` + frappe.Python + ` \
 && ln -s "$(readlink -f "$(command -v yarn)")" ` + frappe.Home + `/.tamp/bin/yarn \
 && . "$NVM_DIR/nvm.sh" \
 && nvm install "$NODE" \
 && for tool in node npm npx; do \
      ln -s "$NVM_DIR/versions/node/$(nvm version "$NODE")/bin/$tool" ` + frappe.Home + `/.tamp/bin/$tool; \
    done
ENV PATH=` + frappe.Home + `/.tamp/bin:$PATH
WORKDIR ` + frappe.BenchDir + `
`

// imageBuild is the image for a Frappe branch, tagged with a hash of its
// Dockerfile and arguments, so a changed one is built anew.
func imageBuild(branch string) docker.ImageBuild {
	tc := matrix[branch]
	uid, gid := imageIDs()
	args := map[string]string{
		"PYTHON": tc.Python,
		"NODE":   tc.Node,
		"UID":    strconv.Itoa(uid),
		"GID":    strconv.Itoa(gid),
	}
	h := sha256.New()
	h.Write([]byte(dockerfile))
	for _, k := range []string{"PYTHON", "NODE", "UID", "GID"} {
		h.Write([]byte("\x00" + k + "=" + args[k]))
	}
	return docker.ImageBuild{
		Tag:        "tamp-bench:" + branch + "-" + hex.EncodeToString(h.Sum(nil))[:12],
		Dockerfile: dockerfile,
		Args:       args,
	}
}

// imageIDs are the bench user's IDs: on Linux the host user's, so the files
// the container writes into the bind-mounted apps/ are the user's own.
func imageIDs() (uid, gid int) {
	if runtime.GOOS == "linux" {
		return os.Getuid(), os.Getgid()
	}
	return 1000, 1000
}
