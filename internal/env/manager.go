// Package env is the manager: the recipe, the build, and the behavior of
// every command.
package env

import (
	"context"

	"github.com/zhide915/tamp/internal/docker"
	"github.com/zhide915/tamp/internal/ui"
)

// Manager runs tamp's commands.
type Manager struct {
	p      *ui.Printer
	docker docker.Client
	// browser hands a URL to the default browser.
	browser func(ctx context.Context, url string) error
}

func NewManager(p *ui.Printer, dc docker.Client, browser func(context.Context, string) error) *Manager {
	return &Manager{p: p, docker: dc, browser: browser}
}
