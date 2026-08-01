#!/usr/bin/env bash
# M2 demo (docs/specs/M2-localnet-determinism.md, acceptance check 3):
#   1. make localnet-start; all 4 nodes reach height 30 with identical app hashes (RPC);
#   2. the M1 liquidation scenario across the localnet; vault_liquidated at the same
#      height on all 4 nodes;
#   3. make localnet-byz MODE=extra_magic for 60 blocks: height keeps increasing and the
#      honest nodes count rejects;
#   4. scripts/add-operator.sh: the 5th operator signs blocks.
# Since M3 the vaults are funded through regtest deposits (the dev credit message is
# gone). Needs: Docker, curl, jq. Leaves the localnet stopped (homes kept; make
# localnet-clean).
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"
WORK=$ROOT/.demo/M2
# shellcheck source=lib/localnet.sh
. "$ROOT/scripts/lib/localnet.sh"

rm -rf "$WORK"; mkdir -p "$WORK"
make --no-print-directory localnet-clean >/dev/null 2>&1 || true
make --no-print-directory localnet-start >"$WORK/start.log" 2>&1 || { tail -20 "$WORK/start.log"; fail "make localnet-start"; }
pass "localnet started (4 nodes + mockfeed)"

# 1. Height 30, identical app hashes on all 4 nodes.
wait_height 31 300
for ((h = 1; h <= 30; h++)); do
  ref=$(curl -fsS "${RPCS[0]}/block?height=$h" | jq -r .result.block.header.app_hash)
  for i in 1 2 3; do
    got=$(curl -fsS "${RPCS[$i]}/block?height=$h" | jq -r .result.block.header.app_hash)
    [ "$got" = "$ref" ] || fail "height $h: node$i app hash $got != node0 $ref"
  done
done
pass "heights 1..30: identical app hashes on all 4 nodes (height 30: ${ref:0:16}…)"

# 2. M1 liquidation scenario. A = node0 "test", B = node1 "operator"; admin = multisig.
for _ in $(seq 1 30); do [ "$(price_field status)" = "PRICE_STATUS_ACTIVE" ] && break; sleep 2; done
[ "$(price_field status)" = "PRICE_STATUS_ACTIVE" ] || fail "oracle not ACTIVE"
B=$("$BIN" keys show operator -a --home "$H/node1" --keyring-backend test)
btc_ready
mine 101 # faucet coins mature
btc_ready
utx 0 test vault open-vault
fund_vault 1 0.5
utx 0 test vault mint 1 30000000000
utx 1 operator vault open-vault
fund_vault 2 2
utx 1 operator vault mint 2 100000000000
utx 1 operator liquidation deposit-pool 50000000000
pass "vault 1: 0.5 BTC / 30,000 KUSD; vault 2: 2 BTC / 100,000 KUSD (regtest deposits); pool 50,000 KUSD"

DROP_H=$(height)
feed price '{"usd":"76000"}'
start=$SECONDS
while [ "$(price_field spot_micro_usd)" != "76000000000" ]; do
  [ $((SECONDS - start)) -lt 180 ] || fail "spot never reached 76,000 (at $(price_field spot_micro_usd))"
  sleep 2
done
NOW=$(height)
liq_h=()
for i in 0 1 2 3; do
  found=
  for ((h = DROP_H; h <= NOW; h++)); do
    ev=$(curl -fsS "${RPCS[$i]}/block_results?height=$h" | jq -c '[.result.finalize_block_events[]? | select(.type=="vault_liquidated")
          | (.attributes | map({(.key): .value}) | add)] | .[0] // empty')
    if [ -n "$ev" ]; then found=$h; break; fi
  done
  [ -n "$found" ] || fail "node$i: no vault_liquidated between $DROP_H and $NOW"
  [ "$(printf '%s' "$ev" | jq -r .id)" = "1" ] && [ "$(printf '%s' "$ev" | jq -r .mode)" = "POOL" ] || fail "node$i: $ev"
  liq_h+=("$found")
done
[ "${liq_h[0]}" = "${liq_h[1]}" ] && [ "${liq_h[0]}" = "${liq_h[2]}" ] && [ "${liq_h[0]}" = "${liq_h[3]}" ] \
  || fail "vault_liquidated at different heights: ${liq_h[*]}"
spot=$(curl -fsS "${RPCS[0]}/block_results?height=${liq_h[0]}" | jq -r '[.result.finalize_block_events[] | select(.type=="price_updated")
        | (.attributes | map({(.key): .value}) | add)][0].spot')
[ "$(q vault vault 1 | jq -r .vault.debt)" = "0" ] || fail "vault 1 still has debt"
pass "vault_liquidated{id: 1, mode: POOL} at height ${liq_h[0]} on all 4 nodes (spot $spot); vault 1 debt 0"

# 3. Byzantine node3 (extra_magic) for 60 blocks.
make --no-print-directory localnet-byz MODE=extra_magic >/dev/null 2>&1 || fail "localnet-byz"
B0=$(height)
rejects() { curl -fsS -m 3 "${PROMS[$1]}/metrics" | awk '/^keystone_process_proposal_rejects\{reason="extra_magic"\}/ {print $2}' | head -1; }
wait_height $((B0 + 60)) 900
B1=$(height)
total=0
for i in 0 1 2; do
  r=$(rejects "$i"); r=${r:-0}; r=${r%.*}
  [ "$r" -gt 0 ] || fail "node$i counted no extra_magic rejects"
  total=$((total + r))
done
make --no-print-directory localnet-byz MODE= >/dev/null 2>&1 || fail "restore honest node3"
pass "node3 byzantine (extra_magic): height $B0 → $B1 (+$((B1 - B0))), honest nodes counted $total rejects"

# 4. A 5th operator via the admin multisig.
bash "$ROOT/scripts/add-operator.sh" | sed 's/^/      /' || fail "add-operator.sh"
pass "5th operator added and signing"

make --no-print-directory localnet-stop >/dev/null 2>&1 || true
pass "localnet stopped (homes kept in localnet/homes)"
echo "M2 demo passed"
