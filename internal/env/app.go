package env

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/zhide915/tamp/internal/docker"
	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/frappe"
)

// resolveApp makes apps/<name> hold the app at url and returns it as the
// recipe records it. A folder apps/ already holds is adopted, not cloned.
// Otherwise the clone lands in a temp folder until its package names it.
// retry ends the fix of a folder conflict, such as ", then run `tamp app add x`".
func (m *Manager) resolveApp(ctx context.Context, dir, url, branch, retry string) (App, error) {
	apps := filepath.Join(dir, AppsDir)
	repo := repoName(url)
	if isDir(filepath.Join(apps, repo)) {
		return adoptApp(dir, repo, url, branch, retry)
	}
	// A clone kept by `tamp app remove` sits under its package's name.
	entries, err := os.ReadDir(apps)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return App{}, exitcode.New(exitcode.CodeFailed, fmt.Sprintf("cannot read %s: %v", apps, err),
			"check that you can read the directory")
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if got, err := remoteURL(filepath.Join(apps, e.Name())); err != nil {
			return App{}, err
		} else if got == url {
			return adoptApp(dir, e.Name(), url, branch, retry)
		}
	}
	if err := os.MkdirAll(apps, 0o755); err != nil {
		return App{}, exitcode.New(exitcode.CodeFailed, fmt.Sprintf("cannot create %s: %v", apps, err),
			"check that you can write to the directory")
	}
	tmp, err := os.MkdirTemp(apps, "."+repo+"-")
	if err != nil {
		return App{}, exitcode.New(exitcode.CodeFailed, fmt.Sprintf("cannot create a folder in %s: %v", apps, err),
			"check that you can write to the directory")
	}
	m.p.Step("cloning " + url)
	app, err := m.cloneInto(ctx, tmp, url, branch)
	if err != nil || isDir(filepath.Join(apps, app.Name)) {
		// The temp clone is tamp's own; nothing else refers to it yet.
		if rmErr := os.RemoveAll(tmp); rmErr != nil {
			return App{}, exitcode.New(exitcode.CodeFailed, fmt.Sprintf("cannot remove the temp clone %s: %v", tmp, rmErr),
				"delete the folder")
		}
		if err != nil {
			return App{}, err
		}
		return adoptApp(dir, app.Name, url, branch, retry)
	}
	if err := os.Rename(tmp, filepath.Join(apps, app.Name)); err != nil {
		return App{}, exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot move the clone of %s to %s: %v", url, filepath.Join(apps, app.Name), err),
			"check that nothing holds files open in "+apps)
	}
	return app, nil
}

func (m *Manager) cloneInto(ctx context.Context, tmp, url, branch string) (App, error) {
	if err := gitClone(ctx, url, branch, tmp, m.p.Progress()); err != nil {
		return App{}, err
	}
	name, err := appNameOf(tmp, url)
	if err != nil {
		return App{}, err
	}
	if branch == "" {
		if branch, err = currentBranch(tmp); err != nil {
			return App{}, err
		}
	}
	return App{Name: name, URL: url, Branch: branch}, nil
}

// adoptApp records the app that apps/<folder> already holds.
func adoptApp(dir, folder, url, branch, retry string) (App, error) {
	clone := filepath.Join(dir, AppsDir, folder)
	name, err := appNameOf(clone, url)
	if err != nil {
		return App{}, err
	}
	if name != folder {
		return App{}, exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("%s holds the app %s, but bench needs the folder named after the app", clone, name),
			"rename the folder to "+filepath.Join(dir, AppsDir, name)+retry)
	}
	if branch == "" {
		if branch, err = currentBranch(clone); err != nil {
			return App{}, err
		}
	}
	if branch == "" {
		return App{}, exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("%s is not on a branch, so tamp cannot record one", clone),
			"check out a branch in "+clone)
	}
	return App{Name: name, URL: url, Branch: branch}, nil
}

func appNameOf(clone, url string) (string, error) {
	name, err := packageName(clone)
	if err != nil {
		return "", exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot read the app's name from %s: %v", url, err),
			"check that the repository holds a Frappe app")
	}
	if !appName.MatchString(name) || name == "frappe" {
		return "", exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("the package of %s is named %q, which is not a Frappe app name", url, name),
			"check that the repository holds a Frappe app")
	}
	return name, nil
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// AppAdd records the app spec names in the recipe and builds the
// environment, which installs it into the bench and on every site.
func (m *Manager) AppAdd(envFlag, spec string) error {
	url, branch, err := parseAppSpec(spec)
	if err != nil {
		return err
	}
	ctx := context.Background()
	t, err := m.find(ctx, envFlag)
	if err != nil {
		return err
	}
	r, err := readRecipe(t.dir)
	if err != nil {
		return err
	}
	for _, a := range r.Apps {
		if a.URL == url || a.Name == repoName(url) {
			return t.alreadyLists(a, branch)
		}
	}
	if err := m.docker.Ping(ctx); err != nil {
		return err
	}
	// The build makes the same check, but only after the recipe is written.
	if _, err := m.owner(ctx, t); err != nil {
		return err
	}
	app, err := m.resolveApp(ctx, t.dir, url, branch, ", then run "+t.command("app add "+spec))
	var failed cloneFailed
	if errors.As(err, &failed) {
		return exitcode.New(failed.err.Code, failed.err.Msg, failed.err.Fix+", then run "+t.command("app add "+spec))
	}
	if err != nil {
		return err
	}
	if i := slices.Index(r.names(), app.Name); i >= 0 {
		return t.alreadyLists(r.Apps[i], branch)
	}
	// An app goes before the first app that requires it, as dependencies come first.
	at := len(r.Apps)
	for i, o := range r.Apps {
		required, err := requiredApps(filepath.Join(t.dir, AppsDir, o.Name), o.Name)
		if err != nil {
			return exitcode.New(exitcode.CodeFailed, fmt.Sprintf("cannot read the hooks of %s: %v", o.Name, err),
				"check that you can read "+filepath.Join(t.dir, AppsDir, o.Name))
		}
		if o.Name != "" && slices.ContainsFunc(required, func(e string) bool { return repoName(e) == app.Name }) {
			at = i
			break
		}
	}
	r.Apps = slices.Insert(r.Apps, at, app)
	if err := writeRecipe(t.dir, r); err != nil {
		return err
	}
	m.p.OK(fmt.Sprintf("added the app %s at %s to the recipe", app.Name, app.Branch))
	return m.build(t, r)
}

// alreadyLists refuses an app the recipe lists. A command that asks for another
// branch is pointed at where the branch is changed.
func (t target) alreadyLists(a App, branch string) error {
	name := a.Name
	if name == "" {
		name = a.URL
	}
	msg := fmt.Sprintf("%s already lists the app %s", t.name, name)
	if branch == "" || branch == a.Branch {
		return exitcode.New(exitcode.CodeFailed, msg, "run "+t.command("start")+" to build it")
	}
	if a.Branch != "" {
		msg += " at " + a.Branch
	}
	fix := fmt.Sprintf("to use %s, set its branch in %s", branch, filepath.Join(t.dir, RecipeFile))
	if a.Name != "" {
		fix += fmt.Sprintf(" and check out %s in %s", branch, filepath.Join(t.dir, AppsDir, a.Name))
	}
	return exitcode.New(exitcode.CodeFailed, msg, fix+", then run "+t.command("start"))
}

// AppList prints Frappe and each app of the recipe with its branch and URL.
func (m *Manager) AppList(envFlag string) error {
	t, err := m.locate(context.Background(), envFlag)
	if err != nil {
		return err
	}
	r, err := readRecipe(t.dir)
	if err != nil {
		return err
	}
	rows := [][3]string{{"frappe", r.Frappe, FrappeURL}}
	for _, a := range r.Apps {
		name, branch := a.Name, a.Branch
		// The next build clones it and fills these in.
		if name == "" {
			name = "(not cloned)"
		}
		if branch == "" {
			branch = "(default)"
		}
		rows = append(rows, [3]string{name, branch, a.URL})
	}
	var name, branch int
	for _, row := range rows {
		name, branch = max(name, len(row[0])), max(branch, len(row[1]))
	}
	for _, row := range rows {
		m.p.Print(fmt.Sprintf("%-*s  %-*s  %s", name, row[0], branch, row[1], row[2]))
	}
	return nil
}

// AppRemove uninstalls an app from every site and from the bench, and
// removes it from the recipe. The clone stays: it may hold the user's work.
func (m *Manager) AppRemove(envFlag, name string, yes bool) error {
	if !appName.MatchString(name) {
		return exitcode.Usage(fmt.Sprintf("%q is not an app name", name), "give the app's name, as `tamp app list` prints it")
	}
	ctx := context.Background()
	t, err := m.locate(ctx, envFlag)
	if err != nil {
		return err
	}
	if name == "frappe" {
		return exitcode.New(exitcode.CodeFailed, "Frappe is the bench itself, not an app tamp can remove",
			"remove the whole environment with "+t.command("remove --yes"))
	}
	r, err := readRecipe(t.dir)
	if err != nil {
		return err
	}
	if !slices.Contains(r.names(), name) {
		return exitcode.New(exitcode.CodeNotFound, fmt.Sprintf("%s has no app %s", t.name, name),
			"run "+t.command("app list")+" to see its apps")
	}

	container := frappeContainer(t.name)
	c, err := m.docker.Inspect(ctx, container)
	built := false
	switch {
	case errors.Is(err, docker.ErrNoContainer):
	case err != nil:
		return err
	case !c.Running:
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("%s is stopped, so tamp cannot uninstall the app %s from its sites", t.name, name),
			"run "+t.command("start")+", then "+t.command("app remove "+name+" --yes"))
	default:
		built = true
	}
	if !yes {
		return exitcode.New(exitcode.CodeNeedsYes,
			fmt.Sprintf("removing the app %s deletes its data from every site", name),
			"run "+t.command("app remove "+name+" --yes")+" to confirm")
	}

	if built {
		b := bench{m: m, ctx: ctx, container: container}
		if err := b.uninstall(name); err != nil {
			return err
		}
	}
	r.Apps = slices.DeleteFunc(r.Apps, func(a App) bool { return a.Name == name })
	if err := writeRecipe(t.dir, r); err != nil {
		return err
	}
	m.p.OK("removed the app " + name + " from " + t.name)
	m.p.Print("kept the clone at " + filepath.Join(t.dir, AppsDir, name))
	return nil
}

// uninstall removes an app from each site that has it, then from the bench.
func (b bench) uninstall(name string) error {
	hosts, err := b.sites()
	if err != nil {
		return err
	}
	for _, host := range hosts {
		apps, err := b.siteApps(host)
		if err != nil {
			return err
		}
		if !slices.Contains(apps, name) {
			continue
		}
		b.m.p.Step(fmt.Sprintf("uninstalling the app %s from %s", name, host))
		if err := b.run(frappe.UninstallSiteApp(host, name)); err != nil {
			return err
		}
	}
	if err := b.writeAppsTxt(func(apps []string) []string {
		return slices.DeleteFunc(apps, func(a string) bool { return a == name })
	}); err != nil {
		return err
	}
	installed, err := b.exists(frappe.AppInstalled(name))
	if err != nil || !installed {
		return err
	}
	b.m.p.Step("uninstalling the app " + name + " from the bench")
	if err := b.run(frappe.UninstallApp(name)); err != nil {
		return err
	}
	if err := b.run([]string{"rm", "-f", frappe.AppInstalled(name)}); err != nil {
		return err
	}
	return b.run(frappe.RestartProcesses())
}

// installApps installs each app into the bench in recipe order, the way bench
// get-app installs: editable into the Python environment, listed in apps.txt,
// then node packages and assets, Frappe's first. Running processes are paused
// for the node and asset steps. It reports whether the bench's Python
// environment changed and whether it paused the processes.
func (b bench) installApps(dir string, r Recipe, running bool) (changed, paused bool, err error) {
	for _, a := range r.Apps {
		installed, err := b.exists(frappe.AppInstalled(a.Name))
		if err != nil {
			return false, false, err
		}
		if installed {
			continue
		}
		b.m.p.Step("installing the app " + a.Name + " into the bench")
		if err := b.run(frappe.InstallApp(a.Name)); err != nil {
			return false, false, err
		}
		if err := b.m.docker.WriteFile(b.ctx, b.container, frappe.AppInstalled(a.Name), nil); err != nil {
			return false, false, err
		}
		changed = true
	}
	if err := b.writeAppsTxt(func(apps []string) []string { return appsTxt(apps, r.names()) }); err != nil {
		return false, false, err
	}
	var plans []assetPlan
	for _, name := range append([]string{"frappe"}, r.names()...) {
		p, err := b.planAssets(dir, name)
		if err != nil {
			return false, false, err
		}
		if p.packages || p.build {
			plans = append(plans, p)
		}
	}
	if running && len(plans) > 0 {
		// A running process keeps files open in node_modules, and a fresh
		// clone's yarn install then fails on Docker Desktop's bind mount.
		b.m.p.Step("pausing the bench's processes")
		if err := b.m.docker.WriteFile(b.ctx, b.container, frappe.Paused, nil); err != nil {
			return false, false, err
		}
		if err := b.run(frappe.StopProcesses()); err != nil {
			return false, false, err
		}
		paused = true
	}
	for _, p := range plans {
		if err := b.setupAssets(p); err != nil {
			return false, false, err
		}
	}
	return changed, paused, nil
}

// assetPlan is the node and asset work one app needs.
type assetPlan struct {
	name, inClone   string
	packages, build bool
}

// planAssets plans an app's node packages unless its clone holds them, and
// its assets unless both the clone and the bench volume hold them: either can
// be new while the other is kept.
func (b bench) planAssets(dir, name string) (assetPlan, error) {
	p := assetPlan{name: name, inClone: frappe.AssetsInClone(name, isDir(filepath.Join(dir, AppsDir, name, ".git")))}
	_, err := os.Stat(filepath.Join(dir, AppsDir, name, "package.json"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return p, exitcode.New(exitcode.CodeFailed, fmt.Sprintf("cannot read %s: %v", filepath.Join(dir, AppsDir, name), err),
			"check that you can read the directory")
	}
	if err == nil {
		installed, err := b.exists(frappe.NodeInstalled(name))
		if err != nil {
			return p, err
		}
		p.packages = !installed
	}
	// bench build writes into both the clone and the bench volume, and new
	// packages may change what it builds.
	p.build = p.packages
	for _, marker := range []string{frappe.AssetsBuilt(name), p.inClone} {
		ok, err := b.exists(marker)
		if err != nil {
			return p, err
		}
		p.build = p.build || !ok
	}
	return p, nil
}

func (b bench) setupAssets(p assetPlan) error {
	if p.packages {
		b.m.p.Step("installing the node packages of " + p.name)
		if err := b.run(frappe.NodePackages(p.name)); err != nil {
			return err
		}
		if err := b.m.docker.WriteFile(b.ctx, b.container, frappe.NodeInstalled(p.name), nil); err != nil {
			return err
		}
	}
	if !p.build {
		return nil
	}
	b.m.p.Step("building the assets of " + p.name)
	if err := b.run(frappe.BuildAssets(p.name)); err != nil {
		return err
	}
	for _, marker := range []string{frappe.AssetsBuilt(p.name), p.inClone} {
		if err := b.m.docker.WriteFile(b.ctx, b.container, marker, nil); err != nil {
			return err
		}
	}
	return nil
}

// appsTxt orders apps.txt as Frappe, then the recipe's apps, then any app
// installed some other way, which tamp keeps.
func appsTxt(existing, recipe []string) []string {
	out := append([]string{"frappe"}, recipe...)
	for _, a := range existing {
		if !slices.Contains(out, a) {
			out = append(out, a)
		}
	}
	return out
}

func (b bench) writeAppsTxt(edit func([]string) []string) error {
	body, err := b.read(frappe.AppsTxt)
	if err != nil && !errors.Is(err, docker.ErrNotExist) {
		return err
	}
	apps := strings.Fields(string(body))
	next := edit(slices.Clone(apps))
	if slices.Equal(next, apps) {
		return nil
	}
	return b.m.docker.WriteFile(b.ctx, b.container, frappe.AppsTxt, []byte(strings.Join(next, "\n")+"\n"))
}

// ensureSiteApps installs on a site each app of the recipe it lacks, in
// recipe order.
func (b bench) ensureSiteApps(host string, r Recipe) error {
	if len(r.Apps) == 0 {
		return nil
	}
	have, err := b.siteApps(host)
	if err != nil {
		return err
	}
	for _, name := range r.names() {
		if slices.Contains(have, name) {
			continue
		}
		b.m.p.Step(fmt.Sprintf("installing the app %s on %s", name, host))
		if err := b.run(frappe.InstallSiteApp(host, name)); err != nil {
			return err
		}
	}
	return nil
}

func (b bench) siteApps(host string) ([]string, error) {
	var out bytes.Buffer
	if err := b.m.docker.Exec(b.ctx, docker.Exec{Container: b.container, Cmd: frappe.SiteApps(host),
		Stdout: &out, Stderr: b.m.p.Progress()}); err != nil {
		return nil, err
	}
	return frappe.ParseSiteApps(out.Bytes(), host)
}

func (b bench) exists(path string) (bool, error) {
	_, err := b.read(path)
	if errors.Is(err, docker.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// sites are the sites the bench holds.
func (b bench) sites() ([]string, error) {
	var out bytes.Buffer
	if err := b.m.docker.Exec(b.ctx, docker.Exec{Container: b.container, Cmd: frappe.ListSites(), Stdout: &out}); err != nil {
		return nil, err
	}
	return strings.Fields(out.String()), nil
}

// warnClones warns about each clone on a branch other than the recipe's and
// each folder in apps/ that the recipe does not list. It never changes a clone.
func (m *Manager) warnClones(t target, r Recipe) error {
	apps := filepath.Join(t.dir, AppsDir)
	want := map[string]string{"frappe": r.Frappe}
	for _, a := range r.Apps {
		want[a.Name] = a.Branch
	}
	for _, name := range append([]string{"frappe"}, r.names()...) {
		clone := filepath.Join(apps, name)
		got, err := currentBranch(clone)
		if err != nil {
			// A warning only: a clone tamp cannot read still builds or fails on its own.
			m.p.Warn(err.Error())
			continue
		}
		if got != want[name] {
			on := "is on " + got
			if got == "" {
				on = "is not on a branch"
			}
			m.p.Warn(fmt.Sprintf("%s %s, but the recipe names %s: check out %s there, or change its branch in %s",
				clone, on, want[name], want[name], filepath.Join(t.dir, RecipeFile)))
		}
	}
	entries, err := os.ReadDir(apps)
	if err != nil {
		return exitcode.New(exitcode.CodeFailed, fmt.Sprintf("cannot read %s: %v", apps, err),
			"check that you can read the directory")
	}
	for _, e := range entries {
		if _, listed := want[e.Name()]; listed || !e.IsDir() {
			continue
		}
		folder := filepath.Join(apps, e.Name())
		url, err := remoteURL(folder)
		if err != nil {
			m.p.Warn(err.Error())
		}
		if url == "" {
			url = "<url>"
		}
		m.p.Warn(fmt.Sprintf("%s is not in the recipe: add it with %s, or delete the folder",
			folder, t.command("app add "+url)))
	}
	return nil
}

// checkRequired stops the build when an app requires one the recipe does not
// list: bench cannot install it on a site.
func checkRequired(t target, r Recipe) error {
	for _, a := range r.Apps {
		required, err := requiredApps(filepath.Join(t.dir, AppsDir, a.Name), a.Name)
		if err != nil {
			return exitcode.New(exitcode.CodeFailed, fmt.Sprintf("cannot read the hooks of %s: %v", a.Name, err),
				"check that you can read "+filepath.Join(t.dir, AppsDir, a.Name))
		}
		for _, entry := range required {
			need := repoName(entry)
			if need == "frappe" || slices.Contains(r.names(), need) {
				continue
			}
			// Frappe installs a required app by its repository's name, so
			// adding the repository again would not help.
			if i := slices.IndexFunc(r.Apps, func(o App) bool { return repoName(o.URL) == need }); i >= 0 {
				return exitcode.New(exitcode.CodeFailed,
					fmt.Sprintf("the app %s requires %s, whose package is named %s, and Frappe looks for an app named %s",
						a.Name, entry, r.Apps[i].Name, need),
					fmt.Sprintf("name it %q in required_apps in %s", r.Apps[i].Name,
						filepath.Join(t.dir, AppsDir, a.Name, a.Name, "hooks.py")))
			}
			return exitcode.New(exitcode.CodeFailed,
				fmt.Sprintf("the app %s requires the app %s, which the recipe does not list", a.Name, need),
				"add it with "+t.command("app add "+requiredAppSpec(entry)))
		}
	}
	return nil
}

// cloneApps clones each app of the recipe that apps/ lacks, the way bench
// get-app clones. An app the recipe gives only by url is named from its
// clone, and the recipe records its name and branch.
func (m *Manager) cloneApps(ctx context.Context, t target, r Recipe) error {
	for i, a := range r.Apps {
		got, err := a, error(nil)
		if a.Name == "" {
			got, err = m.resolveApp(ctx, t.dir, a.URL, a.Branch, "")
		} else {
			err = m.cloneApp(ctx, t.dir, a.Name, a.URL, a.Branch)
			if err == nil && a.Branch == "" {
				got.Branch, err = currentBranch(filepath.Join(t.dir, AppsDir, a.Name))
			}
		}
		var failed cloneFailed
		if errors.As(err, &failed) {
			return exitcode.New(failed.err.Code, failed.err.Msg, failed.err.Fix+", or correct the app's url in "+filepath.Join(t.dir, RecipeFile))
		}
		if err != nil {
			return err
		}
		if got == a {
			continue
		}
		for _, o := range r.Apps {
			if o.Name == got.Name && o.URL != got.URL {
				return exitcode.New(exitcode.CodeFailed,
					fmt.Sprintf("%s and %s both hold the app %s", o.URL, got.URL, got.Name),
					"remove one of them from "+filepath.Join(t.dir, RecipeFile))
			}
		}
		// Recorded at once, so a later failure keeps what this clone found.
		r.Apps[i] = got
		if err := writeRecipe(t.dir, r); err != nil {
			return err
		}
	}
	return nil
}
