package engine

import (
	"context"
	"fmt"

	"github.com/moby/moby/client"
	"github.com/zhide915/tamp/internal/exitcode"
)

// InspectNetwork reports a network and what is attached to it, and reports a
// name the machine has no network for as nil rather than as a failure.
//
// The absence is an ordinary answer here: tamp carries routes for
// environments that are stopped, and a stopped environment whose network has
// been taken down is simply nothing for the router to attach to.
func (d *Docker) InspectNetwork(ctx context.Context, name string) (*Network, error) {
	_, api, err := d.connect()
	if err != nil {
		return nil, err
	}

	// Listed before it is inspected because the list distinguishes "no such
	// network" from every other failure without tamp having to interpret the
	// daemon's status codes; inspect alone would return one error for both.
	found, err := api.NetworkList(ctx, client.NetworkListOptions{
		// The daemon's name filter matches substrings, so the exact name is
		// still tamp's to pick out below.
		Filters: make(client.Filters).Add("name", name),
	})
	if err != nil {
		return nil, networkError("list Docker networks", err)
	}
	exists := false
	for _, item := range found.Items {
		if item.Name == name {
			exists = true
			break
		}
	}
	if !exists {
		return nil, nil
	}

	res, err := api.NetworkInspect(ctx, name, client.NetworkInspectOptions{})
	if err != nil {
		return nil, networkError("inspect the network "+name, err)
	}
	net := &Network{Name: name}
	for _, endpoint := range res.Network.Containers {
		net.Containers = append(net.Containers, endpoint.Name)
	}
	return net, nil
}

func (d *Docker) ConnectNetwork(ctx context.Context, network, container string) error {
	_, api, err := d.connect()
	if err != nil {
		return err
	}
	_, err = api.NetworkConnect(ctx, network, client.NetworkConnectOptions{Container: container})
	if err != nil {
		return networkError(fmt.Sprintf("attach %s to the network %s", container, network), err)
	}
	return nil
}

func (d *Docker) DisconnectNetwork(ctx context.Context, network, container string) error {
	_, api, err := d.connect()
	if err != nil {
		return err
	}
	_, err = api.NetworkDisconnect(ctx, network, client.NetworkDisconnectOptions{Container: container})
	if err != nil {
		return networkError(fmt.Sprintf("detach %s from the network %s", container, network), err)
	}
	return nil
}

func networkError(what string, err error) error {
	return exitcode.New(exitcode.CodeEngineUnavailable,
		fmt.Sprintf("cannot %s: %v", what, rootCause(err)),
		"start Docker and try again")
}
