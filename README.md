# tamp

**tamp — environment manager for Frappe Framework.**

tamp is a single-binary CLI (Go) that creates and manages isolated Frappe bench environments in Docker containers. Each bench gets its own pinned Frappe/Python/Node/MariaDB versions, so benches on different Frappe majors (v15, v16, develop) coexist on one machine. Sites are reached by hostname (`mysite.localhost`) through a shared Caddy router — never by IP or port. Source code syncs to a native host folder, so AI coding agents like Claude Code work on it at full speed on Windows, macOS, or Linux. You install tamp and Docker — nothing else.

## Apps

Apps are fetched onto the bench when the environment is created, and installed
onto a site when the site is created. The two are separate because one bench
holds many sites, and they need not run the same apps.

```sh
tamp create erp15 --frappe version-15 --apps erpnext:version-15
tamp site new erp15 shop.localhost --apps erpnext
```

Always pin the branch. An app written without one — `--apps erpnext` — is
fetched at its repository's default branch, which for most Frappe apps is
`develop`, and `develop` does not run on a version-15 bench. tamp fetches what
you asked for and says what it did rather than guessing a branch for you: not
every app has a `version-15` branch to guess at.

An app a site asks for has to be on the bench already. tamp will not fetch it
for you, for the same reason — it would have to invent the branch.

```sh
tamp exec erp15 -- bench get-app hrms --branch version-15
```

## Removing an environment, and bringing it back

`tamp rm` removes the containers, the network, the routes and the registry
entry. It keeps the volumes, and it never deletes the directory — tamp does
not destroy source code.

That is what makes it reversible. Run `tamp init` in what is left and tamp
re-adopts it: same name, same path, so the same volumes attach and every site
comes back with its data.

```sh
tamp rm erp15 --yes          # containers gone, data and source kept
cd erp15 && tamp init        # the environment is back
```

`tamp init` also converts an empty directory into a new environment — create's
sibling, named after the folder unless `--name` says otherwise. In a directory
being re-adopted, `tamp.toml` decides what the environment is and the flags do
not apply: the surviving volumes were built for the version and apps it records.

To delete an environment completely, take the volumes too and then remove the
folder yourself:

```sh
tamp rm erp15 --volumes --yes
rm -rf erp15
```

## Where the source lives

Every environment keeps its apps in `<envdir>/apps/`, and that is where you and
your agent edit them. How it gets there depends on the machine, and tamp picks
for you.

On Linux it is a plain bind mount: the container reads the host's filesystem
directly, at full speed, with file events arriving as they should.

On Windows and macOS a bind mount is neither of those things — it is slow
enough to have made `bench new-site` take hours, and inotify events never fire,
so the dev server never reloads. So the code lives in a container volume and
[Mutagen](https://mutagen.io) mirrors it two ways to `apps/` on the host, host
side winning any conflict. tamp downloads and manages that binary itself,
checksum-verified: you install tamp and Docker, nothing else. If the download
is blocked, tamp says so loudly and falls back to a bind mount — which works,
slowly, without hot reload.

The session pauses when you stop the environment, resumes when you start it,
and is torn down when you remove it. `--sync bind|mutagen|off` overrides the
choice; `tamp doctor` says which Mutagen tamp found.

**Git belongs to the host.** `.git` syncs, and after an app's first clone every
git command should be run on the host side, in `<envdir>/apps/<app>`. That is
what makes two-way syncing a live `.git` safe: only one side ever writes to it.
tamp refuses `bench update` through `tamp exec` and warns on container-side
`git` for the same reason.

Two things fight this. Keep environments out of OneDrive, Dropbox and Google
Drive — two synchronizers on one directory undo each other — and out of paths
with spaces in them. tamp warns about both when it creates one.

## Watching an environment, and its database

`tamp logs` shows one service at a time. The bench's five processes — `web`,
`socketio`, `watch`, `schedule`, `worker` — share a container and a log, and
tamp shows the lines belonging to the one you asked for. `mariadb`,
`redis-cache`, `redis-queue` and `mailpit` have a container each. `router` is
the machine's, so it answers from anywhere.

```sh
tamp logs erp15 web -f
tamp logs router --tail 50
```

`tamp db` is the one place tamp gives out a port. A database GUI client
speaks the MySQL protocol and has nowhere to put a Host header, so every
environment publishes one MariaDB port on loopback, and `tamp db` prints it
along with the credential tamp generated and the database behind each site.

```sh
tamp db erp15
```

## Reaching an environment

One Caddy container routes for the whole machine. It takes port 80 when it can,
and 8080 when something else already has it — tamp says so and prints every URL
with the port in it.

Every environment gets a mail UI at `http://mail.<env>.localhost` the moment it
exists, and every site gets `http://<site>` once it is created. `*.localhost`
resolves to loopback in every evergreen browser with no configuration at all.

Command-line tools are the exception: `curl` does not resolve `*.localhost` on
every platform, so point it at the router yourself.

```sh
curl --resolve mail.demo.localhost:80:127.0.0.1 http://mail.demo.localhost
```

With the router on the fallback port, use that port in both places:

```sh
curl --resolve mail.demo.localhost:8080:127.0.0.1 http://mail.demo.localhost:8080
```
