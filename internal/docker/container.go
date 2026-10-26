package docker

import (
	"context"
	"fmt"
	"slices"
	"strings"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

func (d *Daemon) Containers(ctx context.Context, label string) ([]Container, error) {
	_, api, err := d.connect()
	if err != nil {
		return nil, err
	}
	res, err := api.ContainerList(ctx, client.ContainerListOptions{
		Filters: make(client.Filters).Add("label", label),
	})
	if err != nil {
		return nil, unavailable("list Docker containers", err)
	}
	containers := make([]Container, 0, len(res.Items))
	for _, item := range res.Items {
		c := Container{Labels: item.Labels, Running: true}
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
