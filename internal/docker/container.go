package docker

import (
	"context"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
	"github.com/zhide915/tamp/internal/exitcode"
)

func (d *Daemon) StopContainer(ctx context.Context, name string) error {
	_, api, err := d.connect()
	if err != nil {
		return err
	}
	_, err = api.ContainerStop(ctx, name, client.ContainerStopOptions{})
	return refused("stop the container "+name, err)
}

func (d *Daemon) RemoveContainer(ctx context.Context, name string) error {
	_, api, err := d.connect()
	if err != nil {
		return err
	}
	_, err = api.ContainerRemove(ctx, name, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true})
	return refused("remove the container "+name, err)
}

func (d *Daemon) RemoveNetwork(ctx context.Context, net string) error {
	_, api, err := d.connect()
	if err != nil {
		return err
	}
	if _, err := api.NetworkRemove(ctx, net, client.NetworkRemoveOptions{}); !cerrdefs.IsNotFound(err) {
		return refused("remove the network "+net, err)
	}
	return nil
}

func (d *Daemon) InspectVolume(ctx context.Context, name string) (Volume, error) {
	_, api, err := d.connect()
	if err != nil {
		return Volume{}, err
	}
	res, err := api.VolumeInspect(ctx, name, client.VolumeInspectOptions{})
	if cerrdefs.IsNotFound(err) {
		return Volume{}, fmt.Errorf("%s: %w", name, ErrNoVolume)
	}
	if err != nil {
		return Volume{}, unavailable("inspect the volume "+name, err)
	}
	return Volume{Name: res.Volume.Name, Labels: res.Volume.Labels}, nil
}

func (d *Daemon) RemoveVolume(ctx context.Context, name string) error {
	_, api, err := d.connect()
	if err != nil {
		return err
	}
	if _, err := api.VolumeRemove(ctx, name, client.VolumeRemoveOptions{}); !cerrdefs.IsNotFound(err) {
		return refused("remove the volume "+name, err)
	}
	return nil
}

// refused tells an unreachable Docker from one that answered and refused,
// such as a network that a container is still on.
func refused(what string, err error) error {
	if err == nil {
		return nil
	}
	if client.IsErrConnectionFailed(err) {
		return unavailable(what, err)
	}
	return exitcode.New(exitcode.CodeFailed, fmt.Sprintf("cannot %s: %v", what, rootCause(err)),
		"fix what Docker names, then run the command again")
}

func (d *Daemon) Publishing(ctx context.Context, port int) ([]string, error) {
	_, api, err := d.connect()
	if err != nil {
		return nil, err
	}
	res, err := api.ContainerList(ctx, client.ContainerListOptions{
		Filters: make(client.Filters).Add("publish", strconv.Itoa(port)),
	})
	if err != nil {
		return nil, unavailable("list Docker containers", err)
	}
	var names []string
	for _, item := range res.Items {
		if len(item.Names) > 0 && slices.ContainsFunc(item.Ports, func(p container.PortSummary) bool {
			return int(p.PublicPort) == port && p.Type == "tcp" && clashesWithLoopback(p.IP)
		}) {
			names = append(names, strings.TrimPrefix(item.Names[0], "/"))
		}
	}
	slices.Sort(names)
	return names, nil
}

func (d *Daemon) Containers(ctx context.Context, label string) ([]Container, error) {
	return d.list(ctx, label, false)
}

func (d *Daemon) AllContainers(ctx context.Context, label string) ([]Container, error) {
	return d.list(ctx, label, true)
}

func (d *Daemon) list(ctx context.Context, label string, all bool) ([]Container, error) {
	_, api, err := d.connect()
	if err != nil {
		return nil, err
	}
	res, err := api.ContainerList(ctx, client.ContainerListOptions{
		All:     all,
		Filters: make(client.Filters).Add("label", label),
	})
	if err != nil {
		return nil, unavailable("list Docker containers", err)
	}
	containers := make([]Container, 0, len(res.Items))
	for _, item := range res.Items {
		c := Container{Labels: item.Labels, Running: item.State == container.StateRunning}
		if len(item.Names) > 0 {
			c.Name = strings.TrimPrefix(item.Names[0], "/")
		}
		if item.NetworkSettings != nil {
			c.Networks = aliases(item.NetworkSettings.Networks)
		}
		containers = append(containers, c)
	}
	slices.SortFunc(containers, func(a, b Container) int { return strings.Compare(a.Name, b.Name) })
	return containers, nil
}

func (d *Daemon) Inspect(ctx context.Context, name string) (Container, error) {
	_, api, err := d.connect()
	if err != nil {
		return Container{}, err
	}
	res, err := api.ContainerInspect(ctx, name, client.ContainerInspectOptions{})
	if cerrdefs.IsNotFound(err) {
		return Container{}, fmt.Errorf("%s: %w", name, ErrNoContainer)
	}
	if err != nil {
		return Container{}, unavailable("inspect the container "+name, err)
	}
	c := Container{Name: name}
	if cfg := res.Container.Config; cfg != nil {
		c.Labels = cfg.Labels
	}
	if st := res.Container.State; st != nil {
		c.Running = st.Running
	}
	if ns := res.Container.NetworkSettings; ns != nil {
		c.Networks = aliases(ns.Networks)
	}
	return c, nil
}

func (d *Daemon) ConnectNetwork(ctx context.Context, net, container string, aliases []string) error {
	_, api, err := d.connect()
	if err != nil {
		return err
	}
	if _, err := api.NetworkConnect(ctx, net, client.NetworkConnectOptions{
		Container:      container,
		EndpointConfig: &network.EndpointSettings{Aliases: aliases},
	}); err != nil {
		return unavailable(fmt.Sprintf("connect %s to the network %s", container, net), err)
	}
	return nil
}

func (d *Daemon) DisconnectNetwork(ctx context.Context, net, container string) error {
	_, api, err := d.connect()
	if err != nil {
		return err
	}
	if _, err := api.NetworkDisconnect(ctx, net, client.NetworkDisconnectOptions{Container: container}); err != nil {
		return unavailable(fmt.Sprintf("disconnect %s from the network %s", container, net), err)
	}
	return nil
}

func aliases(networks map[string]*network.EndpointSettings) map[string][]string {
	out := make(map[string][]string, len(networks))
	for name, ep := range networks {
		if ep != nil {
			out[name] = slices.Clone(ep.Aliases)
		} else {
			out[name] = nil
		}
	}
	return out
}

// clashesWithLoopback reports whether a publish on ip holds 127.0.0.1, where
// the router publishes: one on every address does, one on another does not.
func clashesWithLoopback(ip netip.Addr) bool {
	return !ip.IsValid() || ip.IsUnspecified() || ip.Unmap() == netip.AddrFrom4([4]byte{127, 0, 0, 1})
}
