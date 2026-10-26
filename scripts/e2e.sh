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

cleanup() {
  # tamp never removes the router; the network below cannot go while it is attached.
  docker rm --force tamp-router >/dev/null 2>&1 || true
  if [ -f "$ENV_DIR/compose.yaml" ]; then
    docker compose --project-name erp-dev --file "$ENV_DIR/compose.yaml" down --volumes --timeout 5 || true
  fi
  docker volume rm erp-dev_bench >/dev/null 2>&1 || true
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
answered=
for _ in $(seq 1 30); do
  if body=$(through_router http://shop.localhost/api/method/ping); then
    answered=$body
    break
  fi
  sleep 2
done
echo "$answered"
grep -q pong <<<"$answered" || fail "shop.localhost did not answer through the router"

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
after=$(through_router http://shop.localhost/api/method/ping) || fail "shop.localhost stopped answering after the reload"
grep -q pong <<<"$after" || fail "shop.localhost answered $after after the reload"
socketio_handshake || fail "the socket.io handshake failed after the reload"

say "a site added to the recipe is routed by the running router after tamp start"
printf '\n[[sites]]\n  host = "erp.localhost"\n' >>tamp.toml
"$TAMP" start
[ "$(docker inspect --format '{{ .State.StartedAt }}' tamp-router)" = "$router_started" ] ||
  fail "tamp start restarted the router instead of reloading it"
added=$(curl -sf --max-time 10 --resolve erp.localhost:80:127.0.0.1 http://erp.localhost/api/method/ping) ||
  fail "erp.localhost does not answer through the router"
grep -q pong <<<"$added" || fail "erp.localhost answered $added"

say "e2e passed"
