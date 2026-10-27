#!/usr/bin/env bash
# Runs the built binary end to end against real Docker, on ubuntu in the E2E
# workflow.
set -euo pipefail

TAMP=${1:?usage: e2e.sh /path/to/tamp}
# Absolute, because the script changes directory before it runs tamp.
TAMP=$(cd "$(dirname "$TAMP")" && pwd)/$(basename "$TAMP")
SCRIPTS=$(cd "$(dirname "$0")" && pwd)
WORK=$(mktemp -d)
ENV_DIR=$WORK/erp-dev
FRAPPE=erp-dev-frappe-1
CADDYFILE=$HOME/.tamp/router/Caddyfile

TIMINGS=

cleanup() {
  if [ -n "$TIMINGS" ]; then
    printf '\n=== timings\n%s' "$TIMINGS"
    if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then printf '### Timings\n\n%s' "$TIMINGS" >>"$GITHUB_STEP_SUMMARY"; fi
  fi
  "$TAMP" remove -e erp-dev --yes --quiet || true
  "$TAMP" remove -e erp-two --yes --quiet || true
  # tamp never removes the router.
  docker rm --force tamp-router >/dev/null 2>&1 || true
  docker rm --force tamp-e2e-port80 >/dev/null 2>&1 || true
  docker network rm tamp-router_default >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

say() { printf '\n=== %s\n' "$*"; }
fail() {
  echo "e2e: $*" >&2
  exit 1
}

# target records a timing in seconds, and fails when it misses its target.
target() {
  local line="- $1: $3 s, target $2 s or less"
  TIMINGS+="$line"$'\n'
  echo "timing $line"
  [ "$3" -le "$2" ] || fail "$1 took $3 s, over its target of $2 s"
}

bench_images() { docker images --quiet --filter 'reference=tamp-bench:*' | sort; }

# through_router fetches a URL from the router on port 80, whatever
# *.localhost resolves to on this machine. curl sends no Origin header.
through_router() {
  curl -sf --max-time 10 --resolve shop.localhost:80:127.0.0.1 "$@"
}

# answers fetches a site's ping through the router once, with no retry:
# tamp start and tamp new return only when each site answers.
answers() {
  local body
  body=$(curl -sf --max-time 10 --resolve "$1:80:127.0.0.1" "http://$1/api/method/ping") && grep -q pong <<<"$body"
}

# pongs polls a site's ping through the router for up to 60 s and prints the
# reply, for a site whose processes are coming back, as after honcho exits.
pongs() {
  local host=$1 body deadline=$((SECONDS + 60))
  while [ "$SECONDS" -lt "$deadline" ]; do
    if body=$(curl -sf --max-time 10 --resolve "$host:80:127.0.0.1" "http://$host/api/method/ping") &&
      grep -q pong <<<"$body"; then
      echo "$body"
      return 0
    fi
    sleep 2
  done
  return 1
}

# socketio_handshake logs in, opens an engine.io session by polling, and
# connects to the site's namespace, which the realtime server refuses
# without an Origin matching the host.
socketio_handshake() {
  local jar=$WORK/cookies base=http://shop.localhost/socket.io/?EIO=4 open= sid= reply=
  through_router -c "$jar" --data usr=Administrator --data pwd=password http://shop.localhost/api/method/login >/dev/null || return 1
  for _ in $(seq 1 15); do
    if open=$(through_router -b "$jar" "$base&transport=polling"); then break; fi
    sleep 2
  done
  echo "open: $open"
  sid=$(sed -n 's/^0{"sid":"\([^"]*\)".*/\1/p' <<<"$open")
  [ -n "$sid" ] || return 1
  through_router -b "$jar" -H 'Content-Type: text/plain;charset=UTF-8' --data '40/shop.localhost,' "$base&transport=polling&sid=$sid" >/dev/null || return 1
  reply=$(through_router -b "$jar" "$base&transport=polling&sid=$sid") || return 1
  echo "connect: $reply"
  grep -q '40/shop.localhost,{"sid"' <<<"$reply"
}

say "tamp version prints the version, commit, and build date"
out=$("$TAMP" version)
echo "$out"
grep -q '^tamp ' <<<"$out" || fail "no version line"
grep -q '^commit: ' <<<"$out" || fail "no commit line"
grep -q '^build date: ' <<<"$out" || fail "no build date line"

cd "$WORK"

say "tamp new without --frappe exits 2"
code=0
"$TAMP" new shop || code=$?
[ "$code" = 2 ] || fail "exit code $code, want 2"
[ ! -e shop ] || fail "shop was written"

say "tamp new with a site that is not *.localhost exits 2 and writes nothing"
code=0
"$TAMP" new shop --frappe version-15 --site shop.example.com || code=$?
[ "$code" = 2 ] || fail "exit code $code, want 2"
[ ! -e shop ] || fail "shop was written"

say "tamp new builds the first environment on the machine in 15 minutes or less"
[ -z "$(bench_images)" ] || fail "a tamp-bench image exists, so the first environment cannot be measured"
start=$SECONDS
"$TAMP" new erp-dev --frappe version-15 --site shop.localhost
target "first environment on the machine (image built)" 900 $((SECONDS - start))
[ -n "$(bench_images)" ] || fail "tamp new built no image"
grep -qx 'frappe = "version-15"' erp-dev/tamp.toml || fail "unexpected recipe: $(cat erp-dev/tamp.toml)"
grep -qx '  host = "shop.localhost"' erp-dev/tamp.toml || fail "the recipe lacks the site: $(cat erp-dev/tamp.toml)"
grep -qx 'apps/' erp-dev/.gitignore || fail ".gitignore lacks apps/"
grep -qx 'compose.yaml' erp-dev/.gitignore || fail ".gitignore lacks compose.yaml"
[ "$(git -C erp-dev/apps/frappe remote)" = upstream ] || fail "the Frappe clone's remote is not upstream"
[ "$(git -C erp-dev/apps/frappe rev-parse --is-shallow-repository)" = true ] || fail "the Frappe clone is not shallow"

say "a new environment with the image cached takes 8 minutes or less"
images=$(bench_images)
start=$SECONDS
"$TAMP" new erp-two --frappe version-15 --site two.localhost
target "new environment, image cached" 480 $((SECONDS - start))
[ "$(bench_images)" = "$images" ] || fail "the second tamp new built an image"
answers two.localhost || fail "two.localhost did not answer through the router as soon as tamp new returned"
"$TAMP" remove -e erp-two --yes
rm -rf erp-two

say "the bench volume carries the Frappe branch, and the containers both labels"
[ "$(docker volume inspect --format '{{ index .Labels "tamp.frappe" }}' erp-dev_bench)" = version-15 ] ||
  fail "the bench volume lacks tamp.frappe=version-15"
for service in frappe mariadb redis-cache redis-queue; do
  labels=$(docker inspect --format '{{ index .Config.Labels "tamp.env" }} {{ index .Config.Labels "tamp.path" }}' "erp-dev-$service-1")
  [ "$labels" = "erp-dev $ENV_DIR" ] || fail "erp-dev-$service-1 has labels '$labels'"
done

say "volumes and networks follow compose's naming"
docker volume inspect erp-dev_db erp-dev_bench tamp-cache >/dev/null || fail "a volume is missing: $(docker volume ls --format '{{.Name}}')"
docker network inspect erp-dev_default tamp-router_default >/dev/null || fail "a network is missing: $(docker network ls --format '{{.Name}}')"

say "on Linux, apps/ is bind-mounted, and tamp neither downloads Mutagen nor syncs"
docker inspect --format '{{ range .Mounts }}{{ .Type }}:{{ .Destination }} {{ end }}' "$FRAPPE" |
  grep -qw 'bind:/home/frappe/frappe-bench/apps' || fail "apps/ is not bind-mounted"
# Captured first: grep -q would close the pipe on tamp, which pipefail reports.
out=$("$TAMP" status -e erp-dev)
grep -qx 'sync: apps/ is bind-mounted' <<<"$out" || fail "tamp status does not show the bind mount: $out"
[ ! -e "$HOME/.tamp/bin" ] || fail "tamp downloaded Mutagen on Linux"

say "files the container writes into apps/ belong to the host user"
[ "$(stat -c %u erp-dev/apps/frappe/frappe/public/dist)" = "$(id -u)" ] ||
  fail "apps/ holds files that are not the host user's"

say "the site answers inside the frappe container for its Host header"
answered=
for _ in $(seq 1 90); do
  if body=$(docker exec "$FRAPPE" curl -sf --max-time 5 -H 'Host: shop.localhost' http://127.0.0.1:8000/api/method/ping); then
    answered=$body
    break
  fi
  sleep 2
done
echo "$answered"
grep -q pong <<<"$answered" || fail "shop.localhost did not answer"

say "the bench settings point socket.io at loopback, and no process has DEV_SERVER"
docker exec "$FRAPPE" grep -q '"webserver_host": "127.0.0.1"' sites/common_site_config.json ||
  fail "webserver_host is not 127.0.0.1"
docker exec "$FRAPPE" grep -q '"live_reload": true' sites/common_site_config.json ||
  fail "bench init's live_reload is gone"
if docker exec "$FRAPPE" sh -c 'cat /proc/[0-9]*/environ 2>/dev/null | tr "\0" "\n" | grep -q "^DEV_SERVER="'; then
  fail "a process runs with DEV_SERVER set"
fi

say "the site is a developer site"
docker exec "$FRAPPE" grep -q '"developer_mode": 1' sites/shop.localhost/site_config.json ||
  fail "shop.localhost lacks developer_mode 1"

say "the router runs Caddy on port 80, restarting unless stopped, on the environment's network"
[ "$(docker inspect --format '{{ .HostConfig.RestartPolicy.Name }}' tamp-router)" = unless-stopped ] ||
  fail "the router's restart policy is not unless-stopped"
docker inspect --format '{{ range $net, $_ := .NetworkSettings.Networks }}{{ $net }} {{ end }}' tamp-router | grep -qw erp-dev_default ||
  fail "the router is not on erp-dev_default"
docker inspect --format '{{ range .Mounts }}{{ .Type }}:{{ .Destination }} {{ end }}' tamp-router | grep -qw 'bind:/etc/caddy' ||
  fail "the router does not mount its directory at /etc/caddy"

say "curl --resolve reaches shop.localhost through the router"
pongs shop.localhost || fail "shop.localhost did not answer through the router"

say "a socket.io handshake through the router succeeds with no Origin from the client"
socketio_handshake || fail "the socket.io handshake through the router failed"

say "the site fetches its own URL from inside the frappe container"
own=$(docker exec "$FRAPPE" env/bin/python -c \
  'import urllib.request; print(urllib.request.urlopen("http://shop.localhost/api/method/ping", timeout=10).read().decode())') ||
  fail "the site cannot fetch http://shop.localhost from inside its container"
echo "$own"
grep -q pong <<<"$own" || fail "http://shop.localhost from inside the container did not answer pong"

router_started=$(docker inspect --format '{{ .State.StartedAt }}' tamp-router)

say "a second tamp start repeats no completed work"
cd erp-dev
out=$("$TAMP" start 2>&1)
echo "$out"
if grep -q 'building the image\|Cloning\|running bench init\|creating the site' <<<"$out"; then
  fail "tamp start repeated work"
fi

say "tamp start on a stopped environment takes 1 minute or less"
"$TAMP" stop
start=$SECONDS
"$TAMP" start
target "start on an existing environment" 60 $((SECONDS - start))
answers shop.localhost || fail "shop.localhost did not answer as soon as tamp start returned on the stopped environment"

say "after a second start, the reloaded router still serves the site and socket.io"
pongs shop.localhost || fail "shop.localhost stopped answering after the reload"
socketio_handshake || fail "the socket.io handshake failed after the reload"

say "a site added to the recipe is routed by the running router after tamp start"
printf '\n[[sites]]\n  host = "erp.localhost"\n' >>tamp.toml
"$TAMP" start
[ "$(docker inspect --format '{{ .State.StartedAt }}' tamp-router)" = "$router_started" ] ||
  fail "tamp start restarted the router instead of reloading it"
answers erp.localhost || fail "erp.localhost does not answer through the router"

say "tamp site add writes the recipe, creates the site, and routes it"
"$TAMP" site add blog.localhost
grep -qx '  host = "blog.localhost"' tamp.toml || fail "the recipe lacks blog.localhost: $(cat tamp.toml)"
grep -q '^http://blog.localhost {' "$CADDYFILE" || fail "the Caddyfile does not route blog.localhost"
answers blog.localhost || fail "blog.localhost does not answer through the router"

say "tamp site add of a host that is not *.localhost exits 2"
code=0
"$TAMP" site add blog.example.com || code=$?
[ "$code" = 2 ] || fail "exit code $code, want 2"

say "tamp site list prints each site with its URL"
out=$("$TAMP" site list)
echo "$out"
grep -Eq '^blog\.localhost +http://blog\.localhost$' <<<"$out" || fail "tamp site list does not show blog.localhost"

say "tamp site remove without --yes exits 5 and keeps the site"
code=0
"$TAMP" site remove blog.localhost || code=$?
[ "$code" = 5 ] || fail "exit code $code, want 5"
docker exec "$FRAPPE" test -f sites/blog.localhost/site_config.json || fail "the site is gone"

say "tamp site remove --yes drops the site, edits the recipe, and the route is gone"
"$TAMP" site remove blog.localhost --yes
if docker exec "$FRAPPE" test -e sites/blog.localhost; then fail "sites/blog.localhost remains"; fi
if grep -q 'blog.localhost' tamp.toml; then fail "the recipe still lists blog.localhost"; fi
if grep -q 'blog.localhost' "$CADDYFILE"; then fail "the Caddyfile still routes blog.localhost"; fi
pongs shop.localhost || fail "shop.localhost stopped answering after the remove"

say "a hostname no environment serves gets a 404 that names tamp list, and shop.localhost still answers"
code=$(curl -s --max-time 10 --resolve blog.localhost:80:127.0.0.1 -o "$WORK/gone.body" -w '%{http_code}' http://blog.localhost/) || true
echo "$code $(cat "$WORK/gone.body")"
[ "$code" = 404 ] || fail "the removed blog.localhost answered $code, want 404"
grep -qF '`tamp list`' "$WORK/gone.body" || fail "the 404 body does not name tamp list"
pongs shop.localhost || fail "the catch-all shadows shop.localhost"

# site_has_app checks that bench lists app on a site.
site_has_app() {
  docker exec "$FRAPPE" bench --site "$1" list-apps --format json | grep -q "\"$2\""
}

say "tamp app add clones with host git, records the package name and default branch, and installs on every site"
bash "$SCRIPTS/minimal-app.sh" "$WORK/remotes/acme-erp.git"
ACME_URL=file://$WORK/remotes/acme-erp.git
"$TAMP" app add "$ACME_URL"
grep -qx '  name = "acme"' tamp.toml || fail "the recipe lacks the app acme: $(cat tamp.toml)"
grep -qx '  branch = "main"' tamp.toml || fail "the recipe lacks the default branch main: $(cat tamp.toml)"
[ "$(git -C apps/acme remote)" = upstream ] || fail "the acme clone's remote is not upstream"
[ ! -e apps/acme-erp ] || fail "the clone is named after the repository, not the package"
for site in shop.localhost erp.localhost; do
  site_has_app "$site" acme || fail "$site lacks acme"
done

say "tamp app list prints each app with its branch"
out=$("$TAMP" app list)
echo "$out"
grep -Eq '^frappe +version-15 ' <<<"$out" || fail "tamp app list lacks frappe"
grep -Eq "^acme +main +$ACME_URL\$" <<<"$out" || fail "tamp app list lacks acme at main"

say "after the restart, socket.io stays up"
pongs shop.localhost || fail "shop.localhost does not answer after app add"
sleep 15
first=$(docker exec "$FRAPPE" pgrep -f 'node apps/frappe/socketio.js') || fail "socket.io is not running"
sleep 15
[ "$(docker exec "$FRAPPE" pgrep -f 'node apps/frappe/socketio.js')" = "$first" ] || fail "socket.io restarted"

say "start warns about branch drift and unlisted folders, and touches no clone"
git -C apps/acme checkout --quiet -b drift
mkdir apps/stray
out=$("$TAMP" start 2>&1)
echo "$out"
grep -q "apps/acme is on drift, but the recipe names main" <<<"$out" || fail "no branch drift warning"
grep -qF "apps/stray is not in the recipe: add it with \`tamp app add <url>\`, or delete the folder" <<<"$out" ||
  fail "no unlisted folder warning"
[ "$(git -C apps/acme branch --show-current)" = drift ] || fail "start checked out another branch"
git -C apps/acme checkout --quiet main
git -C apps/acme branch --quiet -D drift
rmdir apps/stray

say "tamp app remove without --yes exits 5"
code=0
"$TAMP" app remove acme || code=$?
[ "$code" = 5 ] || fail "exit code $code, want 5"
site_has_app shop.localhost acme || fail "acme is gone without --yes"

say "tamp app remove --yes uninstalls from every site and the bench, keeps the clone, and prints its path"
out=$("$TAMP" app remove acme --yes)
echo "$out"
grep -qF "$ENV_DIR/apps/acme" <<<"$out" || fail "app remove did not print the clone's path"
[ -d apps/acme ] || fail "app remove deleted the clone"
if grep -q acme tamp.toml; then fail "the recipe still lists acme"; fi
for site in shop.localhost erp.localhost; do
  if site_has_app "$site" acme; then fail "$site still has acme"; fi
done
if docker exec "$FRAPPE" env/bin/python -c 'import acme' 2>/dev/null; then fail "acme is still in the bench's Python"; fi

say "the restart that tamp app remove makes logs no exit of honcho, so status stays running"
# honcho exits 143 on tamp's SIGTERM, and runs again 5 s later.
sleep 8
docker exec "$FRAPPE" bash -c 'test ! -s /tmp/tamp-honcho-exits' ||
  fail "tamp's own restart was logged as an exit: $(docker exec "$FRAPPE" cat /tmp/tamp-honcho-exits)"
out=$("$TAMP" status 2>&1)
grep -qx 'environment erp-dev: running' <<<"$out" || fail "tamp status after tamp's own restart: $out"

say "tamp app add adopts the kept clone without cloning"
out=$("$TAMP" app add "$ACME_URL" 2>&1)
echo "$out"
if grep -q 'Cloning' <<<"$out"; then fail "app add cloned again"; fi
for site in shop.localhost erp.localhost; do
  site_has_app "$site" acme || fail "$site lacks acme after adopting it"
done

say "start warns about a site the recipe does not list, and deletes nothing"
cp tamp.toml "$WORK/tamp.toml.sites"
printf 'frappe = "version-15"\n\n[[sites]]\n  host = "shop.localhost"\n' >tamp.toml
out=$("$TAMP" start 2>&1)
echo "$out"
grep -q 'warning: the bench holds the site erp.localhost, which the recipe does not list' <<<"$out" ||
  fail "start did not warn about erp.localhost"
docker exec "$FRAPPE" test -f sites/erp.localhost/site_config.json || fail "start deleted erp.localhost"
cp "$WORK/tamp.toml.sites" tamp.toml

say "the frappe container outlives honcho, and runs the bench's processes again"
docker exec "$FRAPPE" bash -c 'for p in /proc/[0-9]*; do [ "$(cat "$p/comm" 2>/dev/null)" = honcho ] && kill -TERM "${p#/proc/}"; done; true'
sleep 3
[ "$(docker inspect --format '{{ .State.Running }}' "$FRAPPE")" = true ] || fail "the frappe container exited with honcho"
pongs shop.localhost || fail "shop.localhost did not come back after honcho exited"

say "tamp shell runs one command as the bench user in the bench directory, and exits with its code"
[ "$("$TAMP" shell -- whoami </dev/null)" = frappe ] || fail "tamp shell does not run as frappe"
[ "$("$TAMP" shell -- pwd </dev/null)" = /home/frappe/frappe-bench ] || fail "tamp shell does not run in the bench directory"
code=0
"$TAMP" shell -- bash -c 'exit 7' </dev/null || code=$?
[ "$code" = 7 ] || fail "exit code $code, want the command's 7"

say "tamp shell passes stdin through, so bench mariadb reaches the database"
out=$(echo 'select 41 + 1;' | "$TAMP" shell -- bench --site shop.localhost mariadb)
echo "$out"
grep -qx 42 <<<"$out" || fail "bench mariadb did not answer through tamp shell"

say "tamp shell needs --, refuses bench start, and without a terminal a bare shell exits 2"
code=0
"$TAMP" shell bench --version </dev/null || code=$?
[ "$code" = 2 ] || fail "exit code $code without --, want 2"
code=0
"$TAMP" shell -- bench start </dev/null || code=$?
[ "$code" = 1 ] || fail "exit code $code for bench start, want 1"
code=0
timeout 30 "$TAMP" shell </dev/null || code=$?
[ "$code" = 2 ] || fail "exit code $code for a bare shell without a terminal, want 2"

say "tamp logs shows each service's logs, with --tail and -f"
for service in frappe mariadb redis-cache redis-queue router; do
  out=$("$TAMP" logs "$service" --tail 3 2>&1)
  lines=$(wc -l <<<"$out")
  [ -n "$out" ] && [ "$lines" -le 3 ] || fail "tamp logs $service --tail 3 printed $lines lines: $out"
done
code=0
timeout 5 "$TAMP" logs -f --tail 1 >/dev/null || code=$?
[ "$code" = 124 ] || fail "tamp logs -f ended by itself with $code, want it still following"
code=0
"$TAMP" logs web || code=$?
[ "$code" = 2 ] || fail "exit code $code for an unknown service, want 2"

say "tamp doctor passes a healthy environment, and fails port 80 held by something other than the router"
out=$("$TAMP" doctor) || fail "tamp doctor exited $? on a healthy environment: $out"
echo "$out"
if grep -qE '^(!|✗) ' <<<"$out"; then fail "tamp doctor flagged a healthy environment"; fi
docker stop tamp-router >/dev/null
docker run --detach --name tamp-e2e-port80 --publish 127.0.0.1:80:80 caddy:2.10 >/dev/null
code=0
out=$("$TAMP" doctor) || code=$?
docker rm --force tamp-e2e-port80 >/dev/null
docker start tamp-router >/dev/null
# The next stop reloads the router, whose admin API needs a moment after a start.
for _ in $(seq 1 20); do
  docker exec tamp-router wget -qO /dev/null http://localhost:2019/config/ 2>/dev/null && break
  sleep 1
done
echo "$out"
[ "$code" = 1 ] || fail "exit code $code for doctor with port 80 held, want 1"
grep -qF '✗ something other than the router holds port 80: stop the container that publishes port 80 with `docker stop tamp-e2e-port80`' <<<"$out" ||
  fail "doctor does not fail port 80 with a fix that names the container"

say "tamp context describes the running environment, with versions read from its containers, and changes nothing"
before=$(docker ps --all --format '{{.Names}} {{.State}}' | sort; cat tamp.toml "$CADDYFILE")
out=$(cd "$WORK" && "$TAMP" context -e erp-dev 2>"$WORK/context.err" </dev/null) || fail "tamp context exited $?"
echo "$out"
[ ! -s "$WORK/context.err" ] || fail "tamp context wrote to stderr: $(cat "$WORK/context.err")"
[ "$(docker ps --all --format '{{.Names}} {{.State}}' | sort; cat tamp.toml "$CADDYFILE")" = "$before" ] ||
  fail "tamp context changed a container, the recipe or the Caddyfile"
grep -qx '# tamp environment erp-dev' <<<"$out" || fail "the block does not name the environment"
grep -Eq '^- Frappe [0-9]+\.[0-9.]+, branch version-15, read from the frappe container$' <<<"$out" || fail "no Frappe version from the container"
grep -Eq '^- Python 3\.11\.[0-9]+, read from the frappe container$' <<<"$out" || fail "no Python version from the container"
grep -Eq '^- Node 18\.[0-9.]+, read from the frappe container$' <<<"$out" || fail "no Node version from the container"
grep -Eq '^- MariaDB 10\.11\.[0-9]+, read from the mariadb container$' <<<"$out" || fail "no MariaDB version from the container"
grep -qF "Edit app code on the host, in $ENV_DIR/apps:" <<<"$out" || fail "the block lacks the host apps/ path"
grep -qx -- '- http://shop.localhost' <<<"$out" || fail "the block lacks shop.localhost"
grep -qF '`Administrator`, with the password `password`' <<<"$out" || fail "the block lacks the admin login"
grep -qF 'Never run `bench start`' <<<"$out" && grep -qF 'Never run `bench update`' <<<"$out" ||
  fail "the block does not forbid bench start and bench update"

say "every example tamp context prints runs as printed, without a terminal, from another directory"
examples=$(awk '/^```/ { fenced = !fenced; next } fenced' <<<"$out")
grep -q 'run-tests' <<<"$examples" && grep -q 'execute' <<<"$examples" && grep -q -- '--resolve shop.localhost:80:127.0.0.1' <<<"$examples" ||
  fail "the examples lack run-tests, execute or curl --resolve: $examples"
if grep -q console <<<"$examples"; then fail "an example runs console"; fi
# Prose lines too, as an agent copies a command from a sentence as readily.
if grep -q -- 'run-tests --app' <<<"$out"; then fail "a line runs a whole app's suite"; fi
if grep -E 'run-tests|allow_tests|CI=1' <<<"$out" | grep -vq -- 'erp-dev-test.localhost'; then
  fail "a line mentions tests without naming erp-dev-test.localhost: $(grep -E 'run-tests|allow_tests|CI=1' <<<"$out")"
fi
while IFS= read -r line; do
  echo "\$ $line"
  # The examples call tamp by name, as an agent would.
  (cd "$WORK" && PATH=$(dirname "$TAMP"):$PATH bash -c "$line" </dev/null >"$WORK/example.out" 2>&1) ||
    fail "the example \`$line\` exited $?: $(tail -5 "$WORK/example.out")"
done <<<"$examples"

say "the printed test line exits non-zero when a test fails"
failing=$ENV_DIR/apps/acme/acme/acme/test_tamp_e2e.py
printf 'import unittest\n\n\nclass TestFails(unittest.TestCase):\n\tdef test_fails(self):\n\t\tself.assertEqual(1, 2)\n' >"$failing"
line=$(cd "$WORK" && "$TAMP" context -e erp-dev 2>/dev/null | grep -- ' run-tests ')
echo "\$ $line"
grep -q -- '--module acme.acme.test_tamp_e2e$' <<<"$line" || fail "the test line does not run the app's test module: $line"
code=0
(cd "$WORK" && PATH=$(dirname "$TAMP"):$PATH bash -c "$line" </dev/null >"$WORK/example.out" 2>&1) || code=$?
rm "$failing"
[ "$code" != 0 ] || fail "the test line exited 0 on a failing test: $(tail -5 "$WORK/example.out")"

say "a bench whose processes keep crashing reads as crash looping, and makes tamp start exit 1 at once"
# A listener on the web port makes bench serve exit as it starts, so honcho
# exits again and again. Killed by its signal, honcho logs no exit.
docker exec "$FRAPPE" bash -c 'for p in /proc/[0-9]*; do [ "$(cat "$p/comm" 2>/dev/null)" = honcho ] && kill -TERM "${p#/proc/}"; done; true'
sleep 2
docker exec --detach "$FRAPPE" env/bin/python -m http.server 8000
looping=
for _ in $(seq 1 30); do
  out=$("$TAMP" status 2>&1)
  if grep -qx 'environment erp-dev: crash looping' <<<"$out"; then
    looping=1
    break
  fi
  sleep 2
done
echo "$out"
[ -n "$looping" ] || fail "tamp status does not read the crash loop"
grep -qF 'warning: the bench processes of erp-dev keep crashing: honcho exited' <<<"$out" &&
  grep -qF 'find out why with `tamp logs frappe`' <<<"$out" || fail "tamp status does not warn with the logs fix"
out=$(cd "$WORK" && "$TAMP" list 2>&1)
echo "$out"
grep -Eq '^erp-dev +crash looping ' <<<"$out" || fail "tamp list does not show the crash loop"
grep -qF 'find out why with `tamp logs frappe -e erp-dev`' <<<"$out" || fail "tamp list does not warn with the logs fix"
code=0
out=$("$TAMP" doctor) || code=$?
echo "$out"
[ "$code" = 1 ] || fail "exit code $code for doctor on a crash loop, want 1"
grep -q '^✗ the bench processes of erp-dev keep crashing: honcho exited [0-9]* times in the last 2 minutes: find out why with `tamp logs frappe`$' <<<"$out" ||
  fail "doctor does not fail the crash loop with the logs fix"
code=0
start=$SECONDS
out=$("$TAMP" start 2>&1) || code=$?
took=$((SECONDS - start))
echo "$out"
[ "$code" = 1 ] || fail "exit code $code for start on a crash loop, want 1"
[ "$took" -lt 60 ] || fail "start took $took s to report the crash loop, want it before its 2-minute wait ends"
grep -qF 'error: the bench processes of erp-dev keep crashing' <<<"$out" || fail "start does not name the crash loop"
docker exec "$FRAPPE" pkill -f 'http.server 8000'
"$TAMP" start
answers shop.localhost || fail "shop.localhost did not answer after the crash loop ended"
grep -qx 'environment erp-dev: running' <<<"$("$TAMP" status)" || fail "tamp status still reads a crash loop after start"

say "tamp stop stops the containers and takes the sites off the router"
"$TAMP" stop
for service in frappe mariadb redis-cache redis-queue; do
  [ "$(docker inspect --format '{{ .State.Running }}' "erp-dev-$service-1")" = false ] || fail "erp-dev-$service-1 still runs"
done
[ "$(docker inspect --format '{{ .State.Running }}' tamp-router)" = true ] || fail "tamp stop stopped the router"
if grep -q 'shop.localhost' "$CADDYFILE"; then fail "the Caddyfile still routes the stopped shop.localhost"; fi

cd "$WORK"

say "tamp list shows the stopped environment with its sites"
out=$("$TAMP" list)
echo "$out"
grep -Eq "^erp-dev +stopped +$ENV_DIR\$" <<<"$out" || fail "tamp list does not show erp-dev as stopped"
grep -qx '  http://shop.localhost' <<<"$out" || fail "tamp list does not nest shop.localhost"

say "tamp context on the stopped environment prints from the recipe, says it is stopped, and starts nothing"
out=$("$TAMP" context -e erp-dev 2>"$WORK/context.err") || fail "tamp context exited $?"
echo "$out"
[ ! -s "$WORK/context.err" ] || fail "tamp context wrote to stderr: $(cat "$WORK/context.err")"
grep -q '^The environment is stopped' <<<"$out" || fail "the block does not say the environment is stopped"
grep -qx -- '- Python 3.11, from the recipe, because the frappe container is not running' <<<"$out" || fail "the Python version is not the recipe's"
grep -qx -- '- http://shop.localhost' <<<"$out" || fail "the block lacks shop.localhost"
[ "$(docker inspect --format '{{ .State.Running }}' "$FRAPPE")" = false ] || fail "tamp context started the frappe container"

say "tamp status -e from another directory shows the containers and site URLs"
out=$("$TAMP" status -e erp-dev)
echo "$out"
grep -Eq '^  erp-dev-frappe-1 +stopped$' <<<"$out" || fail "tamp status does not show the stopped frappe container"
grep -qx '  http://erp.localhost' <<<"$out" || fail "tamp status does not show erp.localhost"

say "no -e outside an environment directory exits 2, and an unknown -e exits 3"
code=0
"$TAMP" status || code=$?
[ "$code" = 2 ] || fail "exit code $code, want 2"
code=0
"$TAMP" status -e nope || code=$?
[ "$code" = 3 ] || fail "exit code $code, want 3"

say "a moved directory's start recreates the containers with the new path and keeps the sites"
mkdir "$WORK/moved"
mv "$ENV_DIR" "$WORK/moved/"
ENV_DIR=$WORK/moved/erp-dev
cd "$ENV_DIR"
"$TAMP" start
[ "$(docker inspect --format '{{ index .Config.Labels "tamp.path" }}' "$FRAPPE")" = "$ENV_DIR" ] ||
  fail "the frappe container does not carry the new tamp.path"
answers shop.localhost || fail "shop.localhost does not answer after the move"

say "a same-named environment elsewhere that has a recipe makes start exit 1"
mkdir -p "$WORK/second/erp-dev"
printf 'frappe = "version-15"\n' >"$WORK/second/erp-dev/tamp.toml"
code=0
out=$(cd "$WORK/second/erp-dev" && "$TAMP" start 2>&1) || code=$?
echo "$out"
[ "$code" = 1 ] || fail "exit code $code, want 1"
grep -q 'rename this directory' <<<"$out" || fail "the error does not say to rename the directory"

say "a recipe for another Frappe version makes start exit 1 and build nothing"
cp tamp.toml "$WORK/tamp.toml.saved"
sed -i 's/^frappe = .*/frappe = "version-16"/' tamp.toml
code=0
out=$("$TAMP" start 2>&1) || code=$?
echo "$out"
[ "$code" = 1 ] || fail "exit code $code, want 1"
grep -qF 'run `tamp stop`, then fetch and check out version-16 with `git -C apps/frappe remote set-branches --add upstream version-16`, `git -C apps/frappe fetch --depth 1 upstream version-16`, and `git -C apps/frappe checkout version-16`, then run `tamp remove --yes` and `tamp start`' <<<"$out" ||
  fail "the error lacks the version fix"
if grep -q 'building the image' <<<"$out"; then fail "start built an image"; fi
cp "$WORK/tamp.toml.saved" tamp.toml

say "tamp remove without --yes exits 5 and removes nothing"
code=0
"$TAMP" remove || code=$?
[ "$code" = 5 ] || fail "exit code $code, want 5"
docker inspect "$FRAPPE" >/dev/null || fail "tamp remove without --yes removed the frappe container"

say "tamp remove --yes deletes the containers, network, and volumes, and keeps the directory"
"$TAMP" remove --yes
[ -z "$(docker ps --all --quiet --filter label=tamp.env=erp-dev)" ] || fail "erp-dev's containers remain"
if docker network inspect erp-dev_default >/dev/null 2>&1; then fail "erp-dev_default remains"; fi
for volume in erp-dev_db erp-dev_bench; do
  if docker volume inspect "$volume" >/dev/null 2>&1; then fail "$volume remains"; fi
done
docker volume inspect tamp-cache >/dev/null || fail "tamp remove deleted tamp-cache"
[ -f tamp.toml ] && [ -d apps/frappe ] || fail "tamp remove touched the directory"
[ "$(docker inspect --format '{{ .State.Running }}' tamp-router)" = true ] || fail "tamp remove stopped the router"
if grep -q 'localhost' "$CADDYFILE"; then fail "the Caddyfile still routes erp-dev's sites"; fi

say "after tamp remove --yes, tamp start builds a new bench whose sites have their assets and apps"
"$TAMP" start
answers shop.localhost || fail "shop.localhost does not answer on the new bench"
bundle=$(docker exec "$FRAPPE" sh -c 'ls apps/frappe/frappe/public/dist/js | grep "^frappe-web\.bundle\..*\.js$" | head -n 1')
[ -n "$bundle" ] || fail "no frappe-web bundle in the clone"
code=$(through_router -o /dev/null -w '%{http_code}' "http://shop.localhost/assets/frappe/dist/js/$bundle") || code=$?
[ "$code" = 200 ] || fail "the new bench serves /assets/frappe/dist/js/$bundle with $code, want 200"
site_has_app shop.localhost acme || fail "shop.localhost lacks acme on the new bench"

say "e2e passed"
