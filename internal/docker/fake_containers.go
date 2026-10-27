package docker

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
)

// Network is one recorded network connect or disconnect.
type Network struct {
	Network, Container string
	Aliases            []string
}

func (f *Fake) Containers(_ context.Context, label string) ([]Container, error) {
	f.Calls = append(f.Calls, "Containers")
	return f.list(label, false), nil
}

func (f *Fake) AllContainers(_ context.Context, label string) ([]Container, error) {
	f.Calls = append(f.Calls, "AllContainers")
	return f.list(label, true), nil
}

func (f *Fake) list(label string, all bool) []Container {
	var out []Container
	for _, name := range slices.Sorted(maps.Keys(f.containers)) {
		c := f.containers[name]
		listed := c.Running || c.stale
		if _, ok := c.Labels[label]; ok && (all || listed) {
			out = append(out, c.clone())
			out[len(out)-1].Running = listed
		}
	}
	return out
}

func (f *Fake) StopContainer(_ context.Context, name string) error {
	f.Calls = append(f.Calls, "StopContainer")
	f.Stopped = append(f.Stopped, name)
	c, ok := f.containers[name]
	if !ok {
		return fmt.Errorf("%s: %w", name, ErrNoContainer)
	}
	c.Running = false
	c.stale = f.StaleList
	return nil
}

func (f *Fake) RemoveContainer(_ context.Context, name string) error {
	f.Calls = append(f.Calls, "RemoveContainer")
	f.Removed = append(f.Removed, name)
	if _, ok := f.containers[name]; !ok {
		return fmt.Errorf("%s: %w", name, ErrNoContainer)
	}
	delete(f.containers, name)
	delete(f.files, name)
	delete(f.binds, name)
	return nil
}

// RemoveNetwork fails, as Docker does, for a network a container is still on.
func (f *Fake) RemoveNetwork(_ context.Context, network string) error {
	f.Calls = append(f.Calls, "RemoveNetwork")
	f.RemovedNetworks = append(f.RemovedNetworks, network)
	for _, name := range slices.Sorted(maps.Keys(f.containers)) {
		if _, on := f.containers[name].Networks[network]; on {
			return fmt.Errorf("the network %s has active endpoints: %s", network, name)
		}
	}
	return nil
}

func (f *Fake) Inspect(_ context.Context, name string) (Container, error) {
	f.Calls = append(f.Calls, "Inspect")
	c, ok := f.containers[name]
	if !ok {
		return Container{}, fmt.Errorf("%s: %w", name, ErrNoContainer)
	}
	return c.clone(), nil
}

// ConnectNetwork fails, as Docker does, for a container already on the
// network: aliases change only by disconnecting first.
func (f *Fake) ConnectNetwork(_ context.Context, network, container string, aliases []string) error {
	f.Calls = append(f.Calls, "ConnectNetwork")
	f.Connects = append(f.Connects, Network{Network: network, Container: container, Aliases: slices.Clone(aliases)})
	c, ok := f.containers[container]
	if !ok {
		return fmt.Errorf("%s: %w", container, ErrNoContainer)
	}
	if _, on := c.Networks[network]; on {
		return fmt.Errorf("%s is already on the network %s", container, network)
	}
	c.Networks[network] = slices.Clone(aliases)
	return nil
}

func (f *Fake) DisconnectNetwork(_ context.Context, network, container string) error {
	f.Calls = append(f.Calls, "DisconnectNetwork")
	f.Disconnects = append(f.Disconnects, Network{Network: network, Container: container})
	c, ok := f.containers[container]
	if !ok {
		return fmt.Errorf("%s: %w", container, ErrNoContainer)
	}
	if _, on := c.Networks[network]; !on {
		return fmt.Errorf("%s is not on the network %s", container, network)
	}
	delete(c.Networks, network)
	return nil
}

// Stop stops a compose project's containers, as `docker compose stop` would.
func (f *Fake) Stop(project string) {
	for _, c := range f.containers {
		if c.project == project {
			c.Running = false
		}
	}
}

// fakeContainer is a container as compose up leaves it.
type fakeContainer struct {
	Container
	project string
	// volumes are the named volumes it mounts.
	volumes []string
	// stale lists it as running though it stopped, under StaleList.
	stale bool
}

func (c *fakeContainer) clone() Container {
	out := c.Container
	out.Labels = maps.Clone(c.Labels)
	out.Networks = map[string][]string{}
	for k, v := range c.Networks {
		out.Networks[k] = slices.Clone(v)
	}
	return out
}

var (
	serviceHeader = regexp.MustCompile(`(?m)^  ([a-z0-9-]+):$`)
	containerName = regexp.MustCompile(`(?m)^    container_name: (\S+)$`)
	labelLine     = regexp.MustCompile(`(?m)^      ([a-z.]+): '((?:[^']|'')*)'$`)
	mountLine     = regexp.MustCompile(`(?m)^      - ([a-z0-9][a-z0-9_-]*):/`)
	volumeHeader  = regexp.MustCompile(`(?m)^  ([a-z0-9-]+):(.*)$`)
	volumeName    = regexp.MustCompile(`(?m)^    name: (\S+)$`)
)

// up models compose up: each service in the file becomes a running
// container, named and labelled as compose would, on the project's network,
// and each volume the file owns is created. An existing container keeps the
// networks it was connected to.
func (f *Fake) up(project, dir, compose string) {
	_, section, _ := strings.Cut(compose, "\nservices:\n")
	section, volumesSection, _ := strings.Cut(section, "\nvolumes:\n")
	volumes := f.composeVolumes(project, volumesSection)
	starts := serviceHeader.FindAllStringSubmatchIndex(section, -1)
	for i, m := range starts {
		end := len(section)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		block := section[m[0]:end]
		name := project + "-" + section[m[2]:m[3]] + "-1"
		if cn := containerName.FindStringSubmatch(block); cn != nil {
			name = cn[1]
		}
		if f.containers == nil {
			f.containers = map[string]*fakeContainer{}
		}
		c, ok := f.containers[name]
		if !ok {
			c = &fakeContainer{project: project, Container: Container{Name: name,
				Networks: map[string][]string{project + "_default": nil}}}
			f.containers[name] = c
		}
		c.Running, c.stale = true, false
		c.Labels = map[string]string{}
		for _, l := range labelLine.FindAllStringSubmatch(block, -1) {
			// Compose reads $$ as a literal $ after YAML unquotes.
			c.Labels[l[1]] = strings.ReplaceAll(strings.ReplaceAll(l[2], "''", "'"), "$$", "$")
		}
		f.setBinds(name, dir, block)
		c.volumes = nil
		for _, m := range mountLine.FindAllStringSubmatch(block, -1) {
			if v, ok := volumes[m[1]]; ok {
				c.volumes = append(c.volumes, v)
			}
		}
	}
}

// composeVolumes maps each volume key of a compose file to its Docker name,
// and creates the volumes compose owns, which are not external.
func (f *Fake) composeVolumes(project, section string) map[string]string {
	names := map[string]string{}
	starts := volumeHeader.FindAllStringSubmatchIndex(section, -1)
	for i, m := range starts {
		end := len(section)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		key, block := section[m[2]:m[3]], section[m[0]:end]
		name := project + "_" + key
		if n := volumeName.FindStringSubmatch(block); n != nil {
			name = n[1]
		}
		names[key] = name
		if _, ok := f.volumes[name]; !ok && !strings.Contains(block, "external: true") {
			f.putVolume(Volume{Name: name})
		}
	}
	return names
}
