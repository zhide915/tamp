# tamp

tamp is an environment manager for [Frappe Framework](https://frappeframework.com). It builds a Frappe bench in Docker from one file, the recipe, and serves each site at its own hostname, such as `http://shop.localhost`. You and your coding agent edit the app code in a folder on your machine, with your own editor and your own git.

Each environment is a directory. Its recipe, `tamp.toml`, names the Frappe branch, the apps, and the sites. A team commits the recipe, and each teammate builds the same environment from it with `tamp start`.

## Before you begin

You need the following on your machine:

- Docker. On Windows and macOS, install Docker Desktop. On Linux, install Docker Engine with the Compose plugin. Start Docker before you run tamp.
- git. tamp clones each app with the git on your machine.
- Port 80, free for the router that tamp runs.

## Install tamp

Each release on [GitHub Releases](https://github.com/zhide915/tamp/releases) has one binary for each of these platforms: Windows on x64, Linux on x64, and macOS on Apple silicon.

On Windows, in PowerShell, download tamp into a folder of its own and add the folder to your `PATH`:

```powershell
$dir = "$env:LOCALAPPDATA\Programs\tamp"
New-Item -ItemType Directory -Force $dir | Out-Null
curl.exe -fLo "$dir\tamp.exe" https://github.com/zhide915/tamp/releases/latest/download/tamp_windows_amd64.exe
$path = [Environment]::GetEnvironmentVariable("Path", "User")
if (($path -split ";") -notcontains $dir) { [Environment]::SetEnvironmentVariable("Path", "$path;$dir", "User") }
```

Then open a new terminal, which reads the new `PATH`.

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

To update tamp, run the same commands again.

On any other platform, such as an Intel Mac, install tamp with Go 1.26.4 or later:

```sh
go install github.com/zhide915/tamp/cmd/tamp@latest
```

`go install` puts `tamp` in the `bin` folder of your Go path, which `go env GOPATH` prints. If that folder is not on your `PATH`, add it.

To see which tamp you run, run `tamp version`. It prints the version, the commit, and the build date.

## Check your machine

Run the following command:

```sh
tamp doctor
```

tamp prints one line for each check, and a fix for each check that fails. It changes nothing.

## Create an environment

1. Create an environment with one site:

   ```sh
   tamp new erp-dev --frappe version-15 --site shop.localhost
   ```

   tamp writes `erp-dev/tamp.toml` and `erp-dev/.gitignore`, clones Frappe into `erp-dev/apps/`, starts the containers, runs `bench init`, and creates the site. The first environment for each Frappe branch also builds the image for that branch, which takes the longest.

   `--frappe` takes `version-15`, `version-16`, or `develop`.

2. Go to the directory of the environment, and open the site in your browser:

   ```sh
   cd erp-dev
   tamp open
   ```

   Log in as `Administrator`, with the password `password`.

3. Add an app. A name without a URL means the app of that name in the `frappe` organization on GitHub, and `#` names the branch:

   ```sh
   tamp app add payments#version-15
   ```

   tamp clones the app into `apps/payments`, adds it to the recipe, and installs it on every site.

4. Run a bench command in the environment:

   ```sh
   tamp shell -- bench --site shop.localhost migrate
   ```

5. When you are done, stop the environment. Start it again when you need it:

   ```sh
   tamp stop
   tamp start
   ```

`tamp new`, `tamp start`, `tamp site add`, and `tamp app add` build the environment, and return when every site answers. If a site does not answer in 2 minutes, or the bench processes keep crashing while tamp waits, the command exits 1 and names the command that shows why. A crash loop that tamp finds after every site answers is a warning, and the command exits 0.

## Work in an environment

Edit the code in `apps/` on your machine. Run git on your machine too, in the folder of each app, because the container has none of your credentials. On Linux, the container uses `apps/` directly. On Windows and macOS, tamp syncs `apps/` with the container through [Mutagen](https://mutagen.io), which it downloads itself.

Run each command in the directory of the environment. From any other directory, name the environment with `-e`, such as `tamp status -e erp-dev`.

## Share an environment with your team

The recipe holds no name, password, port, or data, so it works on any machine. The `.gitignore` that `tamp new` writes keeps `apps/` and the generated `compose.yaml` out of the repository.

1. In the directory of the environment, commit the recipe, and push it to an empty repository of your team:

   ```sh
   git init
   git add tamp.toml .gitignore
   git commit -m "Add the erp-dev environment"
   git remote add origin <repository-url>
   git push -u origin HEAD
   ```

2. A teammate clones the repository and builds the environment:

   ```sh
   git clone <repository-url> erp-dev
   cd erp-dev
   tamp start
   ```

The name of an environment is the name of its directory, so each teammate picks it in `git clone`.

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

You can edit the recipe by hand. Then run `tamp start`, which builds what the recipe lists and the environment lacks. `tamp start` never deletes anything. Apps install in the order that the recipe lists them, so list the apps that an app depends on before it.

A private app works like any other app. Give its URL, such as `tamp app add git@github.com:<owner>/<repo>.git#<branch>`, and the git on your machine clones it with your own keys or credential manager.

## Work with an AI coding agent

`tamp context` prints what an agent needs to work in the environment: the versions, where to edit code, how to run bench commands and tests, the sites and their login, and where to look when something fails. Add this line to the instruction file of the agent, such as `AGENTS.md` or `CLAUDE.md`, in the directory of the environment:

```text
Run `tamp context` at the start of each session.
```

Without `-e`, tamp finds an environment only in its own directory. For an agent that starts in the folder of an app, such as `apps/acme`, name the environment:

```text
Run `tamp context -e erp-dev` at the start of each session.
```

## Remove an environment

To delete the containers, network, volumes, and sync session of an environment, run the following command in its directory:

```sh
tamp remove --yes
```

tamp keeps the directory, so `tamp start` can build the environment again.

To delete the environment completely, run `tamp remove --yes`, and then delete its directory, which holds the recipe and the clones in `apps/`. The `tamp-cache` volume stays, because every environment shares the pip and yarn caches in it.

tamp keeps its own files in `~/.tamp`: the Caddyfile and compose file of the router in `~/.tamp/router`, and, on Windows and macOS, Mutagen in `~/.tamp/bin` and its sync data in `~/.tamp/mutagen`.

The router keeps running after you remove the last environment. To remove the router, run the following commands:

```sh
docker rm -f tamp-router
docker network rm tamp-router_default
```

The next `tamp start` starts the router again.

## Exit codes

| Code | Meaning |
| ---- | ------- |
| 0 | The command did what you asked. |
| 1 | The command failed, including a conflict with the current state. |
| 2 | The command line is wrong. |
| 3 | The environment, site, or app does not exist. |
| 4 | tamp cannot reach Docker. |
| 5 | The command deletes data and needs `--yes`. |

`tamp shell -- <command>` exits with the exit code of the command.

## Known limits

The recipe and the apps:

- The recipe pins branches, not commits, so two teammates who build on different days can get different code.
- `tamp site add`, `tamp site remove`, `tamp app add`, and `tamp app remove` rewrite `tamp.toml`. So does each build, such as `tamp start`, when a recipe app lacks a name or a branch, to record them. Each rewrite drops the comments of the recipe.
- A key in the recipe that tamp does not know makes `start`, `open`, `context`, `doctor`, and the `site` and `app` commands exit 1, so tamp never drops the key in a rewrite. `list` and `status` warn and show no sites, and the router skips the environment with a warning.
- tamp reads the name of an app from its `pyproject.toml`, or else from a literal `name="..."` in its `setup.py`. It does not read `setup.cfg`.
- tamp does not check an app `name` that you edit by hand in the recipe against the package of the app.
- The check for missing required apps reads only a literal `required_apps = [...]` in the `hooks.py` of the app.
- tamp cannot use a team fork of Frappe.
- Your credentials stay on your machine, so a pip or yarn dependency from a private git URL fails inside the container. List such a dependency in the recipe as an app.
- The git on your machine can open a sign-in window, such as the one of Git Credential Manager, even when an agent runs `tamp start`.
- The node install writes `yarn.lock`, and the asset build writes `public/dist`, into each clone. In an app without a `.gitignore`, git shows them as untracked.
- Two environments that install apps at the same time share the yarn cache.

Finding an environment:

- Without `-e`, tamp looks for the recipe only in the working directory, so a command in a subfolder, such as `apps/acme`, does not find it. Use `-e`.
- `-e` finds an environment by its containers, so you can reach an environment that never started only from its directory.
- `tamp site add` cannot read the recipe of an environment that never started, so it cannot refuse a hostname that such a recipe lists.
- If you delete the directory of an environment without `tamp remove`, and then create an environment with the same name, tamp treats it as a move: the new environment takes the old volumes, and `tamp start` warns about the old sites. `tamp new` over the old volumes takes them in the same way.

Sites and the router:

- `tamp status`, `tamp list`, and `tamp doctor` report a bench as crash looping when its processes stopped on their own twice in the last 2 minutes. After you fix the cause, they keep reporting it until 2 minutes pass, or until `tamp start` runs.
- Outside a browser, `*.localhost` might not resolve. Point a command-line tool at the router, such as `curl --resolve shop.localhost:80:127.0.0.1 http://shop.localhost`. In PowerShell, type `curl.exe`.
- A site that fetches its own URL from inside the container can fail.
- Routes for containers that you delete by hand, not through tamp, stay in the router until the next tamp command rebuilds its config.
- The bench runs without `DEV_SERVER`, because with it the browser opens socket.io on port 9000, which the router does not publish. So the routes from the `website_route_rules` hook, and the routes of DocTypes with `has_web_view`, stay cached until you run `bench clear-website-cache`. Deprecation warnings do not print, and the default log level stays at ERROR. Edits to templates, pages, and Python still reload.
- `tamp context` runs tests on the site `<env>-test.localhost`. Two environments whose names differ only in `_` and `-`, or that share their first 58 characters, share the hostname of that site.

Sync on Windows and macOS:

- The first build on a machine, by `tamp new` or `tamp start`, downloads Mutagen, about 100 MB, into `~/.tamp/bin`.
- `apps/` syncs both ways, so a deletion inside the container, such as `rm -rf apps` through `tamp shell`, reaches your machine before Mutagen halts. Push your work, so that the remote can restore what is lost.
- CI never runs Mutagen against real Docker: its tests use a fake of Mutagen. Only the manual acceptance scripts run Mutagen against real Docker.

`tamp shell`:

- `tamp shell` refuses `bench start` and `bench update` by the first words of the command only, so `bash -c "bench update"` passes.

## Commands

This section is the `--help` of each command. Every command also takes the global flags that `tamp --help` lists: `-e/--env`, `--quiet`, and `--no-color`.

<!-- commands:begin: generated from --help by `go test ./cmd/tamp -run TestTheREADMECommandSectionIsTheHelpOfEveryCommand -update-readme` -->

### `tamp`

```text
tamp builds each Frappe bench in Docker from its recipe, tamp.toml, and
serves each site at its own hostname.

Usage:
  tamp [flags]
  tamp [command]

Available Commands:
  app         Add, list, and remove the environment's apps
  context     Print what an AI coding agent needs to work in the environment, as Markdown
  doctor      Check Docker, git, Mutagen, port 80, the router, and the environment, and change nothing
  help        Show the help of a command
  list        List each environment that has containers, with its sites
  logs        Show the logs of a service: frappe, mariadb, redis-cache, redis-queue, or router
  new         Create an environment in a new or empty directory, and build it
  open        Open a site in the default browser: the host you name, or the only site
  remove      Delete the environment's containers, network, volumes, and sync session, and keep its directory
  shell       Open a shell in the bench directory as the bench user, or run one command there
  site        Add, list, and remove the environment's sites
  start       Build what the recipe lists and the environment lacks, and start it
  status      Show the environment's containers, sync state, and site URLs
  stop        Stop the environment's containers
  version     Print the tamp version, commit, and build date

Flags:
  -e, --env string   name of the environment to act on
  -h, --help         help for tamp
      --no-color     print without color
      --quiet        print only results, warnings, and errors

Use "tamp [command] --help" for more information about a command.

Examples:
  tamp new erp-dev --frappe version-15 --site shop.localhost
  tamp status -e erp-dev
```

### `tamp app`

```text
Add, list, and remove the environment's apps

Usage:
  tamp app [flags]
  tamp app [command]

Available Commands:
  add         Clone an app, or adopt its folder in apps/, add it to the recipe, and install it on every site
  list        List Frappe and the environment's apps, with their branches and URLs
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
Clone an app, or adopt its folder in apps/, add it to the recipe, and install it on every site.
It rewrites tamp.toml, which drops the recipe's comments.

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
List Frappe and the environment's apps, with their branches and URLs

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
Uninstall an app from every site and the bench, remove it from the recipe, and keep its clone.
It rewrites tamp.toml, which drops the recipe's comments.

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
context prints the versions, where to edit code, how to run bench commands and tests,
the sites and their login, and where to look when something fails. It reads the recipe
and the containers on each run, works on a stopped environment, and changes nothing.

To use it, add this line to the agent's instruction file, such as AGENTS.md or
CLAUDE.md, in the environment's directory:

  Run `tamp context` at the start of each session.

Without -e, tamp finds the environment only in its own directory. For an agent that
starts anywhere else, such as an app's folder in apps/, name the environment:

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
Check Docker, git, Mutagen, port 80, the router, and the environment, and change nothing

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
Show the help of a command

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
List each environment that has containers, with its sites

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
Show the logs of a service: frappe, mariadb, redis-cache, redis-queue, or router.
Without a service, logs shows frappe's logs.

Usage:
  tamp logs [service] [-f] [--tail n] [flags]

Flags:
  -f, --follow   keep printing new lines until you press Ctrl+C
  -h, --help     help for logs
      --tail n   show only the last n lines

Examples:
  tamp logs -f
  tamp logs mariadb --tail 50
```

### `tamp new`

```text
Create an environment in a new or empty directory, and build it

Usage:
  tamp new <path> --frappe <branch> [--app <url|name>[#branch]]... [--site <host>]... [flags]

Flags:
      --app stringArray    app to clone and install: a URL, or an app's name in the frappe GitHub organization, with an optional #branch. Repeat the flag for more apps
      --frappe string      Frappe branch, one of: version-15, version-16, develop
  -h, --help               help for new
      --site stringArray   *.localhost site to create with every app. Repeat the flag for more sites

Examples:
  tamp new erp-dev --frappe version-15 --site shop.localhost
  tamp new . --frappe version-15 --app payments#version-15 --site pay.localhost
```

### `tamp open`

```text
Open a site in the default browser: the host you name, or the only site

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
Delete the environment's containers, network, volumes, and sync session, and keep its directory

Usage:
  tamp remove --yes [flags]

Flags:
  -h, --help   help for remove
      --yes    confirm deleting the containers, network, and volumes, with every site's database

Examples:
  tamp remove --yes
  tamp remove -e erp-dev --yes
```

### `tamp shell`

```text
Without a command, shell opens bash in the frappe container. It needs a terminal.
With a command after `--`, shell runs the command and exits with its exit code.
shell refuses `bench start` and `bench update`, and warns about `bench new-site` and git.

Usage:
  tamp shell [--force] [-- <command>...] [flags]

Flags:
      --force   skip the checks for bench start, bench update, bench new-site, and git
  -h, --help    help for shell

Examples:
  tamp shell -- bench --site shop.localhost migrate
  tamp shell -e erp-dev -- bench build --app frappe
```

### `tamp site`

```text
Add, list, and remove the environment's sites

Usage:
  tamp site [flags]
  tamp site [command]

Available Commands:
  add         Add a *.localhost site to the recipe, create it with every app, and route it
  list        List the environment's sites and their URLs
  remove      Drop a site and its database, remove it from the recipe, and stop routing it

Flags:
  -h, --help   help for site

Use "tamp site [command] --help" for more information about a command.

Examples:
  tamp site add blog.localhost
  tamp site list -e erp-dev
```

### `tamp site add`

```text
Add a *.localhost site to the recipe, create it with every app, and route it.
It rewrites tamp.toml, which drops the recipe's comments.

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
List the environment's sites and their URLs

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
Drop a site and its database, remove it from the recipe, and stop routing it.
It rewrites tamp.toml, which drops the recipe's comments.

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
Build what the recipe lists and the environment lacks, and start it.
It rewrites tamp.toml when a recipe app lacks a name or a branch, to record them, which drops the recipe's comments.

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
Show the environment's containers, sync state, and site URLs

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
