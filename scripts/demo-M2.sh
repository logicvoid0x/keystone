#!/usr/bin/env bash
# M2 demo (docs/specs/M2-localnet-determinism.md, acceptance check 3):
#   1. make localnet-start; all 4 nodes reach height 30 with identical app hashes (RPC);
#   2. the M1 liquidation scenario across the localnet; vault_liquidated at the same
#      height on all 4 nodes;
#   3. make localnet-byz MODE=extra_magic for 60 blocks: height keeps increasing and the
#      honest nodes count rejects;
#   4. scripts/add-operator.sh: the 5th operator signs blocks.
# Needs: Docker, curl, jq. Leaves the localnet stopped (homes kept; make localnet-clean).
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"
BIN=${BIN:-$ROOT/build/keystoned}
[ -x "$BIN" ] || BIN="$BIN.exe"
H=$ROOT/localnet/homes
CHAIN=keystone-local-1
FEED=http://127.0.0.1:18080
RPCS=(http://127.0.0.1:26657 http://127.0.0.1:26667 http://127.0.0.1:26677 http://127.0.0.1:26687)
PROMS=(http://127.0.0.1:26660 http://127.0.0.1:26670 http://127.0.0.1:26680 http://127.0.0.1:26690)
WORK=$ROOT/.demo/M2
KR=(--keyring-backend test --chain-id "$CHAIN")

fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "ok:   $*"; }
height() { curl -fsS -m 3 "${1:-${RPCS[0]}}/status" 2>/dev/null | jq -r '.result.sync_info.latest_block_height' 2>/dev/null || echo 0; }
wait_height() { # $1 target, $2 timeout seconds
  local start=$SECONDS h
  while :; do
    h=$(height); [[ "$h" =~ ^[0-9]+$ ]] || h=0
    [ "$h" -ge "$1" ] && return 0
    [ $((SECONDS - start)) -lt "${2:-300}" ] || fail "timeout waiting for height $1 (at $h)"
    sleep 2
  done
}
q() { "$BIN" q "$@" --home "$H/node0" -o json; }
price_field() { q oracle price | jq -r ".price.$1"; }
feed() { curl -fsS -X POST "$FEED/admin/$1" -H 'Content-Type: application/json' -d "$2" >/dev/null; }

# wait_tx <broadcast-json>: wait for inclusion and require code 0.
wait_tx() {
  local out=$1 hash res
  [ "$(printf '%s' "$out" | jq -r .code)" = "0" ] || fail "CheckTx: $out"
  hash=$(printf '%s' "$out" | jq -r .txhash)
  res=$("$BIN" q wait-tx "$hash" --timeout 60s --home "$H/node0" -o json) || fail "not included: $hash"
  [ "$(printf '%s' "$res" | jq -r .code)" = "0" ] || fail "tx failed: $(printf '%s' "$res" | jq -r .raw_log)"
}

# utx <node> <key> <module> <cmd> [args...]: user tx from node<N>'s keyring.
utx() {
  local n=$1 key=$2; shift 2
  wait_tx "$("$BIN" tx "$@" --from "$key" --home "$H/node$n" "${KR[@]}" --gas 2000000 --yes -o json)"
}

# atx <module> <cmd> [args...]: admin tx, signed by the 3-of-4 operator multisig.
atx() {
  local admin; admin=$("$BIN" keys show admin -a --home "$H/node0" --keyring-backend test)
  "$BIN" tx "$@" --from admin --generate-only --home "$H/node0" "${KR[@]}" --gas 2000000 > "$WORK/a.json"
  for k in 0 1 2; do
    "$BIN" tx sign "$WORK/a.json" --multisig "$admin" --from operator --sign-mode amino-json \
      --home "$H/node$k" "${KR[@]}" --output-document "$WORK/s$k.json"
  done
  "$BIN" tx multi-sign "$WORK/a.json" admin "$WORK/s0.json" "$WORK/s1.json" "$WORK/s2.json" \
    --home "$H/node0" "${KR[@]}" --output-document "$WORK/m.json"
  wait_tx "$("$BIN" tx broadcast "$WORK/m.json" --home "$H/node0" -o json)"
}

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
utx 0 test vault open-vault
atx vault dev credit-collateral 1 50000000
utx 0 test vault mint 1 30000000000
utx 1 operator vault open-vault
atx vault dev credit-collateral 2 200000000
utx 1 operator vault mint 2 100000000000
utx 1 operator liquidation deposit-pool 50000000000
pass "vault 1: 0.5 BTC / 30,000 KUSD; vault 2: 2 BTC / 100,000 KUSD; pool 50,000 KUSD (admin txs signed 3-of-4)"

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
