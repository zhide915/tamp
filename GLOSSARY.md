# tamp

tamp is an environment manager for Frappe Framework. It builds each Frappe bench in Docker and serves every site at its own hostname. These terms are the vocabulary of the code, the output, and the docs.

## Language

**Environment**:
The unit that tamp manages: a directory with a recipe, and the bench and containers built from it. Its name is the name of its directory.
_Avoid_: project, instance, bench as a synonym

**Recipe**:
The `tamp.toml` file that states what an environment should hold: its Frappe branch, its apps, and its sites. A team shares an environment by sharing its recipe.
_Avoid_: config, manifest, spec

**Bench**:
The Frappe workspace inside an environment: its apps, its sites, and its processes.
_Avoid_: environment as a synonym

**App**:
A Frappe app of the bench, kept in the `apps/` folder of the environment: cloned from a git repository, or created there. Every site has every app.
_Avoid_: module, plugin, package

**Site**:
A Frappe site of the bench, whose name is exactly its `*.localhost` hostname, with a database of its own.
_Avoid_: tenant, domain

**Router**:
The one container on a machine that serves every site of every running environment by hostname.
_Avoid_: proxy, gateway

**Sync session**:
The two-way copy of the `apps/` folder of an environment between the host and its container, on Windows and macOS. Linux has none, because the container uses the host folder directly.
_Avoid_: mount, watcher
