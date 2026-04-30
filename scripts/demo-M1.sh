#!/usr/bin/env bash
# M1 demo (docs/specs/M1-oracle-vaults-liquidations.md, acceptance check 3), one node,
# mockfeed at $100,000:
#   1. A opens vault 1; admin dev-credits 50,000,000 sat; A mints 30,000 KUSD;
#   2. B opens vault 2; admin dev-credits 200,000,000 sat; B mints 100,000 KUSD and
#      deposits 50,000 KUSD into the stability pool;
#   3. mockfeed → $76,000; follow the spot walk 95,000 → … → 76,000;
#   4. vault 1 is liquidated through the pool in the block where spot first reaches
#      77,378.09375: vault_liquidated{id: 1, mode: POOL};
#   5. vault 1 debt is 0; B's claimable BTC equals the seized amount (within rounding);
#   6. mockfeed down: after 20 blocks STALE and MsgMint is rejected; back up → ACTIVE.
# Needs: make build-dev (keystoned with -tags devcredit, mockfeed), curl, jq, base64.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
BIN=${BIN:-$ROOT/build/keystoned}
[ -x "$BIN" ] || BIN="$BIN.exe"
FEED_BIN=${FEED_BIN:-$ROOT/build/mockfeed}
[ -x "$FEED_BIN" ] || FEED_BIN="$FEED_BIN.exe"
WORK=${WORK:-$ROOT/.demo/M1}
HOME0="$WORK/node0"
RPC=${RPC:-http://127.0.0.1:26657}
FEED=${FEED:-http://127.0.0.1:18080}
CHAIN=keystone-local-1

for tool in curl jq; do
  command -v "$tool" >/dev/null || { echo "missing tool: $tool" >&2; exit 1; }
done
[ -x "$BIN" ] || { echo "binary not found: $BIN (run make build-dev)" >&2; exit 1; }
[ -x "$FEED_BIN" ] || { echo "mockfeed not found: $FEED_BIN (run make build-dev)" >&2; exit 1; }

fail() { echo "FAIL: $*" >&2; [ -f "$WORK/node.log" ] && tail -30 "$WORK/node.log" >&2; exit 1; }
pass() { echo "ok:   $*"; }

rm -rf "$WORK"
mkdir -p "$WORK"

"$FEED_BIN" --listen "${FEED#http://}" --price 100000 >"$WORK/mockfeed.log" 2>&1 &
FEED_PID=$!
NODE_PID=
cleanup() {
  for pid in $NODE_PID $FEED_PID; do
    if kill -0 "$pid" 2>/dev/null; then kill "$pid" 2>/dev/null || true; wait "$pid" 2>/dev/null || true; fi
  done
}
trap cleanup EXIT
for _ in $(seq 1 50); do curl -fsS "$FEED/admin/state" >/dev/null 2>&1 && break; sleep 0.2; done
curl -fsS "$FEED/admin/state" >/dev/null || fail "mockfeed did not start"
pass "mockfeed at \$100,000 on $FEED"

"$BIN" testnet init-files --operators 1 --output "$WORK" --chain-id "$CHAIN" --mockfeed "$FEED" >/dev/null 2>&1 \
  || fail "testnet init-files"
"$BIN" start --home "$HOME0" >"$WORK/node.log" 2>&1 &
NODE_PID=$!
pass "node started"

feed() { curl -fsS -X POST "$FEED/admin/$1" -H 'Content-Type: application/json' -d "$2" >/dev/null; }
height() { curl -fsS "$RPC/status" 2>/dev/null | jq -r '.result.sync_info.latest_block_height' 2>/dev/null || echo 0; }
q() { "$BIN" q "$@" --home "$HOME0" -o json; }
addr() { "$BIN" keys show "$1" -a --home "$HOME0" --keyring-backend test; }
price_field() { q oracle price | jq -r ".price.$1"; }

wait_height() { # wait until height >= $1
  local start=$SECONDS
  while :; do
    local h; h=$(height); [[ "$h" =~ ^[0-9]+$ ]] || h=0
    [ "$h" -ge "$1" ] && return 0
    kill -0 "$NODE_PID" 2>/dev/null || fail "node exited"
    [ $((SECONDS - start)) -lt 300 ] || fail "timeout waiting for height $1"
    sleep 1
  done
}

wait_status() { # wait for oracle status $1 within $2 blocks
  local target=$1 limit=$2 from; from=$(height)
  while :; do
    [ "$(price_field status)" = "$target" ] && return 0
    [ $(( $(height) - from )) -le "$limit" ] || return 1
    sleep 1
  done
}

# tx <from-key> <expect: ok|fail> <module> <cmd> [args...]: broadcast, wait for inclusion.
tx() {
  local from=$1 expect=$2; shift 2
  local out hash res code
  out=$("$BIN" tx "$@" --from "$from" --home "$HOME0" --keyring-backend test --chain-id "$CHAIN" \
    --gas 2000000 --yes -o json 2>&1) || fail "tx $* (broadcast): $out"
  hash=$(printf '%s' "$out" | jq -r '.txhash')
  code=$(printf '%s' "$out" | jq -r '.code')
  [ "$code" = "0" ] || { [ "$expect" = fail ] && { echo "$out"; return 0; }; fail "tx $* rejected by CheckTx: $out"; }
  res=$("$BIN" q wait-tx "$hash" --timeout 60s --home "$HOME0" -o json 2>&1) || fail "tx $* not included: $res"
  code=$(printf '%s' "$res" | jq -r '.code')
  if [ "$expect" = ok ]; then
    [ "$code" = "0" ] || fail "tx $* failed: $(printf '%s' "$res" | jq -r '.raw_log')"
  else
    [ "$code" != "0" ] || fail "tx $* was expected to fail"
  fi
  printf '%s' "$res"
}

wait_height 3
wait_status PRICE_STATUS_ACTIVE 20 || fail "price never ACTIVE (status $(price_field status))"
pass "oracle ACTIVE at spot $(price_field spot_micro_usd)"

A=$(addr test); B=$(addr operator)

# 1. Vault 1 for A.
tx test ok vault open-vault >/dev/null
tx admin ok vault dev credit-collateral 1 50000000 >/dev/null
tx test ok vault mint 1 30000000000 >/dev/null
pass "A: vault 1, 0.5 BTC, 30,000 KUSD"

# 2. Vault 2 for B and the stability pool.
tx operator ok vault open-vault >/dev/null
tx admin ok vault dev credit-collateral 2 200000000 >/dev/null
tx operator ok vault mint 2 100000000000 >/dev/null
tx operator ok liquidation deposit-pool 50000000000 >/dev/null
pool=$(q liquidation pool | jq -r '.total_deposits')
[ "$pool" = "50000000000" ] || fail "pool total $pool"
pass "B: vault 2, 2 BTC, 100,000 KUSD, 50,000 KUSD in the pool"

# 3. Price drop: follow the spot walk.
DROP_H=$(height)
feed price '{"usd":"76000"}'
pass "mockfeed → \$76,000 at height $DROP_H"
want=(95000000000 90250000000 85737500000 81450625000 77378093750 76000000000)
seen=()
start=$SECONDS
while [ "${#seen[@]}" -lt "${#want[@]}" ]; do
  s=$(price_field spot_micro_usd)
  if [ "$s" != "100000000000" ] && { [ "${#seen[@]}" -eq 0 ] || [ "${seen[-1]}" != "$s" ]; }; then
    seen+=("$s")
    echo "      spot $s at height $(height)"
  fi
  [ $((SECONDS - start)) -lt 120 ] || fail "spot walk stalled: ${seen[*]}"
  sleep 0.5
done
[ "${seen[*]}" = "${want[*]}" ] || fail "spot walk ${seen[*]}, want ${want[*]}"
pass "spot walk 95,000 → 90,250 → 85,737.5 → 81,450.625 → 77,378.09375 → 76,000"

# 4. Find the liquidation block and check its spot.
NOW=$(height)
LIQ_H=; LIQ_EV=
for ((h = DROP_H; h <= NOW; h++)); do
  ev=$(curl -fsS "$RPC/block_results?height=$h" | jq -c '[.result.finalize_block_events[]? | select(.type=="vault_liquidated")
        | (.attributes | map({(.key): .value}) | add)]')
  if [ "$(printf '%s' "$ev" | jq 'length')" -gt 0 ]; then LIQ_H=$h; LIQ_EV=$(printf '%s' "$ev" | jq -c '.[0]'); break; fi
done
[ -n "$LIQ_H" ] || fail "no vault_liquidated event between heights $DROP_H and $NOW"
[ "$(printf '%s' "$LIQ_EV" | jq -r '.id')" = "1" ] || fail "liquidated vault: $LIQ_EV"
[ "$(printf '%s' "$LIQ_EV" | jq -r '.mode')" = "POOL" ] || fail "liquidation mode: $LIQ_EV"
liq_spot=$(curl -fsS "$RPC/block_results?height=$LIQ_H" | jq -r '[.result.finalize_block_events[]? | select(.type=="price_updated")
        | (.attributes | map({(.key): .value}) | add)][0].spot')
[ "$liq_spot" = "77378093750" ] || fail "liquidation at spot $liq_spot, want 77378093750"
SEIZED=$(printf '%s' "$LIQ_EV" | jq -r '.seized_sat')
pass "vault_liquidated{id: 1, mode: POOL} at height $LIQ_H, spot 77,378.09375, seized $SEIZED sat"

# 5. Vault 1 debt 0; B's claimable = seized (B claims with a zero withdrawal).
debt=$(q vault vault 1 | jq -r '.vault.debt')
[ "$debt" = "0" ] || fail "vault 1 debt $debt"
tx operator ok liquidation withdraw-pool 0 >/dev/null
claim=$(q custody claimable "$B" | jq -r '.claimable_sat')
diff=$(( claim > SEIZED ? claim - SEIZED : SEIZED - claim ))
[ "$diff" -le 1 ] || fail "B claimable $claim, seized $SEIZED"
pass "vault 1 debt 0; B claimable $claim sat (seized $SEIZED)"

# 6. Feed down → STALE after 20 blocks, mint rejected; feed up → ACTIVE.
feed down '{"exchange":"*"}'
DOWN_H=$(height)
wait_status PRICE_STATUS_STALE 30 || fail "not STALE 30 blocks after the feed went down"
STALE_H=$(height)
[ $((STALE_H - DOWN_H)) -ge 20 ] || fail "STALE after only $((STALE_H - DOWN_H)) blocks"
res=$(tx operator fail vault mint 2 1000000000)
printf '%s' "$res" | grep -q "price status" || fail "mint rejected for another reason: $res"
pass "feed down at $DOWN_H: STALE at $STALE_H; MsgMint rejected"
feed up '{"exchange":"*"}'
wait_status PRICE_STATUS_ACTIVE 120 || fail "status did not return to ACTIVE (status $(price_field status))"
pass "feed up: status ACTIVE at height $(height)"

cleanup
trap - EXIT
pass "node and mockfeed stopped"
echo "M1 demo passed"
