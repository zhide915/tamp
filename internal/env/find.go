package env

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/zhide915/tamp/internal/docker"
	"github.com/zhide915/tamp/internal/exitcode"
)

// target is the environment a command acts on.
type target struct {
	name, dir string
	// flag is the -e value that named it, empty when the working directory did.
	flag string
	// where is the path `tamp new` was given, when it is not the working
	// directory.
	where string
}

// find returns the environment that -e names, found by its containers'
// labels, or else the one whose directory is the working directory.
func (m *Manager) find(ctx context.Context, flag string) (target, error) {
	if flag != "" {
		containers, err := m.envContainers(ctx, flag)
		if err != nil {
			return target{}, err
		}
		if len(containers) == 0 {
			return target{}, exitcode.New(exitcode.CodeNotFound,
				fmt.Sprintf("no environment named %s has containers", flag),
				"run `tamp list` to see the environments, or run the command in the environment's directory")
		}
		return target{name: flag, dir: containers[0].Labels[LabelPath], flag: flag}, nil
	}
	dir, err := os.Getwd()
	if err != nil {
		return target{}, err
	}
	if _, err := os.Stat(filepath.Join(dir, RecipeFile)); errors.Is(err, fs.ErrNotExist) {
		return target{}, exitcode.Usage(fmt.Sprintf("no recipe in %s", dir),
			"use `-e <name>`, or run the command in an environment's directory")
	} else if err != nil {
		return target{}, exitcode.New(exitcode.CodeFailed, fmt.Sprintf("cannot read %s: %v", dir, err),
			"check that you can read the directory")
	}
	return target{name: filepath.Base(dir), dir: dir}, nil
}

// locate is find for a command that acts on existing containers: those that
// the working directory names must be its own, as the build requires.
func (m *Manager) locate(ctx context.Context, flag string) (target, error) {
	t, err := m.find(ctx, flag)
	if err != nil || t.flag != "" {
		return t, err
	}
	moved, err := m.owner(ctx, t)
	if moved != "" {
		m.p.Warn(fmt.Sprintf("%s moved from %s to %s: `tamp start` recreates its containers for the new directory", t.name, moved, t.dir))
	}
	return t, err
}

// owner checks that the containers named like t are t's: those whose
// directory still holds a recipe belong to another environment of the same
// name. It returns the directory of containers that hold none, which is
// where t moved from.
func (m *Manager) owner(ctx context.Context, t target) (moved string, err error) {
	containers, err := m.envContainers(ctx, t.name)
	if err != nil {
		return "", err
	}
	for _, c := range containers {
		path := c.Labels[LabelPath]
		if path == moved || samePath(path, t.dir) {
			continue
		}
		_, err := os.Stat(filepath.Join(path, RecipeFile))
		switch {
		case err == nil:
			return "", exitcode.New(exitcode.CodeFailed,
				fmt.Sprintf("the environment %s already exists at %s", t.name, path),
				"rename the directory")
		case !errors.Is(err, fs.ErrNotExist):
			return "", exitcode.New(exitcode.CodeFailed,
				fmt.Sprintf("cannot tell whether the environment %s at %s still exists: %v", t.name, path, err),
				"check that you can read "+path)
		}
		moved = path
	}
	return moved, nil
}

func samePath(a, b string) bool {
	ai, err := os.Stat(a)
	if err != nil {
		return false
	}
	bi, err := os.Stat(b)
	return err == nil && os.SameFile(ai, bi)
}

// envContainers lists one environment's containers, running or not.
func (m *Manager) envContainers(ctx context.Context, name string) ([]docker.Container, error) {
	all, err := m.docker.AllContainers(ctx, LabelEnv)
	if err != nil {
		return nil, err
	}
	var out []docker.Container
	for _, c := range all {
		if c.Labels[LabelEnv] == name {
			out = append(out, c)
		}
	}
	return out, nil
}

// command is a tamp command line that acts on t, for a fix: with -e when
// -e named it.
func (t target) command(args string) string {
	if t.flag != "" {
		return fmt.Sprintf("`tamp %s -e %s`", args, t.flag)
	}
	return fmt.Sprintf("`tamp %s`", args)
}
