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

say "a second tamp start repeats no completed work"
cd erp-dev
out=$("$TAMP" start 2>&1)
echo "$out"
if grep -q 'building the image\|Cloning\|running bench init\|creating the site' <<<"$out"; then
  fail "tamp start repeated work"
fi

say "e2e passed"
