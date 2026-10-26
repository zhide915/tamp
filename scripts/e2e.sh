#!/usr/bin/env bash
# Runs the built binary end to end against real Docker, on ubuntu in the E2E
# workflow. Each slice of the rebuild extends it.
set -euo pipefail

TAMP=${1:?usage: e2e.sh /path/to/tamp}
# Absolute, because the script changes directory before it runs tamp.
TAMP=$(cd "$(dirname "$TAMP")" && pwd)/$(basename "$TAMP")
WORK=$(mktemp -d)
ENV_DIR=$WORK/erp-dev
FRAPPE=erp-dev-frappe-1
CADDYFILE=$HOME/.tamp/router/Caddyfile

cleanup() {
  "$TAMP" remove -e erp-dev --yes --quiet || true
  # tamp never removes the router.
  docker rm --force tamp-router >/dev/null 2>&1 || true
  docker network rm tamp-router_default >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

say() { printf '\n=== %s\n' "$*"; }
fail() {
  echo "e2e: $*" >&2
  exit 1
}

# through_router fetches a URL from the router on port 80, whatever
# *.localhost resolves to on this machine. curl sends no Origin header.
through_router() {
  curl -sf --max-time 10 --resolve shop.localhost:80:127.0.0.1 "$@"
}

# pongs polls a site's ping through the router for up to 60 s and prints the
# reply: tamp start does not wait for Frappe to finish booting.
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

say "tamp new builds the environment"
start=$SECONDS
"$TAMP" new erp-dev --frappe version-15 --site shop.localhost
echo "tamp new took $((SECONDS - start))s"
grep -qx 'frappe = "version-15"' erp-dev/tamp.toml || fail "unexpected recipe: $(cat erp-dev/tamp.toml)"
grep -qx '  host = "shop.localhost"' erp-dev/tamp.toml || fail "the recipe lacks the site: $(cat erp-dev/tamp.toml)"
grep -qx 'apps/' erp-dev/.gitignore || fail ".gitignore lacks apps/"
grep -qx 'compose.yaml' erp-dev/.gitignore || fail ".gitignore lacks compose.yaml"
[ "$(git -C erp-dev/apps/frappe remote)" = upstream ] || fail "the Frappe clone's remote is not upstream"
[ "$(git -C erp-dev/apps/frappe rev-parse --is-shallow-repository)" = true ] || fail "the Frappe clone is not shallow"

say "the bench volume carries the Frappe branch, and the containers both labels"
[ "$(docker volume inspect --format '{{ index .Labels "tamp.frappe" }}' erp-dev_bench)" = version-15 ] ||
  fail "the bench volume lacks tamp.frappe=version-15"
for service in frappe mariadb redis-cache redis-queue; do
  labels=$(docker inspect --format '{{ index .Config.Labels "tamp.env" }} {{ index .Config.Labels "tamp.path" }}' "erp-dev-$service-1")
  [ "$labels" = "erp-dev $ENV_DIR" ] || fail "erp-dev-$service-1 has labels '$labels'"
done

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

say "after a second start, the reloaded router still serves the site and socket.io"
pongs shop.localhost || fail "shop.localhost stopped answering after the reload"
socketio_handshake || fail "the socket.io handshake failed after the reload"

say "a site added to the recipe is routed by the running router after tamp start"
printf '\n[[sites]]\n  host = "erp.localhost"\n' >>tamp.toml
"$TAMP" start
[ "$(docker inspect --format '{{ .State.StartedAt }}' tamp-router)" = "$router_started" ] ||
  fail "tamp start restarted the router instead of reloading it"
pongs erp.localhost || fail "erp.localhost does not answer through the router"

say "tamp site add writes the recipe, creates the site, and routes it"
"$TAMP" site add blog.localhost
grep -qx '  host = "blog.localhost"' tamp.toml || fail "the recipe lacks blog.localhost: $(cat tamp.toml)"
grep -q '^http://blog.localhost {' "$CADDYFILE" || fail "the Caddyfile does not route blog.localhost"
pongs blog.localhost || fail "blog.localhost does not answer through the router"

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

say "tamp status -e from another directory shows the containers and site URLs"
out=$("$TAMP" status -e erp-dev)
echo "$out"
grep -Eq '^  erp-dev-frappe-1 +stopped$' <<<"$out" || fail "tamp status does not show the stopped frappe container"
grep -qx '  http://erp.localhost' <<<"$out" || fail "tamp status does not show erp.localhost"

say "no -e outside an environment directory exits 2; an unknown -e exits 3"
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
pongs shop.localhost || fail "shop.localhost does not answer after the move"

say "a same-named environment elsewhere that has a recipe makes start exit 1"
mkdir -p "$WORK/second/erp-dev"
printf 'frappe = "version-15"\n' >"$WORK/second/erp-dev/tamp.toml"
code=0
out=$(cd "$WORK/second/erp-dev" && "$TAMP" start 2>&1) || code=$?
echo "$out"
[ "$code" = 1 ] || fail "exit code $code, want 1"
grep -q 'rename the directory' <<<"$out" || fail "the error does not say to rename the directory"

say "a recipe for another Frappe version makes start exit 1 and build nothing"
cp tamp.toml "$WORK/tamp.toml.saved"
sed -i 's/^frappe = .*/frappe = "version-16"/' tamp.toml
code=0
out=$("$TAMP" start 2>&1) || code=$?
echo "$out"
[ "$code" = 1 ] || fail "exit code $code, want 1"
grep -qF 'check out version-16 in apps/frappe, then `tamp remove --yes` and `tamp start`' <<<"$out" ||
  fail "the error lacks the version fix"
if grep -q 'building the image' <<<"$out"; then fail "start built an image"; fi
cp "$WORK/tamp.toml.saved" tamp.toml

say "tamp remove without --yes exits 5 and removes nothing"
code=0
"$TAMP" remove || code=$?
[ "$code" = 5 ] || fail "exit code $code, want 5"
docker inspect "$FRAPPE" >/dev/null || fail "tamp remove without --yes removed the frappe container"

say "tamp remove --yes deletes the containers, network and volumes, and keeps the directory"
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

say "e2e passed"
