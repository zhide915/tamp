package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/zhide915/tamp/internal/frappe"
)

// BenchInitConfig is part of what a real bench init writes to
// common_site_config.json, so tamp's merge has bench's own keys to keep.
const BenchInitConfig = `{
 "background_workers": 1,
 "live_reload": true,
 "redis_cache": "redis://127.0.0.1:13000",
 "webserver_port": 8000
}
`

// Fake is the recording Client for tests. It also models a bench's files, so
// tamp reads back what bench init, new-site and its own writes left.
type Fake struct {
	// PingErr is Ping's answer.
	PingErr error
	// ExecFails fails each exec whose command line contains a key, with its
	// error: how a test plants a failure partway through a build.
	ExecFails map[string]error
	// ExecFailsFirst fails the first n execs whose command line contains a
	// key: how a test models a command that works once retried.
	ExecFailsFirst map[string]int
	// ExecOutput writes its value to the stderr of each exec whose command
	// line contains its key, as the command's own log.
	ExecOutput map[string]string
	// UpFails fails the compose up of each project named by a key, with its
	// error, and starts none of its containers.
	UpFails map[string]error
	// StaleList models the engine listing a container as running for a
	// moment after its stop returns: a stopped container stays listed until
	// its next compose up. Inspect still sees it stopped.
	StaleList bool

	// Calls names each method tamp called, in order.
	Calls []string
	// Builds, Volumes, Ups, Execs, Connects and Disconnects record each
	// request in full.
	Builds      []ImageBuild
	Volumes     []Volume
	Ups         []Up
	Execs       []Exec
	Connects    []Network
	Disconnects []Network
	// Stopped, Removed, RemovedNetworks and RemovedVolumes name what each
	// stop and removal asked for.
	Stopped, Removed, RemovedNetworks, RemovedVolumes []string

	images     map[string]bool
	containers map[string]*fakeContainer
	volumes    map[string]Volume
	// files is each container's filesystem, keyed by container then path.
	files map[string]map[string]string
}

// Up is one recorded compose up, with the compose file as it was then.
type Up struct {
	Project Project
	Compose string
}

func (f *Fake) Ping(context.Context) error {
	f.Calls = append(f.Calls, "Ping")
	return f.PingErr
}

func (f *Fake) ImageExists(_ context.Context, tag string) (bool, error) {
	f.Calls = append(f.Calls, "ImageExists")
	return f.images[tag], nil
}

func (f *Fake) BuildImage(_ context.Context, b ImageBuild) error {
	f.Calls = append(f.Calls, "BuildImage")
	f.Builds = append(f.Builds, b)
	if f.images == nil {
		f.images = map[string]bool{}
	}
	f.images[b.Tag] = true
	return nil
}

func (f *Fake) EnsureVolume(_ context.Context, v Volume) error {
	f.Calls = append(f.Calls, "EnsureVolume")
	f.Volumes = append(f.Volumes, v)
	if _, ok := f.volumes[v.Name]; !ok {
		f.putVolume(v)
	}
	return nil
}

func (f *Fake) InspectVolume(_ context.Context, name string) (Volume, error) {
	f.Calls = append(f.Calls, "InspectVolume")
	v, ok := f.volumes[name]
	if !ok {
		return Volume{}, fmt.Errorf("%s: %w", name, ErrNoVolume)
	}
	v.Labels = maps.Clone(v.Labels)
	return v, nil
}

// RemoveVolume fails, as Docker does, for a volume a container still uses.
func (f *Fake) RemoveVolume(_ context.Context, name string) error {
	f.Calls = append(f.Calls, "RemoveVolume")
	f.RemovedVolumes = append(f.RemovedVolumes, name)
	for _, c := range f.containers {
		if slices.Contains(c.volumes, name) {
			return fmt.Errorf("the volume %s is in use by %s", name, c.Name)
		}
	}
	delete(f.volumes, name)
	return nil
}

// VolumeNames lists the volumes that exist, sorted.
func (f *Fake) VolumeNames() []string {
	return slices.Sorted(maps.Keys(f.volumes))
}

// SetVolume places a volume, as one left by an earlier build would be.
func (f *Fake) SetVolume(v Volume) { f.putVolume(v) }

func (f *Fake) putVolume(v Volume) {
	if f.volumes == nil {
		f.volumes = map[string]Volume{}
	}
	v.Labels = maps.Clone(v.Labels)
	f.volumes[v.Name] = v
}

func (f *Fake) ComposeUp(_ context.Context, p Project) error {
	f.Calls = append(f.Calls, "ComposeUp")
	body, err := os.ReadFile(p.File)
	if err != nil {
		return err
	}
	f.Ups = append(f.Ups, Up{Project: p, Compose: string(body)})
	if err := f.UpFails[p.Name]; err != nil {
		return err
	}
	f.up(p.Name, string(body))
	return nil
}

// UpsOf returns the recorded compose ups of one project, in order.
func (f *Fake) UpsOf(project string) []Up {
	var ups []Up
	for _, u := range f.Ups {
		if u.Project.Name == project {
			ups = append(ups, u)
		}
	}
	return ups
}

func (f *Fake) Exec(_ context.Context, e Exec) error {
	f.Calls = append(f.Calls, "Exec")
	f.Execs = append(f.Execs, e)
	line := strings.Join(e.Cmd, " ")
	// bench new-site writes the site config before it installs anything, so
	// even a failed one leaves it.
	if len(e.Cmd) > 2 && e.Cmd[0] == "bench" && e.Cmd[1] == "new-site" {
		f.put(e.Container, frappe.SiteConfig(e.Cmd[2]), `{"db_name": "_site"}`)
	}
	for fragment, out := range f.ExecOutput {
		if strings.Contains(line, fragment) && e.Stderr != nil {
			fmt.Fprint(e.Stderr, out)
		}
	}
	for fragment, n := range f.ExecFailsFirst {
		if strings.Contains(line, fragment) && n > 0 {
			f.ExecFailsFirst[fragment] = n - 1
			return fmt.Errorf("%s failed", line)
		}
	}
	for fragment, err := range f.ExecFails {
		if strings.Contains(line, fragment) {
			return err
		}
	}
	switch {
	case slices.Equal(e.Cmd, frappe.ListSites()):
		if e.Stdout != nil {
			for _, host := range f.sites(e.Container) {
				fmt.Fprintln(e.Stdout, host)
			}
		}
	case len(e.Cmd) > 2 && e.Cmd[0] == "bench" && e.Cmd[1] == "drop-site":
		// bench moves the site's folder out of sites/.
		for path := range f.files[e.Container] {
			if strings.HasPrefix(path, frappe.SitesDir+"/"+e.Cmd[2]+"/") {
				delete(f.files[e.Container], path)
			}
		}
	case strings.Contains(line, "bench init"):
		f.put(e.Container, frappe.CommonSiteConfig, BenchInitConfig)
		f.put(e.Container, frappe.SitesDir+"/apps.txt", "frappe\n")
	case len(e.Cmd) == 7 && e.Cmd[0] == "bench" && e.Cmd[3] == "set-config" && e.Cmd[4] == "-p":
		return f.setSiteConfig(e.Container, e.Cmd[2], e.Cmd[5], e.Cmd[6])
	}
	return nil
}

// sites are the hosts whose site config is in the container, sorted.
func (f *Fake) sites(container string) []string {
	var hosts []string
	for path := range f.files[container] {
		rest, ok := strings.CutPrefix(path, frappe.SitesDir+"/")
		host, isConfig := strings.CutSuffix(rest, "/site_config.json")
		if ok && isConfig && !strings.Contains(host, "/") {
			hosts = append(hosts, host)
		}
	}
	slices.Sort(hosts)
	return hosts
}

// setSiteConfig is `bench --site <host> set-config -p <key> <value>`, for a
// value that is JSON.
func (f *Fake) setSiteConfig(container, host, key, value string) error {
	path := frappe.SiteConfig(host)
	config := map[string]any{}
	if body, ok := f.files[container][path]; ok {
		if err := json.Unmarshal([]byte(body), &config); err != nil {
			return err
		}
	}
	var v any
	if err := json.Unmarshal([]byte(value), &v); err != nil {
		return err
	}
	config[key] = v
	body, err := json.Marshal(config)
	if err != nil {
		return err
	}
	f.put(container, path, string(body))
	return nil
}

func (f *Fake) ReadFile(_ context.Context, container, path string) ([]byte, error) {
	f.Calls = append(f.Calls, "ReadFile")
	body, ok := f.files[container][path]
	if !ok {
		return nil, fmt.Errorf("%s in %s: %w", path, container, ErrNotExist)
	}
	return []byte(body), nil
}

func (f *Fake) WriteFile(_ context.Context, container, path string, data []byte) error {
	f.Calls = append(f.Calls, "WriteFile")
	f.put(container, path, string(data))
	return nil
}

// File returns a file in a container and whether it is there.
func (f *Fake) File(container, path string) (string, bool) {
	body, ok := f.files[container][path]
	return body, ok
}

// SetFile places a file in a container, as a user's hand edit would.
func (f *Fake) SetFile(container, path, body string) { f.put(container, path, body) }

// ExecLines joins each recorded exec's command line, in order.
func (f *Fake) ExecLines() []string {
	lines := make([]string, len(f.Execs))
	for i, e := range f.Execs {
		lines[i] = strings.Join(e.Cmd, " ")
	}
	return lines
}

func (f *Fake) put(container, path, body string) {
	if f.files == nil {
		f.files = map[string]map[string]string{}
	}
	if f.files[container] == nil {
		f.files[container] = map[string]string{}
	}
	f.files[container][path] = body
}
