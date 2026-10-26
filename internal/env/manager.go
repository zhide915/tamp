// Package env is the manager: the recipe, the build, and the behavior of
// every command.
package env

import (
	"github.com/zhide915/tamp/internal/docker"
	"github.com/zhide915/tamp/internal/ui"
)

// Manager runs tamp's commands.
type Manager struct {
	p      *ui.Printer
	docker docker.Client
}

func NewManager(p *ui.Printer, dc docker.Client) *Manager {
	return &Manager{p: p, docker: dc}
}
