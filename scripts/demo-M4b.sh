#!/usr/bin/env bash
# M4b demo (docs/specs/M4b-dkg.md, acceptance check 2) on the docker localnet started
# without --dev-dealer:
#   1. no custody epoch at genesis; the epoch-1 DKG runs through transactions and the
#      epoch becomes ACTIVE within about 80 blocks;
#   2. every node's derived group key matches epoch_activated;
#   3. the M4a deposit-and-withdraw demo then succeeds on the DKG key.
# Needs: Docker, curl, jq. Leaves the localnet stopped (make localnet-clean to reset).
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"
WORK=$ROOT/.demo/M4b
# shellcheck source=lib/localnet.sh
. "$ROOT/scripts/lib/localnet.sh"

rm -rf "$WORK"; mkdir -p "$WORK"
make --no-print-directory localnet-clean >/dev/null 2>&1 || true
make --no-print-directory localnet-start DEALER= >"$WORK/start.log" 2>&1 || { tail -20 "$WORK/start.log"; fail "make localnet-start DEALER="; }
pass "localnet started without a dealer key (4 nodes, each with custody keys and a DKG participant)"
wait_height 3 300
if q custody active-epoch >/dev/null 2>&1; then fail "a custody epoch exists before the DKG"; fi
st=$(q custody dkg 1)
[ "$(printf '%s' "$st" | jq '.state.participants | length')" = "4" ] || fail "DKG participants: $st"
pass "no epoch at genesis; DKG for epoch 1 opened at height $(printf '%s' "$st" | jq -r .state.start_height) with 4 participants, t = $(printf '%s' "$st" | jq -r .state.t)"

# 1. epoch_activated within about 80 blocks.
hit=""
h=2
while [ -z "$hit" ] && [ "$h" -le 85 ]; do
  wait_height "$h" 120
  hit=$(find_event 0 "$h" "$h" epoch_activated '.epoch == "1"')
  h=$((h + 1))
done
[ -n "$hit" ] || fail "epoch 1 not activated by height 85"
HA=${hit%% *}
GK=$(printf '%s' "${hit#* }" | jq -r .group_key)
QUAL=$(printf '%s' "${hit#* }" | jq -r .qualified | tr ',' '\n' | grep -c .)
for i in 1 2 3; do
  [ -n "$(find_event "$i" "$HA" "$HA" epoch_activated ".group_key == \"$GK\"")" ] || fail "node$i: different epoch_activated at $HA"
done
[ "$(q custody active-epoch | jq -r .epoch.status)" = "EPOCH_STATUS_ACTIVE" ] || fail "epoch 1 not ACTIVE"
pass "epoch_activated at height $HA on all 4 nodes ($QUAL qualified dealers); group key $GK"

# 2. Every node derived its share and its group key matches.
for i in 0 1 2 3; do
  start=$SECONDS
  until out=$("$BIN" custody shares 1 --home "$H/node$i" --passphrase-file "$H/node$i/custody/passphrase" 2>/dev/null); do
    [ $((SECONDS - start)) -lt 60 ] || fail "node$i wrote no share for epoch 1"
    sleep 2
  done
  [ "$(printf '%s' "$out" | jq -r .group_key)" = "$GK" ] || fail "node$i group key $(printf '%s' "$out" | jq -r .group_key) != $GK"
  echo "        node$i: share index $(printf '%s' "$out" | jq -r .index), verifying share $(printf '%s' "$out" | jq -r .verifying_share | cut -c1-16)…"
done
pass "every node's derived group key matches epoch_activated"

# 3. The M4a flow on the DKG key.
KEYSTONE_DEMO_REUSE=1 bash "$ROOT/scripts/demo-M4a.sh"
echo "M4b demo passed"
