package env

import (
	"context"
	"fmt"

	"github.com/zhide915/tamp/internal/engine"
	"github.com/zhide915/tamp/internal/exitcode"
)

// RemoveRequest is what `tamp rm` was asked for.
type RemoveRequest struct {
	Name string
	// Volumes destroys the environment's storage layers along with it.
	// Without it they survive, and `tamp init` in the leftover directory
	// re-adopts them.
	Volumes bool
	// Yes confirms a destructive action. tamp never prompts: agents run
	// these commands, so confirmation is a flag, not a question.
	Yes bool
}

// Remove takes an environment's containers, network and registry entry away.
//
// It never deletes the environment directory. The source layer lives there and
// tamp does not destroy source — so "remove" here always means
// "remove what tamp made", and the output says so rather than leaving the
// user to discover it.
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

	// Before the teardown, not after: Docker refuses to remove a network that
	// still has a container attached, and the router is attached to every
	// environment's.
	if err := m.router().Detach(ctx, e.Resources.Network()); err != nil {
		return err
	}

	removal := engine.KeepVolumes
	if req.Volumes {
		removal = engine.RemoveVolumes
	}
	if err := m.Engine.ComposeDown(ctx, e.project(), removal, m.Out.Stream()); err != nil {
		return err
	}

	if err := UpdateRegistry(m.Home, func(reg Registry) error {
		delete(reg, string(e.Name()))
		return nil
	}); err != nil {
		return err
	}

	// The registry is what the routes are assembled from, so this is what
	// takes this environment's routes away — and, just as importantly, leaves
	// every other environment's in place.
	if _, err := m.refreshRoutes(ctx); err != nil {
		return err
	}

	m.Out.OK(fmt.Sprintf("%s removed", e.Name()))
	m.reportSurvivors(e, req.Volumes)
	return nil
}

// previewRemoval prints exactly what --yes would destroy, and — because this
// is where it is still actionable — the recipe for removing the environment
// completely.
//
// This is the whole value of the exit-5 contract: the answer to "what am I
// confirming" has to be on screen before the user retypes the command.
func (m *Manager) previewRemoval(e *Environment, volumes bool) {
	dataVolume := "  volume      " + e.Resources.Volume(DataVolume) + "  (every site's database)"

	m.Out.Print(fmt.Sprintf("tamp rm would destroy, in %s:", e.Name()))
	m.Out.Print("  containers  " + e.Resources.Project() + "-*")
	m.Out.Print("  network     " + e.Resources.Network())
	m.Out.Print("  registry    the entry naming " + string(e.Name()))
	if volumes {
		m.Out.Print(dataVolume)
	}

	m.Out.Print("")
	m.Out.Print("it would keep:")
	m.Out.Print("  directory   " + e.Dir + "  (tamp never deletes it)")
	if !volumes {
		m.Out.Print(dataVolume)
	}

	m.Out.Print("")
	m.Out.Print("to delete this environment completely:")
	m.Out.Print("  tamp rm " + string(e.Name()) + " --volumes --yes")
	m.Out.Print("  then delete " + e.Dir + " yourself")
}

// reportSurvivors says what is still on the machine, and how to finish the job.
//
// The recipe printed here is deliberately not the one in the preview: by now
// the registry entry is gone, so `tamp rm --volumes` would only report an
// environment it can no longer find. What is left is a Docker volume and a
// directory, and those are what the user is told about.
func (m *Manager) reportSurvivors(e *Environment, volumes bool) {
	if !volumes {
		volume := e.Resources.Volume(DataVolume)
		m.Out.Note("the data volume " + volume + " survives, with every site's database in it")
		m.Out.Hint("remove it too: docker volume rm " + volume)
	}
	m.Out.Note(e.Dir + " was not touched — tamp never deletes your source code")
	m.Out.Hint("delete it yourself when you are done with it")
}
