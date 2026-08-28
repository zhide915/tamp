#!/usr/bin/env bash
# The whole loop against real Docker, run by the E2E workflow on ubuntu.
set -euo pipefail

TAMP=${1:?usage: e2e.sh /path/to/tamp}
WORK=$(mktemp -d)

say() { printf '\n=== %s\n' "$*"; }

fail() {
  echo "e2e: $*" >&2
  exit 1
}

dump() {
  say "e2e failed â€” engine state"
  docker ps -a || true
  echo
  docker logs tamp-router-caddy-1 --tail 50 2>&1 || true
  echo
  cat "$HOME/.tamp/router/Caddyfile" || true
}
# EXIT, not ERR: fail() leaves through exit, which an ERR trap never sees.
trap 'code=$?; if [ "$code" -ne 0 ]; then dump; fi' EXIT

# The router publishes 80, or 8080 when 80 was busy; router.json records which.
router_port() {
  local state="$HOME/.tamp/router/router.json"
  if [ -f "$state" ]; then jq -r '.port // 80' "$state"; else echo 80; fi
}

# Retries: the bench's first request may still be importing apps.
expect() {
  local host=$1 path=$2 port code=000
  port=$(router_port)
  for _ in $(seq 1 30); do
    code=$(curl -s -o /dev/null -w '%{http_code}' \
      -H "Host: $host" "http://127.0.0.1:$port$path") || code=000
    if [ "$code" = 200 ]; then return 0; fi
    sleep 4
  done
  fail "wanted 200 for Host: $host at $path, last answer was $code"
}

say "create fifteen â€” version-15, with erpnext pinned to its branch"
"$TAMP" create fifteen --frappe version-15 --apps erpnext:version-15 --dir "$WORK"

say "site on fifteen"
"$TAMP" site new fifteen fifteen.localhost --admin-password admin

say "fifteen serves through the router"
expect fifteen.localhost /api/method/ping
expect mail.fifteen.localhost /

say "create sixteen â€” version-16, alongside fifteen, under Mutagen"
# fifteen keeps bind, the Linux default; Mutagen here puts its pin under
# the scheduled drift run.
"$TAMP" create sixteen --frappe version-16 --sync mutagen --dir "$WORK"
# A blocked Mutagen falls back to a bind mount with exit 0; only the
# compose file records the mode.
if grep -q '\./apps:' "$WORK/sixteen/compose.yaml"; then
  fail "sixteen fell back to a bind mount â€” the Mutagen pin was never exercised"
fi
"$TAMP" site new sixteen sixteen.localhost --admin-password admin

say "both environments serve simultaneously"
expect sixteen.localhost /api/method/ping
expect mail.sixteen.localhost /
expect fifteen.localhost /api/method/ping

say "the sync session mirrors sixteen's source to the host"
found=no
for _ in $(seq 1 75); do
  if [ -e "$WORK/sixteen/apps/frappe/pyproject.toml" ]; then
    found=yes
    break
  fi
  sleep 4
done
[ "$found" = yes ] || fail "Mutagen never delivered apps/frappe to the host"

say "each environment runs its own MariaDB version"
# The pins own the exact versions; assert only that the two differ.
mariadb=$(docker ps --format '{{.Image}}' | grep '^mariadb:' | sort -u || true)
if [ "$(printf '%s' "$mariadb" | grep -c .)" -ne 2 ]; then
  fail "expected two distinct MariaDB versions, saw: ${mariadb:-none}"
fi

say "a warm create of a version-15 bench comes from the template cache"
# The first create above filled the store; this one must unpack it. Two
# minutes is the promise, and it is measured with everything else the create
# does still in it.
start=$(date +%s)
"$TAMP" create warm --frappe version-15 --dir "$WORK" > "$WORK/warm.log" 2>&1 || {
  cat "$WORK/warm.log"
  fail "the warm create failed"
}
warm_seconds=$(( $(date +%s) - start ))
grep -q "template cache hit for version-15" "$WORK/warm.log"   || fail "the warm create did not use the stored template: $(grep -i template "$WORK/warm.log" || echo 'it said nothing about the cache')"
echo "warm create took ${warm_seconds}s"
[ "$warm_seconds" -lt 120 ] || fail "the warm create took ${warm_seconds}s, and the promise is under 120"

say "the warm environment serves, so the template is a working bench"
"$TAMP" site new warm warm.localhost --admin-password admin
expect warm.localhost /api/method/ping

"$TAMP" rm warm --volumes --yes

# develop tracks a branch that moves, so it belongs to the run that exists to
# notice that — a scheduled or hand-started one, not every pull request.
if [ "${TAMP_E2E_DEVELOP:-0}" = 1 ]; then
  say "create dev — the develop preset, beside the stable environments"
  "$TAMP" create dev --frappe develop --dir "$WORK"
  "$TAMP" site new dev dev.localhost --admin-password admin

  say "the develop bench runs the develop toolchain"
  in_bench() { "$TAMP" exec dev -- bash -c ". /home/frappe/.tamp-toolchain/env.sh; $1"; }
  in_bench "python --version" | grep -q "3\.14" || fail "the develop bench is not on Python 3.14"
  in_bench "node --version" | grep -q "^v24\." || fail "the develop bench is not on Node 24"
  docker ps --format "{{.Names}} {{.Image}}" | grep "^tamp-dev-" | grep -q "mariadb:11.8" \
    || fail "the develop environment is not on MariaDB 11.8"

  say "develop and version-15 serve at the same time"
  expect dev.localhost /api/method/ping
  expect fifteen.localhost /api/method/ping
  "$TAMP" list | grep -q develop || fail "tamp list does not show the develop environment"

  "$TAMP" rm dev --volumes --yes
fi

say "rm fifteen leaves the source tree intact"
# Positive check first, so the survived-rm grep cannot silently match nothing.
docker ps --format '{{.Names}}' | grep -q '^tamp-fifteen-' \
  || fail "expected running fifteen containers before rm"
"$TAMP" rm fifteen --yes
[ -d "$WORK/fifteen/apps/frappe" ] || fail "rm lost apps/frappe"
[ -d "$WORK/fifteen/apps/erpnext" ] || fail "rm lost apps/erpnext"
if docker ps -a --format '{{.Names}}' | grep -q '^tamp-fifteen-'; then
  fail "fifteen containers survived rm"
fi

say "sixteen still serves after fifteen is gone"
expect sixteen.localhost /api/method/ping

"$TAMP" rm sixteen --yes

say "e2e passed"
