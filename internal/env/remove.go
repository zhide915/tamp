package env

import (
	"context"
	"fmt"

	"github.com/zhide915/tamp/internal/engine"
	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/syncer"
)

// RemoveRequest is what `tamp rm` was asked for.
type RemoveRequest struct {
	Name string
	// Volumes destroys the storage layers too; without it they survive for
	// `tamp init` to re-adopt.
	Volumes bool
	// Yes replaces a prompt — agents run these commands, so confirmation is a
	// flag.
	Yes bool
}

// Remove tears down an environment's containers, network and registry entry.
// It never deletes the directory: the source lives there, and tamp does not
// destroy source.
func (m *Manager) Remove(ctx context.Context, req RemoveRequest) error {
	e, err := m.resolve(req.Name)
	if err != nil {
		return err
	}

	if !req.Yes {
		m.previewRemoval(e, req.Volumes)
		return exitcode.New(exitcode.CodeConfirmationRequired,
			fmt.Sprintf("removing %s is destructive", e.Name()),
			"add --yes once the list above is what you meant")
	}

	if _, err := m.Engine.Ping(ctx); err != nil {
		return err
	}

	// Both before the teardown: Docker refuses to remove a network with a
	// container still on it, and a sync whose far end vanished loops on errors.
	m.terminateSync(ctx, e)
	if err := m.router().Detach(ctx, e.Resources.Network()); err != nil {
		return err
	}

	removal := engine.KeepVolumes
	if req.Volumes && !e.sourceInVolume() {
		removal = engine.RemoveVolumes
	}
	if err := m.Engine.ComposeDown(ctx, e.project(), removal, m.Out.Stream()); err != nil {
		return err
	}
	if req.Volumes && e.sourceInVolume() {
		// One volume at a time: compose down --volumes cannot spare the code
		// volume, which holds the only copy of the source when sync is off.
		for _, layer := range e.disposableLayers() {
			if err := m.Engine.RemoveVolume(ctx, e.Resources.Volume(layer)); err != nil {
				return err
			}
		}
	}

	if err := UpdateRegistry(m.Home, func(reg Registry) error {
		delete(reg, string(e.Name()))
		return nil
	}); err != nil {
		return err
	}

	// Routes are assembled from the registry, so this drops only this
	// environment's.
	if _, err := m.refreshRoutes(ctx); err != nil {
		return err
	}

	m.Out.OK(fmt.Sprintf("%s removed", e.Name()))
	m.reportSurvivors(e, req.Volumes)
	return nil
}

// sourceInVolume reports whether the code volume holds the only copy of the
// source — true with sync off, where nothing mirrors it to the host.
func (e *Environment) sourceInVolume() bool { return e.Config.Sync.Mode == syncer.ModeOff }

// disposableLayers are the volumes --volumes destroys. The code volume is
// spared when it is the source: tamp never deletes source.
func (e *Environment) disposableLayers() []string {
	if e.sourceInVolume() {
		return []string{DataVolume, DepsVolume, SitesVolume}
	}
	return []string{DataVolume, CodeVolume, DepsVolume, SitesVolume}
}

func volumeNote(layer string) string {
	switch layer {
	case DataVolume:
		return "  (every site's database)"
	case SitesVolume:
		return "  (site files)"
	}
	return ""
}

// previewRemoval prints exactly what --yes would destroy — the point of the
// exit-5 contract.
func (m *Manager) previewRemoval(e *Environment, volumes bool) {
	m.Out.Print(fmt.Sprintf("tamp rm would destroy, in %s:", e.Name()))
	m.Out.Print("  containers  " + e.Resources.Project() + "-*")
	m.Out.Print("  network     " + e.Resources.Network())
	m.Out.Print("  registry    the entry naming " + string(e.Name()))
	if volumes {
		for _, layer := range e.disposableLayers() {
			m.Out.Print("  volume      " + e.Resources.Volume(layer) + volumeNote(layer))
		}
	}

	m.Out.Print("")
	m.Out.Print("it would keep:")
	m.Out.Print("  directory   " + e.Dir + "  (tamp never deletes it)")
	if !volumes {
		m.Out.Print("  volume      " + e.Resources.Volume(DataVolume) + volumeNote(DataVolume))
	} else if e.sourceInVolume() {
		m.Out.Print("  volume      " + e.Resources.Volume(CodeVolume) + "  (your source — sync is off)")
	}

	m.Out.Print("")
	m.Out.Print("to delete this environment completely:")
	m.Out.Print("  tamp rm " + string(e.Name()) + " --volumes --yes")
	if e.sourceInVolume() {
		m.Out.Print("  then docker volume rm " + e.Resources.Volume(CodeVolume) + "  (tamp spares it — it is your source)")
	}
	m.Out.Print("  then delete " + e.Dir + " yourself")
}

// reportSurvivors names what remains. Its recipe differs from the preview's
// on purpose: the registry entry is gone now, so `tamp rm --volumes` can no
// longer find the environment.
func (m *Manager) reportSurvivors(e *Environment, volumes bool) {
	if !volumes {
		volume := e.Resources.Volume(DataVolume)
		m.Out.Note("the data volume " + volume + " survives, with every site's database in it")
		m.Out.Hint("bring it back with its data: run 'tamp init' in " + e.Dir)
		m.Out.Hint("remove it for good: docker volume rm " + volume)
	} else if e.sourceInVolume() {
		volume := e.Resources.Volume(CodeVolume)
		m.Out.Note("the code volume " + volume + " survives — with sync off it holds your source")
		m.Out.Hint("copy the source out, or remove it yourself: docker volume rm " + volume)
	}
	m.Out.Note(e.Dir + " was not touched — tamp never deletes your source code")
	m.Out.Hint("delete it yourself when you are done with it")
}
