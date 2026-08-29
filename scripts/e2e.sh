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

# resolved is the same check without the Host header: the name has to reach
# 127.0.0.1 by itself, which is the whole point of the hosts entry.
resolved() {
  local host=$1 path=$2 port code
  port=$(router_port)
  code=$(curl -s -o /dev/null -w '%{http_code}' "http://$host:$port$path") || code=000
  [ "$code" = 200 ] || fail "$host does not resolve to the router: got $code"
}

say "create fifteen — version-15, with erpnext pinned to its branch"
"$TAMP" create fifteen --frappe version-15 --apps erpnext:version-15 --dir "$WORK"

say "a bare site, to measure what every site creation pays before any app work"
# bench new-site dominates a small site creation and both paths below pay it,
# so the seed's promise is only about what is left once it is subtracted.
base_start=$(date +%s)
"$TAMP" site new fifteen bare.localhost --admin-password admin
base_seconds=$(( $(date +%s) - base_start ))
echo "a bare site took ${base_seconds}s"

say "site on fifteen, with erpnext installed on it"
install_start=$(date +%s)
"$TAMP" site new fifteen fifteen.localhost --apps erpnext --admin-password admin
install_seconds=$(( $(date +%s) - install_start ))
echo "installing erpnext onto a site took ${install_seconds}s"

say "fifteen serves through the router"
expect fifteen.localhost /api/method/ping
expect mail.fifteen.localhost /

say "a second site of the same app set restores that seed instead of installing"
seed_start=$(date +%s)
"$TAMP" site new fifteen seeded.localhost --apps erpnext --seed --admin-password admin > "$WORK/seeded.log" 2>&1 || {
  cat "$WORK/seeded.log"
  fail "the seeded site creation failed"
}
seed_seconds=$(( $(date +%s) - seed_start ))
grep -q "seed, restored and migrated" "$WORK/seeded.log"   || fail "site new --seed did not say it restored a seed: $(cat "$WORK/seeded.log")"

# Both numbers carry the bare site's cost, which no seed can remove; what the
# seed replaces is what is left. Compared at a third, the promise's generous
# form, and a regression to the install path fails it outright.
install_work=$(( install_seconds - base_seconds ))
seed_work=$(( seed_seconds - base_seconds ))
# A seed can measure faster than a bare site through nothing but noise; one
# second keeps the comparison meaningful without inventing a saving.
if [ "$seed_work" -lt 1 ]; then
  seed_work=1
fi
echo "the app work took ${seed_work}s from the seed against ${install_work}s installing"
[ "$install_work" -gt 0 ]   || fail "installing erpnext measured ${install_work}s over a bare site, so there is nothing to compare"
[ $(( seed_work * 3 )) -lt "$install_work" ]   || fail "the seed saved too little: ${seed_work}s against ${install_work}s installing"
# A working ERPNext site, not merely a routed one: list-apps reads its
# database, which is the part the seed carried.
expect seeded.localhost /api/method/ping
"$TAMP" exec fifteen -- bench --site seeded.localhost list-apps | grep -q erpnext   || fail "the seeded site does not have erpnext installed"

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

say "clean --deps then rebuild leaves the site serving with its data"
# list-apps reads the site's database, so it says more than a route does: an
# empty site would answer /api/method/ping just as well.
installed() { "$TAMP" exec warm -- bench --site warm.localhost list-apps | awk '{print $1}' | sort; }
before=$(installed)
"$TAMP" clean warm --deps
# The deps clean stops the bench processes, so nothing answers until the
# rebuild puts them back.
"$TAMP" rebuild warm
expect warm.localhost /api/method/ping
[ "$(installed)" = "$before" ] || fail "the site lost its data across clean --deps and rebuild"

say "clean --assets then rebuild restores the built assets"
"$TAMP" clean warm --assets
"$TAMP" exec warm -- test ! -d sites/assets || fail "clean --assets left the built assets behind"
"$TAMP" rebuild warm
"$TAMP" exec warm -- test -d sites/assets || fail "rebuild did not restore sites/assets"
expect warm.localhost /api/method/ping

say "a snapshot of warm records the site and its apps"
"$TAMP" snapshot create warm --name e2e
"$TAMP" snapshot list warm | grep -q e2e || fail "snapshot list does not show the snapshot just taken"
[ -f "$WORK/warm/.tamp/snapshots/e2e.tar.gz" ] || fail "the snapshot bundle is not in the environment directory"
[ -f "$WORK/warm/.tamp/snapshots/e2e.json" ] || fail "the snapshot manifest is not beside the bundle"

say "clean --data needs --yes, and takes every site when it gets one"
if "$TAMP" clean warm --data; then
  fail "clean --data destroyed the data layer without --yes"
fi
"$TAMP" clean warm --data --yes
[ -z "$("$TAMP" site list warm 2>/dev/null | grep warm.localhost || true)" ]   || fail "clean --data left warm.localhost on the bench"
if grep -q "http://warm.localhost" "$HOME/.tamp/router/Caddyfile"; then
  fail "the router still routes a site clean --data destroyed"
fi
[ -d "$WORK/warm/apps/frappe" ] || fail "clean --data deleted the source tree"

say "snapshot restore brings the wiped site fully back"
# No --yes: clean --data left nothing for the restore to write over.
"$TAMP" snapshot restore warm
expect warm.localhost /api/method/ping
# list-apps reads the restored database, so it proves the data came back and
# not merely the route.
"$TAMP" exec warm -- bench --site warm.localhost list-apps | grep -q frappe   || fail "the restored site has no apps — its database did not come back"
"$TAMP" site list warm | grep -q warm.localhost || fail "the restored site is not listed"

say "a second restore over live data needs --yes"
if "$TAMP" snapshot restore warm --name e2e; then
  fail "restoring over live site data did not ask for --yes"
fi
"$TAMP" snapshot restore warm --name e2e --yes
expect warm.localhost /api/method/ping

say "sync status names the bind mode and exits 0, because Linux has no session"
"$TAMP" sync status warm | grep -q "mode: bind" || fail "sync status did not report the bind mode"

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

say "a custom domain becomes browsable after hosts sync"
# The one operation that elevates. The runner's sudo needs no password, so
# this exercises the real Linux elevation path.
cp /etc/hosts "$WORK/hosts.before"
"$TAMP" site new fifteen abc.xyz.com --admin-password admin
"$TAMP" site list fifteen | grep abc.xyz.com | grep -q pending   || fail "site list does not mark the custom domain's hosts entry pending"
"$TAMP" hosts sync
grep -q "127.0.0.1  abc.xyz.com" /etc/hosts || fail "hosts sync did not write the entry"
grep -q "^# --- tamp managed block ---$" /etc/hosts || fail "hosts sync wrote outside a marked block"

# Everything the machine had must survive verbatim; only tamp's block is new.
sed '/^# --- tamp managed block ---$/,/^# --- end tamp block ---$/d' /etc/hosts > "$WORK/hosts.outside"
diff -u "$WORK/hosts.before" "$WORK/hosts.outside" || fail "hosts sync changed content outside its block"

"$TAMP" site list fifteen | grep abc.xyz.com | grep -q ok   || fail "site list still marks the entry pending after a sync"
expect abc.xyz.com /api/method/ping
resolved abc.xyz.com /api/method/ping
"$TAMP" doctor | grep -q "Hosts file" || fail "doctor does not report the hosts block"

say "removing the site takes its line out on the next sync"
"$TAMP" site rm fifteen abc.xyz.com --yes
"$TAMP" hosts sync
if grep -q "abc.xyz.com" /etc/hosts; then
  fail "the removed site kept its hosts line"
fi
diff -u "$WORK/hosts.before" /etc/hosts || fail "the hosts file did not come back byte for byte"

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
