package docker

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// bind is a host folder mounted into a container. The fake keeps the files
// under it on the host, so they outlive the container and its volumes, and
// a test that replaces the host folder replaces them too.
type bind struct{ host, target string }

var bindLine = regexp.MustCompile(`(?m)^      - \./([^:\s]+):(/\S+)$`)

// setBinds records the binds of a service block, relative to the compose
// file's directory.
func (f *Fake) setBinds(container, dir, block string) {
	if f.binds == nil {
		f.binds = map[string][]bind{}
	}
	f.binds[container] = nil
	for _, m := range bindLine.FindAllStringSubmatch(block, -1) {
		f.binds[container] = append(f.binds[container], bind{host: filepath.Join(dir, m[1]), target: m[2]})
	}
}

// hostPath is where a container path lives on the host, if a bind holds it.
func (f *Fake) hostPath(container, path string) (string, bool) {
	for _, b := range f.binds[container] {
		if rest, ok := strings.CutPrefix(path, b.target+"/"); ok {
			return filepath.Join(b.host, filepath.FromSlash(rest)), true
		}
	}
	return "", false
}

func (f *Fake) get(container, path string) (string, bool) {
	if host, ok := f.hostPath(container, path); ok {
		body, err := os.ReadFile(host)
		return string(body), err == nil
	}
	body, ok := f.files[container][path]
	return body, ok
}

func (f *Fake) remove(container, path string) {
	if host, ok := f.hostPath(container, path); ok {
		_ = os.Remove(host)
		return
	}
	delete(f.files[container], path)
}
