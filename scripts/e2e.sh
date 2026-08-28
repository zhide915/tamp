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
  say "e2e failed — engine state"
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

say "create fifteen — version-15, with erpnext pinned to its branch"
"$TAMP" create fifteen --frappe version-15 --apps erpnext:version-15 --dir "$WORK"

say "site on fifteen"
"$TAMP" site new fifteen fifteen.localhost --admin-password admin

say "fifteen serves through the router"
expect fifteen.localhost /api/method/ping
expect mail.fifteen.localhost /

say "create sixteen — version-16, alongside fifteen, under Mutagen"
# fifteen keeps bind, the Linux default; Mutagen here puts its pin under
# the scheduled drift run.
"$TAMP" create sixteen --frappe version-16 --sync mutagen --dir "$WORK"
# A blocked Mutagen falls back to a bind mount with exit 0; only the
# compose file records the mode.
if grep -q '\./apps:' "$WORK/sixteen/compose.yaml"; then
  fail "sixteen fell back to a bind mount — the Mutagen pin was never exercised"
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
