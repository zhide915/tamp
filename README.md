# tamp

tamp is an environment manager for [Frappe Framework](https://frappeframework.com). It builds a Frappe bench in Docker from one file, its recipe, and serves every site by hostname, such as `http://shop.localhost`. You, your editor, and your coding agent edit the app code in a folder on your machine.

An environment is one directory. Its recipe, `tamp.toml`, lists the Frappe branch, the apps, and the sites. A team commits the recipe, and each teammate builds the same environment from it with `tamp start`.

## Install

You need:

- Docker: Docker Desktop on Windows and macOS, or Docker Engine with the Compose plugin on Linux. Start it before you run tamp.
- git on the host. tamp clones every app with it.
- Port 80 free for tamp's router.

Each release on [GitHub Releases](https://github.com/zhide915/tamp/releases) has one binary for each platform: Windows on x64, Linux on x64, and macOS on Apple silicon.

On Windows, in PowerShell, download tamp into a folder of its own, and add that folder to your `PATH`:

```powershell
$dir = "$env:LOCALAPPDATA\Programs\tamp"
New-Item -ItemType Directory -Force $dir | Out-Null
curl.exe -fLo "$dir\tamp.exe" https://github.com/zhide915/tamp/releases/latest/download/tamp_windows_amd64.exe
$path = [Environment]::GetEnvironmentVariable("Path", "User")
if (($path -split ";") -notcontains $dir) { [Environment]::SetEnvironmentVariable("Path", "$path;$dir", "User") }
```

To update tamp, run the same commands again.

Open a new terminal, so that it reads the new `PATH`.

On macOS:

```sh
curl -fLo tamp https://github.com/zhide915/tamp/releases/latest/download/tamp_darwin_arm64
chmod +x tamp
sudo mkdir -p /usr/local/bin
sudo mv tamp /usr/local/bin/tamp
```

On Linux:

```sh
curl -fLo tamp https://github.com/zhide915/tamp/releases/latest/download/tamp_linux_amd64
chmod +x tamp
sudo mv tamp /usr/local/bin/tamp
```

On another platform, such as an Intel Mac, install tamp with Go 1.26.4 or later:

```sh
go install github.com/zhide915/tamp/cmd/tamp@latest
```

`go install` puts `tamp` in the `bin` folder of your Go path, which `go env GOPATH` prints. Add that folder to your `PATH` if it is not there.

`tamp version` prints the version, the commit, and the build date.

Check the machine:

```sh
tamp doctor
```

Each check prints a line, and each failure names its fix.

## Create an environment

1. Create an environment with one site:

   ```sh
   tamp new erp-dev --frappe version-15 --site shop.localhost
   ```

   tamp writes `erp-dev/tamp.toml` and `erp-dev/.gitignore`, clones Frappe into `erp-dev/apps/`, starts the containers, runs `bench init`, and creates the site. The first environment of a Frappe version also builds its image, which takes the longest.

   `--frappe` takes `version-15`, `version-16` or `develop`.

2. Go to the environment's directory and open the site:

   ```sh
   cd erp-dev
   tamp open
   ```

   Log in as `Administrator` with the password `password`.

3. Add an app. A bare name means the app of that name in the `frappe` GitHub organization, and `#` picks the branch:

   ```sh
   tamp app add payments#version-15
   ```

   tamp clones the app into `apps/payments`, records it in the recipe, and installs it on every site.

4. Run a bench command inside the environment:

   ```sh
   tamp shell -- bench --site shop.localhost migrate
   ```

5. Stop the environment when you are done, and start it again later:

   ```sh
   tamp stop
   tamp start
   ```

Edit the code in `apps/` on the host. Run git on the host too, in each app's folder: the container has none of your credentials. On Linux, the container uses `apps/` directly. On Windows and macOS, tamp syncs `apps/` with the container through [Mutagen](https://mutagen.io), which it downloads itself.

Run a command from the environment's directory, or name the environment with `-e` from anywhere else, such as `tamp status -e erp-dev`.

To delete the environment's containers, network, volumes and sync session, run the following command. It keeps the directory, so `tamp start` builds the environment again.

```sh
tamp remove --yes
```

To delete the environment fully, run `tamp remove --yes`, then delete its directory, which holds the recipe and the app clones in `apps/`. The `tamp-cache` volume stays, because every environment shares its pip and yarn caches.

tamp keeps its own state in `~/.tamp`: the router's Caddyfile and compose file in `~/.tamp/router`, and on Windows and macOS, Mutagen in `~/.tamp/bin` and its sync data in `~/.tamp/mutagen`. The router keeps running after you remove the last environment. To remove it, run:

```sh
docker rm -f tamp-router
docker network rm tamp-router_default
```

The next `tamp start` brings the router back.

## Share an environment with your team

The recipe holds no name, password, port or data, so it works on any machine. The `.gitignore` that `tamp new` writes keeps `apps/` and the generated `compose.yaml` out of the repository.

1. In the environment's directory, commit the recipe, and push it to an empty repository of your team:

   ```sh
   git init
   git add tamp.toml .gitignore
   git commit -m "Add the erp-dev environment"
   git remote add origin <repository-url>
   git push -u origin HEAD
   ```

2. A teammate clones the repository and builds it:

   ```sh
   git clone <repository-url> erp-dev
   cd erp-dev
   tamp start
   ```

The environment's name is its directory's name, so the teammate picks it in `git clone`.

A recipe looks like this:

```toml
frappe = "version-15"

[[apps]]
  name = "payments"
  url = "https://github.com/frappe/payments"
  branch = "version-15"

[[sites]]
  host = "shop.localhost"
```

After you edit the recipe by hand, run `tamp start`. It builds what the recipe lists and is missing, and never deletes anything. Apps install in the order the recipe lists them, so list an app's dependencies first.

A private app works like any other: give its URL, such as `tamp app add git@github.com:<owner>/<repo>.git#<branch>`. Host git clones it with your own keys or credential manager.

## Work with an AI coding agent

`tamp context` prints what an agent needs to work in the environment: the versions, where to edit code, how to run bench commands and tests, the sites with their login, and where to look when something fails. Add this line to the agent's instruction file, such as `AGENTS.md` or `CLAUDE.md`, in the environment's directory:

```text
Run `tamp context` at the start of each session.
```

An agent that starts in an app's folder, such as `apps/acme`, needs the environment's name, because tamp looks for the recipe only in the working directory:

```text
Run `tamp context -e erp-dev` at the start of each session.
```

## Exit codes

| Code | Meaning |
| ---- | ------- |
| 0 | The command did what was asked. |
| 1 | The command failed, including a conflict with the current state. |
| 2 | The command line is wrong. |
| 3 | The environment, site, or app does not exist. |
| 4 | tamp cannot reach Docker. |
| 5 | The command removes data and needs `--yes`. |

`tamp shell -- <command>` exits with the command's own code.

## Known limits

The recipe and apps:

- The recipe pins branches, not commits, so two teammates who build on different days can get different code.
- tamp rewrites `tamp.toml` when a command changes it, and drops its comments.
- A recipe key that tamp does not know makes `start`, `open`, `context`, `doctor`, and the `site` and `app` commands exit 1, so that tamp never drops the key when it rewrites the recipe. `list` and `status` warn and show no sites, and the router skips the environment with a warning.
- tamp reads an app's name from `pyproject.toml`, or else from a literal `name="..."` in `setup.py`. It does not read `setup.cfg`.
- tamp does not check an app `name` you edit by hand in the recipe against the app's package.
- The check for missing required apps reads only a literal `required_apps = [...]` in the app's `hooks.py`.
- A team fork of Frappe cannot be used.
- Credentials stay on the host, so a pip or yarn dependency from a private git URL fails inside the container. List such a dependency in the recipe as an app.
- Host git may open a sign-in window, such as Git Credential Manager's, even when an agent runs `tamp start`.
- The node install writes `yarn.lock`, and the build writes `public/dist`, into each clone. An app without a `.gitignore` shows them as untracked.
- Two environments that install apps at the same time share the yarn cache.

Finding an environment:

- tamp looks for the recipe only in the working directory, so a command run from a subfolder, such as `apps/acme`, does not find it. Use `-e`.
- `-e` finds an environment by its containers, so an environment that never started is reachable only from its directory.
- `tamp site add` cannot see the recipe of an environment that never started, so it cannot refuse a hostname that such a recipe lists.
- If you delete an environment's directory without `tamp remove`, and create an environment of the same name, tamp treats it as a move: the new environment takes the old volumes, and `tamp start` warns about the old sites. `tamp new` over the leftover volumes adopts them the same way.

Sites and the router:

- `tamp start` does not wait for the web server. On a test machine, a site answered about 2 seconds after `tamp start` returned on a stopped environment, and at once after `tamp new`.
- Outside a browser, `*.localhost` may not resolve. Point a command-line tool at the router, such as `curl --resolve shop.localhost:80:127.0.0.1 http://shop.localhost`. In PowerShell, type `curl.exe`.
- A site that fetches its own URL from inside the container may fail.
- The router keeps running after you remove the last environment.
- Routes for containers that you delete by hand, not through tamp, stay in the router until the next tamp command rebuilds its config.
- The bench runs without `DEV_SERVER`, because with it the browser opens socket.io on port 9000, which the router does not publish. So routes from the `website_route_rules` hook, and those of DocTypes with `has_web_view`, stay cached until `bench clear-website-cache`. Deprecation warnings are not printed, and the default log level stays at ERROR. Template, page and Python edits still reload.
- `tamp context` runs tests on the site `<env>-test.localhost`. Environment names that differ only in `_` and `-`, or that share their first 58 characters, share that site's hostname.

Sync on Windows and macOS:

- The first build on the machine, by `tamp new` or `tamp start`, downloads Mutagen, about 100 MB, into `~/.tamp/bin`.
- `apps/` syncs both ways, so a deletion inside the container, such as `rm -rf apps` through `tamp shell`, reaches the host before Mutagen halts. Push your work, so that the remote can restore what is lost.
- CI never runs Mutagen against real Docker, only its fake.

`tamp shell`:

- `shell` refuses `bench start` and `bench update` by the first words of the command only, so `bash -c "bench update"` passes.

## Commands

This section is each command's `--help`. Every command also takes the global flags that `tamp --help` lists: `-e/--env`, `--quiet` and `--no-color`.

<!-- commands:begin: generated from --help by `go test ./cmd/tamp -run TestTheREADMECommandSectionIsTheHelpOfEveryCommand -update-readme` -->

### `tamp`

```text
tamp builds each Frappe bench in Docker from its recipe, tamp.toml,
and serves every site by hostname.

Usage:
  tamp [flags]
  tamp [command]

Available Commands:
  app         Add, list and remove the environment's apps
  context     Print, as markdown, what an AI coding agent needs to work in the environment
  doctor      Check Docker, git, Mutagen, port 80, the router and the environment, and repair nothing
  help        Help about any command
  list        List every environment that has containers, with its sites
  logs        Show a service's logs: frappe, mariadb, redis-cache, redis-queue or router
  new         Create an environment at a new or empty directory, and build it
  open        Open a site in the default browser: the host given, or the only site
  remove      Delete the environment's sync session, containers, network and volumes, and keep its directory
  shell       Open a shell as the bench user in the bench directory, or run one command there
  site        Add, list and remove the environment's sites
  start       Build whatever the recipe lists that is missing, and start the environment
  status      Show the environment's containers, sync state and site URLs
  stop        Stop the environment's containers
  version     Print the tamp version, commit, and build date

Flags:
  -e, --env string   the environment to act on, by name
  -h, --help         help for tamp
      --no-color     print without color
      --quiet        print only results, warnings and errors

Use "tamp [command] --help" for more information about a command.

Examples:
  tamp new erp-dev --frappe version-15 --site shop.localhost
  tamp status -e erp-dev
```

### `tamp app`

```text
Add, list and remove the environment's apps

Usage:
  tamp app [flags]
  tamp app [command]

Available Commands:
  add         Clone an app, or adopt the one in apps/, record it in the recipe, and install it on every site
  list        List Frappe and the environment's apps with their branches
  remove      Uninstall an app from every site and the bench, remove it from the recipe, and keep its clone

Flags:
  -h, --help   help for app

Use "tamp app [command] --help" for more information about a command.

Examples:
  tamp app add payments#version-15
  tamp app list -e erp-dev
```

### `tamp app add`

```text
Clone an app, or adopt the one in apps/, record it in the recipe, and install it on every site

Usage:
  tamp app add <url|name>[#branch] [flags]

Flags:
  -h, --help   help for add

Examples:
  tamp app add payments#version-15
  tamp app add -e erp-dev https://github.com/frappe/payments#version-15
```

### `tamp app list`

```text
List Frappe and the environment's apps with their branches

Usage:
  tamp app list [flags]

Flags:
  -h, --help   help for list

Examples:
  tamp app list
  tamp app list -e erp-dev
```

### `tamp app remove`

```text
Uninstall an app from every site and the bench, remove it from the recipe, and keep its clone

Usage:
  tamp app remove <name> --yes [flags]

Flags:
  -h, --help   help for remove
      --yes    confirm deleting the app's data from every site

Examples:
  tamp app remove payments --yes
  tamp app remove -e erp-dev payments --yes
```

### `tamp context`

```text
context prints the versions, where to edit code, how to run bench commands, the sites
with their login, and where to look when something fails. It reads the recipe and the
containers each time, works on a stopped environment, and changes nothing.

Add one line to the agent's instruction file, such as AGENTS.md or CLAUDE.md:

  Run `tamp context` at the start of each session.

That line works in the environment's directory. In any other directory, a subfolder
such as apps/<app> included, name the environment. For an agent that starts in an
app's folder, write:

  Run `tamp context -e <name>` at the start of each session.

Usage:
  tamp context [flags]

Flags:
  -h, --help   help for context

Examples:
  tamp context
  tamp context -e erp-dev
```

### `tamp doctor`

```text
Check Docker, git, Mutagen, port 80, the router and the environment, and repair nothing

Usage:
  tamp doctor [flags]

Flags:
  -h, --help   help for doctor

Examples:
  tamp doctor
  tamp doctor -e erp-dev
```

### `tamp help`

```text
Help about any command

Usage:
  tamp help [command] [flags]

Flags:
  -h, --help   help for help

Examples:
  tamp help new
  tamp help site add
```

### `tamp list`

```text
List every environment that has containers, with its sites

Usage:
  tamp list [flags]

Flags:
  -h, --help   help for list

Examples:
  tamp list
  tamp list --no-color
```

### `tamp logs`

```text
Show a service's logs: frappe, mariadb, redis-cache, redis-queue or router

Usage:
  tamp logs [service] [-f] [--tail n] [flags]

Flags:
  -f, --follow   keep printing new lines until Ctrl-C
  -h, --help     help for logs
      --tail n   show only the last n lines

Examples:
  tamp logs -f
  tamp logs mariadb --tail 50
```

### `tamp new`

```text
Create an environment at a new or empty directory, and build it

Usage:
  tamp new <path> --frappe <branch> [--app <url|name>[#branch]]... [--site <host>]... [flags]

Flags:
      --app stringArray    an app to clone and install, as a URL or a frappe app name, with an optional #branch: repeat the flag for more
      --frappe string      the Frappe branch: version-15, version-16, develop
  -h, --help               help for new
      --site stringArray   a *.localhost site to create: repeat the flag for more

Examples:
  tamp new erp-dev --frappe version-15 --site shop.localhost
  tamp new . --frappe version-15 --app payments#version-15 --site pay.localhost
```

### `tamp open`

```text
Open a site in the default browser: the host given, or the only site

Usage:
  tamp open [host] [flags]

Flags:
  -h, --help   help for open

Examples:
  tamp open
  tamp open -e erp-dev shop.localhost
```

### `tamp remove`

```text
Delete the environment's sync session, containers, network and volumes, and keep its directory

Usage:
  tamp remove --yes [flags]

Flags:
  -h, --help   help for remove
      --yes    confirm deleting the containers, network and volumes, with every site's database

Examples:
  tamp remove --yes
  tamp remove -e erp-dev --yes
```

### `tamp shell`

```text
Without a command, shell opens bash in the frappe container, and needs a terminal.
With a command after `--`, it runs that command and exits with its code.
It refuses `bench start` and `bench update`, and warns on `bench new-site` and git.

Usage:
  tamp shell [--force] [-- <command>...] [flags]

Flags:
      --force   skip the checks on bench start, bench update, bench new-site and git
  -h, --help    help for shell

Examples:
  tamp shell -- bench --site shop.localhost migrate
  tamp shell -e erp-dev -- bench build --app frappe
```

### `tamp site`

```text
Add, list and remove the environment's sites

Usage:
  tamp site [flags]
  tamp site [command]

Available Commands:
  add         Add a *.localhost site to the recipe, create it with every app, and route it
  list        List the environment's sites with their URLs
  remove      Drop a site and its database, remove it from the recipe, and take it off the router

Flags:
  -h, --help   help for site

Use "tamp site [command] --help" for more information about a command.

Examples:
  tamp site add blog.localhost
  tamp site list -e erp-dev
```

### `tamp site add`

```text
Add a *.localhost site to the recipe, create it with every app, and route it

Usage:
  tamp site add <host> [flags]

Flags:
  -h, --help   help for add

Examples:
  tamp site add blog.localhost
  tamp site add -e erp-dev docs.localhost
```

### `tamp site list`

```text
List the environment's sites with their URLs

Usage:
  tamp site list [flags]

Flags:
  -h, --help   help for list

Examples:
  tamp site list
  tamp site list -e erp-dev
```

### `tamp site remove`

```text
Drop a site and its database, remove it from the recipe, and take it off the router

Usage:
  tamp site remove <host> --yes [flags]

Flags:
  -h, --help   help for remove
      --yes    confirm dropping the site's database

Examples:
  tamp site remove blog.localhost --yes
  tamp site remove -e erp-dev docs.localhost --yes
```

### `tamp start`

```text
Build whatever the recipe lists that is missing, and start the environment

Usage:
  tamp start [flags]

Flags:
  -h, --help   help for start

Examples:
  tamp start
  tamp start -e erp-dev
```

### `tamp status`

```text
Show the environment's containers, sync state and site URLs

Usage:
  tamp status [flags]

Flags:
  -h, --help   help for status

Examples:
  tamp status
  tamp status -e erp-dev
```

### `tamp stop`

```text
Stop the environment's containers

Usage:
  tamp stop [flags]

Flags:
  -h, --help   help for stop

Examples:
  tamp stop
  tamp stop -e erp-dev
```

### `tamp version`

```text
Print the tamp version, commit, and build date

Usage:
  tamp version [flags]

Flags:
  -h, --help   help for version

Examples:
  tamp version
  tamp version --no-color
```

<!-- commands:end -->
