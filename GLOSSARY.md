# tamp

tamp is an environment manager for Frappe Framework: it runs each Frappe bench in Docker containers and serves every site by hostname. These terms are the vocabulary for code, CLI output, and docs.

## Language

**Environment**:
The unit tamp manages: one directory holding a recipe, and the bench and containers built from it. Its name is the directory name.
_Avoid_: project, instance, bench as a synonym

**Recipe**:
The `tamp.toml` file that states what an environment should contain: its Frappe branch, its apps, and its sites. A team shares an environment by sharing its recipe.
_Avoid_: config, manifest, spec

**Bench**:
The Frappe workspace inside an environment: its apps, sites, and processes.
_Avoid_: environment as a synonym

**App**:
A Frappe app in the bench, kept in the environment's `apps/` folder: cloned from a git repository, or created there. Every site has every app.
_Avoid_: module, plugin, package

**Site**:
A Frappe site on the bench, named exactly as its `*.localhost` hostname. One site has one database.
_Avoid_: tenant, domain

**Router**:
The single container per machine that serves every site of every running environment by hostname.
_Avoid_: proxy, gateway

**Sync session**:
The two-way copy of an environment's `apps/` folder between the host and its container, on Windows and macOS. Linux has no sync session: the container uses the host folder directly.
_Avoid_: mount, watcher
