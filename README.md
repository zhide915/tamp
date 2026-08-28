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
| `version-15` | 3.11   | 18   | 10.11   |
| `version-16` | 3.14   | 24   | 11.8    |
| `develop`    | latest supported set |||

Always pin an app's branch (`erpnext:version-15`). A bare name fetches the
repository's default branch — usually `develop`, which doesn't run on a
release bench — and tamp tells you it did rather than guessing a branch for
you.

`tamp init` does the same for a directory you already have: run it inside an
empty folder and the environment takes the folder's name.

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
all. Names outside `*.localhost` work too, but you add the hosts-file entry
yourself — tamp prints the line to add.

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

Keep environments out of OneDrive, Dropbox, and Google Drive folders, and out
of paths with spaces. tamp warns when you create one there.

## Everyday commands

```sh
tamp list                    # every environment, its versions, state, and URLs
tamp stop erp15              # stop containers; data always survives
tamp start erp15             # start again, regenerating files from tamp.toml
tamp logs erp15 web -f       # follow one service: web, socketio, watch,
                             # schedule, worker, mariadb, redis-*, mailpit, router
tamp db erp15                # host, port, and credentials for a database GUI
tamp exec erp15 -- bench --version   # run anything inside the bench
```

`tamp.toml` is the source of truth: `start` regenerates the generated files
from it, so hand-edits to `compose.yaml` don't survive.

## Remove an environment

```sh
tamp rm erp15 --yes
```

This removes the containers, network, routes, and registry entry. It keeps
the data volumes and never touches the directory. To bring the environment
back later, run `tamp init` inside it — the volumes reattach and every site
returns with its data.

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
| 3 | environment or site not found |
| 4 | Docker unavailable |
| 5 | confirmation required (add `--yes`) |

Output respects `--quiet`, `--no-color`, and the `NO_COLOR` environment
variable.
