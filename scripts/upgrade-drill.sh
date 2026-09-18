#!/usr/bin/env bash
# Upgrade drill (spec M6-T6, design §17) rehearsed on the docker localnet:
#   1. the 4 nodes run the pre-v2 release (keystoned-pre-v2: no handler for "v2");
#   2. the admin multisig schedules x/upgrade plan "v2" at height U;
#   3. every node stops at U (UPGRADE "v2" NEEDED);
#   4. the nodes restart with the new binary, which applies v2 at U and continues;
#   5. VEs switch to version 2 from height U+1 (prices keep updating from them) and the
#      app hashes stay identical.
# Needs: Docker, curl, jq. Leaves the localnet stopped.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"
WORK=$ROOT/.demo/upgrade
# shellcheck source=lib/localnet.sh
. "$ROOT/scripts/lib/localnet.sh"
COMPOSE=(docker compose -f "$ROOT/localnet/docker-compose.yml")
NODES=(node0 node1 node2 node3)

rm -rf "$WORK"; mkdir -p "$WORK"
make --no-print-directory localnet-clean >/dev/null 2>&1 || true
NODE_BIN=keystoned-pre-v2 NODE3_BIN=keystoned-pre-v2 make --no-print-directory localnet-start >"$WORK/start.log" 2>&1 \
  || { tail -20 "$WORK/start.log"; fail "make localnet-start"; }
wait_height 5 300
pass "localnet on the pre-v2 release ($(docker exec keystone-localnet-node0-1 sh -c 'ps -o args= -p 1' 2>/dev/null || echo keystoned-pre-v2))"

# 2. Schedule v2.
U=$(( $(height) + 20 ))
atx admin-upgrade v2 "$U"
plan=$(q upgrade plan)
[ "$(printf '%s' "$plan" | jq -r .plan.name)" = "v2" ] || fail "no v2 plan: $plan"
pass "admin multisig scheduled upgrade v2 at height $U"

# 3. Every node stops in FinalizeBlock of U (CometBFT has stored block U, not applied it).
start=$SECONDS
until [ "$(height)" -ge $((U - 1)) ]; do
  [ $((SECONDS - start)) -lt 300 ] || fail "chain did not reach $((U - 1))"; sleep 2
done
sleep 15
for n in "${NODES[@]}"; do
  "${COMPOSE[@]}" logs "$n" 2>&1 | grep -q 'UPGRADE "v2" NEEDED' || fail "$n did not stop for v2"
done
pass "all 4 nodes stopped at height $U (UPGRADE \"v2\" NEEDED; block store at $(height))"

# 4. Restart with the new binary.
NODE_BIN=keystoned NODE3_BIN=keystoned "${COMPOSE[@]}" up -d --no-deps --force-recreate "${NODES[@]}" >/dev/null 2>&1
wait_height $((U + 5)) 300
applied=$(q upgrade applied v2 | jq -r .height)
[ "$applied" = "$U" ] || fail "v2 applied at $applied, want $U"
pass "restarted on the new binary: v2 applied at $U, chain at $(height) (no halt beyond the upgrade height)"

# 5. VE version 2 from U+1: prices still update after the switch.
upd=$(find_event 0 $((U + 2)) $((U + 5)) price_updated 'true')
[ -n "$upd" ] || fail "no price update from version-2 VEs"
last=$(( $(height) - 1 ))
for i in 1 2 3; do
  [ "$(curl -fsS "${RPCS[$i]}/block?height=$last" | jq -r .result.block.header.app_hash)" = \
    "$(curl -fsS "${RPCS[0]}/block?height=$last" | jq -r .result.block.header.app_hash)" ] || fail "node$i app hash differs at $last"
done
pass "prices update at ${upd%% *} from version-2 VEs; identical app hashes at $last"
make --no-print-directory localnet-stop >/dev/null 2>&1 || true
echo "upgrade drill passed"
