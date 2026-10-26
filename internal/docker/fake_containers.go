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
	var out []Container
	for _, name := range slices.Sorted(maps.Keys(f.containers)) {
		c := f.containers[name]
		if _, ok := c.Labels[label]; ok && c.Running {
			out = append(out, c.clone())
		}
	}
	return out, nil
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
)

// up models compose up: each service in the file becomes a running
// container, named and labelled as compose would, on the project's network.
// An existing container keeps the networks it was connected to.
func (f *Fake) up(project, compose string) {
	_, section, _ := strings.Cut(compose, "\nservices:\n")
	section, _, _ = strings.Cut(section, "\nvolumes:\n")
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
		c.Running = true
		c.Labels = map[string]string{}
		for _, l := range labelLine.FindAllStringSubmatch(block, -1) {
			c.Labels[l[1]] = strings.ReplaceAll(l[2], "''", "'")
		}
	}
}
