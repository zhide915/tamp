# tamp

tamp is a command-line environment manager for [Frappe Framework](https://frappeframework.com).
It runs each Frappe bench in its own set of Docker containers, with the
Python, Node, and MariaDB versions that its Frappe release requires, and
serves every site by hostname — `http://shop.localhost`, no ports to
remember. The app source code lives in a normal folder on your machine, so
you and your editor or coding agent work on it at native speed on Windows,
macOS, or Linux.

You need tamp and Docker. tamp downloads everything else it uses, pinned and
checksum-verified.

## Install

1. Install [Docker Desktop](https://docs.docker.com/desktop/) (or Docker
   Engine with the Compose v2 plugin on Linux) and start it.
2. Install tamp:

   ```sh
   go install github.com/zhide915/tamp/cmd/tamp@latest
   ```

3. Check the setup:

   ```sh
   tamp doctor
   ```

   Every check prints pass, warn, or fail, and each failure names its fix.

## Create an environment

```sh
tamp create erp15 --frappe version-15 --apps erpnext:version-15
```

This creates the folder `./erp15`, writes `tamp.toml`, starts the containers
(bench, MariaDB, two Redis instances, and [Mailpit](https://mailpit.axllent.org)
for mail), installs the pinned toolchain, initializes the bench, and fetches
the apps you listed. When it finishes, the development server is running.

Supported Frappe versions:

| `--frappe`   | Python | Node | MariaDB |
| ------------ | ------ | ---- | ------- |
| `version-15` | 3.11   | 18   | 10.8   |
| `version-16` | 3.14   | 24   | 11.8    |
| `develop`    | latest supported set |||

The first environment of a Frappe version pays for `bench init` in full. tamp
caches that initialized bench machine-wide, so later creates of the same
version unpack it in seconds. Templates expire after 14 days, because release
branches move. Use `--no-cache` to build a fresh bench and leave the stored
template alone.

Emptying the cache is safe. It costs the next create its full price and
nothing else:

```sh
docker run --rm -v tamp-templates:/store alpine sh -c 'rm -f /store/*'
```

Always pin an app's branch (`erpnext:version-15`). A bare name fetches the
repository's default branch — usually `develop`, which doesn't run on a
release bench — and tamp tells you it did rather than guessing a branch for
you.

`tamp init` does the same for a directory you already have: run it inside an
empty folder and the environment takes the folder's name.

## Private app repositories

Give a private repository's https URL like any other app. When the fetch
needs credentials, tamp asks the credential system your own git uses. Its
usual sign-in prompt may appear, at most once per git host in a run. The
credential serves that one fetch and is never stored. The clone in `apps/`
keeps a clean URL, so host git pushes and pulls normally afterwards.

tamp checks every app source right after the containers start, so a typo, a
deleted repository, or a missing sign-in fails in seconds instead of after
the bench build. Two spellings are refused up front: ssh URLs (use the
https form) and URLs with an embedded token (drop it; tamp asks your
credential system instead). Only private fetches need git installed on the
host. `tamp doctor` reports whether it was found.

## Add a site

```sh
tamp site new erp15 shop.localhost --apps erpnext
```

Open `http://shop.localhost` in a browser. The site's generated Administrator
password is printed once; store it. Each site gets its own database, and mail
the site sends appears at `http://mail.erp15.localhost`.

A site can only install apps that are already on the bench. To add one later:

```sh
tamp exec erp15 -- bench get-app hrms --branch version-15
```

Hostnames are unique across the whole machine, because one router serves them
all.

## Fast sites

Installing ERPNext onto a site takes minutes. tamp pays that once: the first
site of a Frappe version and app set is backed up into a machine-wide seed
store, and `--seed` restores that backup instead of installing the apps
again.

```sh
tamp site new erp15 shop.localhost --apps erpnext            # installs, and caches the result
tamp site new erp15 second.localhost --apps erpnext --seed   # restores it, in seconds
```

A seed never expires: tamp migrates the restored site, which absorbs whatever
the apps' schemas did since. Seeds don't cross versions or app sets — a
`version-16` bench never restores a `version-15` seed — and `--seed` with no
matching seed exits 3, naming the combination, having created nothing.

`--seed` goes with `--apps`. A seed stands in for the app installs and
nothing else, so a site with no apps neither fills the store nor reads it.

A seed is keyed by each app's source and branch, not just its name: two
environments carrying a same-named app from different repositories or
branches never share a seed.

Emptying the store is safe. It costs the next site its install and nothing
else:

```sh
docker run --rm -v tamp-seeds:/store alpine sh -c 'rm -f /store/*'
```

## Custom domains

`*.localhost` names resolve in browsers with no setup. Any other name, such
as `abc.xyz.com`, needs a line in your operating system's hosts file. `tamp
hosts sync` writes it:

```sh
tamp site new erp15 abc.xyz.com   # created and routed; its hosts entry is pending
tamp hosts sync                   # writes the entry; open http://abc.xyz.com
```

tamp keeps every custom domain, from every environment, between two markers:

```
# --- tamp managed block ---
# Generated by tamp. 'tamp hosts sync' rewrites everything between these
# two markers; keep your own entries outside them.
127.0.0.1  abc.xyz.com
# --- end tamp block ---
```

tamp never writes outside the markers. Remove a site and its line disappears
on the next sync. `tamp site list` shows each site's hosts-entry state, and
`tamp doctor` reports whether the block is in sync.

The hosts file belongs to the operating system, so the write asks for elevated
rights: a UAC prompt on Windows, `sudo` elsewhere. No other tamp command
elevates. The sync reads hostnames tamp already recorded, so it works with
every environment stopped.

## Work on the code

Each environment's apps live in `<envdir>/apps/`. Edit them there; the
development server hot-reloads.

- On Linux, the folder is bind-mounted into the container.
- On Windows and macOS, the code lives in a container volume and
  [Mutagen](https://mutagen.io) mirrors it to `apps/` both ways, host side
  winning conflicts. tamp downloads and manages the Mutagen binary itself.
  Override with `--sync bind|mutagen|off`.

Run git on the host, in `<envdir>/apps/<app>` — never inside the container.
Two-way sync of a live `.git` is only safe with one writer. For the same
reason tamp refuses `bench update`; pull on the host, then:

```sh
tamp exec erp15 -- bash -c "bench setup requirements && bench build && bench migrate"
```

`tamp sync` reports on and repairs the session:

```sh
tamp sync status erp15   # endpoints, ignore list, and Mutagen's own account
tamp sync flush erp15    # force a full pass and wait for it
tamp sync reset erp15    # rebuild the session after a big host-side change
tamp sync stop           # stop the Mutagen daemon tamp runs
```

`reset` is the recovery when a large host-side change — a branch checkout,
say — leaves the session with more to reconcile than it settles. On Linux
every subcommand reports `mode: bind — sync not applicable` and exits 0.

Keep environments out of OneDrive, Dropbox, and Google Drive folders, and out
of paths with spaces. tamp warns when you create one there.

## Everyday commands

```sh
tamp list                    # every environment, its versions, state, and URLs
tamp open erp15              # open its first site in the browser; 'mail' opens Mailpit
tamp stop erp15              # stop containers; data always survives
tamp start erp15             # start again, regenerating files from tamp.toml
tamp logs erp15 web -f       # follow one service: web, socketio, watch,
                             # schedule, worker, mariadb, redis-*, mailpit, router
tamp db erp15                # host, port, and credentials for a database GUI
tamp exec erp15 -- bench --version   # run anything inside the bench
tamp clean erp15             # explain the storage layers; destroy nothing
tamp rebuild erp15           # reinstall dependencies and rebuild assets
tamp snapshot erp15          # back every site up, database and files
tamp hosts sync              # write tamp's block in the OS hosts file
```

`tamp.toml` is the source of truth: `start` regenerates the generated files
from it, so hand-edits to `compose.yaml` don't survive.

## Storage layers

An environment stores four things, and tamp wipes them one at a time. Run
`tamp clean` with no flag to print this table in your terminal:

| Layer    | Holds                                       | Wiped by                  | Restored by             |
| -------- | ------------------------------------------- | ------------------------- | ----------------------- |
| `source` | `apps/` — your code                         | nothing tamp does         | it is yours             |
| `deps`   | the virtualenv, `node_modules`, `__pycache__` | `tamp clean --deps`     | `tamp rebuild`          |
| `assets` | the built JS and CSS                        | `tamp clean --assets`     | `tamp rebuild`          |
| `data`   | every site's database, files and config     | `tamp clean --data --yes` | `tamp site new <host>`, or `tamp snapshot restore` |

```sh
tamp clean erp15 --deps      # wipe broken dependencies
tamp rebuild erp15           # reinstall from the shared caches, rebuild assets
tamp clean erp15 --data      # print what would die, exit 5
tamp clean erp15 --all --yes # wipe every layer but source
```

`--deps` and `--assets` need no confirmation. `--data` destroys every site's
database and files, so it exits 5 without `--yes`, printing what it would
take. Both commands need the environment running, and neither deletes code
you wrote.

## Snapshots

A snapshot backs up the data layer — every site's database and files — into
the environment's own `.tamp/snapshots/` folder. Take one before anything
risky:

```sh
tamp snapshot erp15                          # named after the moment
tamp snapshot create erp15 --name pre-v16    # or named by you
tamp snapshot list erp15                     # name, created, size, site count
tamp snapshot restore erp15                  # the newest, or --name
```

A restore recreates sites the environment no longer has, then restores and
migrates each one, so a snapshot from an older checkout still works. Nothing
changes until the pre-flight passes:

- Every app the snapshot's sites had installed is on the bench. A missing app
  exits 1, names the app, and prints the `bench get-app` line that fetches it.
- No hostname in the snapshot belongs to another environment.
- Writing over site data that exists today needs `--yes`.

tamp takes a snapshot only when you ask, and never schedules, prunes, or
expires one. Copy, move, or delete the files as you like.

## Machine settings

`~/.tamp/config.toml` is optional, and tamp never writes it. It holds one
setting today:

```toml
[cache]
# How long a cached bench template is trusted. 0 turns the cache off.
template_ttl_days = 14
```

## Remove an environment

```sh
tamp rm erp15 --yes
```

This removes the containers, network, routes, and registry entry. It keeps
the data volumes and never touches the directory. To bring the environment
back later, run `tamp init` inside it — the volumes reattach and every site
returns with its data.

Snapshots live in the environment directory, so they survive `tamp rm` and
return with `tamp init`.

For a complete deletion, remove the volumes too, then delete the folder:

```sh
tamp rm erp15 --volumes --yes
rm -rf erp15
```

## The router

A single Caddy container serves every environment on the machine. It binds
port 80, or 8080 when 80 is taken — tamp then prints every URL with the port
in it. Browsers resolve `*.localhost` with no configuration. `curl` may not;
point it at the router directly:

```sh
curl --resolve shop.localhost:80:127.0.0.1 http://shop.localhost
```

## Exit codes

| Code | Meaning |
| ---- | ------- |
| 0 | success |
| 1 | operation failed |
| 2 | usage error |
| 3 | environment, site, snapshot or seed not found |
| 4 | Docker unavailable |
| 5 | confirmation required (add `--yes`) |

Output respects `--quiet`, `--no-color`, and the `NO_COLOR` environment
variable.
